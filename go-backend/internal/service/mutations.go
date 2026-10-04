// 事务变更服务：统一处理幂等重放、操作审计、预览回滚与实际提交。

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// APIError 封装业务错误的 HTTP 状态码、稳定错误代码和提示信息。
type APIError struct {
	Status        int
	Code, Message string
}

// Error 返回业务错误的文字说明，以实现标准 error 接口。
func (e *APIError) Error() string { return e.Message }

// MutationOptions 描述事务变更的用户、操作、幂等键、内容指纹及预览标记。
type MutationOptions struct {
	UserID                      int64
	Operation, Key, Fingerprint string
	DryRun                      bool
}

// MutationResult 保存写请求的 HTTP 状态码和 JSON 响应体，供响应与幂等重放复用。
type MutationResult struct {
	Status int
	Body   json.RawMessage
}

// Mutate commits the operation, its replay result and audit record together.
// Callbacks must use the supplied transaction, never the parent DB connection.
// Mutate 在同一事务中执行变更、保存幂等结果并记录审计；预览只返回结果而不提交。
// 回调必须使用传入的事务，避免通过外层数据库连接执行写操作。
func (s *Ledger) Mutate(ctx context.Context, o MutationOptions, apply func(*sql.Tx) (MutationResult, error)) (MutationResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if o.Key != "" && !o.DryRun {
		var fingerprint string
		var response string
		var saved MutationResult
		err = tx.QueryRowContext(ctx, `SELECT fingerprint,status,response FROM mutation_requests WHERE user_id=? AND operation=? AND request_key=?`, o.UserID, o.Operation, o.Key).Scan(&fingerprint, &saved.Status, &response)
		saved.Body = json.RawMessage(response)
		if err == nil {
			if fingerprint != o.Fingerprint {
				return MutationResult{}, &APIError{409, "idempotency_conflict", "请求编号已被不同内容使用"}
			}
			return saved, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return MutationResult{}, err
		}
	}
	result, err := apply(tx)
	if err != nil {
		return MutationResult{}, err
	}
	if !json.Valid(result.Body) {
		return MutationResult{}, errors.New("mutation returned invalid JSON")
	}
	if o.DryRun {
		return result, nil
	}
	if o.Key != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO mutation_requests(user_id,operation,request_key,fingerprint,status,response) VALUES(?,?,?,?,?,?)`, o.UserID, o.Operation, o.Key, o.Fingerprint, result.Status, string(result.Body))
		if err != nil {
			return MutationResult{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_logs(user_id,operation,request_key,response) VALUES(?,?,?,?)`, o.UserID, o.Operation, o.Key, string(result.Body))
	if err != nil {
		return MutationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

// Replay avoids network work on retries of an already applied valuation request.
// Replay 查询已提交写请求的保存结果，避免估值重试时重复请求外部行情。
func (s *Ledger) Replay(ctx context.Context, o MutationOptions) (MutationResult, bool, error) {
	if o.Key == "" || o.DryRun {
		return MutationResult{}, false, nil
	}
	var fingerprint string
	var response string
	var saved MutationResult
	err := s.db.QueryRowContext(ctx, `SELECT fingerprint,status,response FROM mutation_requests WHERE user_id=? AND operation=? AND request_key=?`, o.UserID, o.Operation, o.Key).Scan(&fingerprint, &saved.Status, &response)
	saved.Body = json.RawMessage(response)
	if errors.Is(err, sql.ErrNoRows) {
		return saved, false, nil
	}
	if err != nil {
		return saved, false, err
	}
	if fingerprint != o.Fingerprint {
		return saved, false, &APIError{409, "idempotency_conflict", "请求编号已被不同内容使用"}
	}
	return saved, true, nil
}
