// 事务变更测试：验证请求重放、预览回滚、失败回滚与并发版本冲突。

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"fulibu-go/internal/database"
)

// TestMutationReplayRollbackAndPreview 验证幂等重放不重复执行，预览和执行失败均回滚数据库变更。
func TestMutationReplayRollbackAndPreview(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(1,'one','one','hash','salt')`)
	db.Exec(`INSERT INTO assets(id,user_id,name,category,amount) VALUES(1,1,'cash','deposit',100)`)
	l := NewLedger(db)
	ctx := context.Background()
	calls := 0
	apply := /* 模拟资产写入并计数，用于验证幂等请求仅执行一次。 */ func(tx *sql.Tx) (MutationResult, error) {
		calls++
		_, err := tx.Exec(`UPDATE assets SET amount=200 WHERE id=1`)
		return MutationResult{Status: 200, Body: json.RawMessage(`{"ok":true}`)}, err
	}
	o := MutationOptions{UserID: 1, Operation: "assets.patch", Key: "one", Fingerprint: "a"}
	first, err := l.Mutate(ctx, o, apply)
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.Mutate(ctx, o, apply)
	if err != nil || calls != 1 || string(first.Body) != string(second.Body) {
		t.Fatalf("replay: calls=%d err=%v", calls, err)
	}
	o.Fingerprint = "b"
	_, err = l.Mutate(ctx, o, apply)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 409 {
		t.Fatalf("conflict: %v", err)
	}
	o.Key = "preview"
	o.DryRun = true
	_, err = l.Mutate(ctx, o /* 模拟预览中的资产写入，验证事务结束后数据被回滚。 */, func(tx *sql.Tx) (MutationResult, error) {
		_, e := tx.Exec(`UPDATE assets SET amount=999 WHERE id=1`)
		return MutationResult{Status: 200, Body: json.RawMessage(`{"ok":true}`)}, e
	})
	if err != nil {
		t.Fatal(err)
	}
	o.Key = "rollback"
	o.DryRun = false
	_, err = l.Mutate(ctx, o /* 写入资产后返回错误，验证失败变更不会保留。 */, func(tx *sql.Tx) (MutationResult, error) {
		tx.Exec(`UPDATE assets SET amount=888 WHERE id=1`)
		return MutationResult{}, errors.New("failure")
	})
	if err == nil {
		t.Fatal("failure swallowed")
	}
	var amount, version, count, logs int
	db.QueryRow(`SELECT amount,version FROM assets WHERE id=1`).Scan(&amount, &version)
	db.QueryRow(`SELECT COUNT(*) FROM mutation_requests`).Scan(&count)
	db.QueryRow(`SELECT COUNT(*) FROM operation_logs`).Scan(&logs)
	if amount != 200 || version != 2 || count != 1 || logs != 1 {
		t.Fatalf("state: amount=%d version=%d requests=%d logs=%d", amount, version, count, logs)
	}
}

// TestMutationConcurrentVersionConflict 验证并发更新同一版本时只有一个事务成功，其余请求返回冲突。
func TestMutationConcurrentVersionConflict(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(1,'one','one','hash','salt')`)
	db.Exec(`INSERT INTO assets(id,user_id,name,category,amount) VALUES(1,1,'cash','deposit',100)`)
	l := NewLedger(db)
	results := make(chan error, 2)
	for _, key := range []string{"a", "b"} {
		go /* 并发提交使用不同幂等键的资产更新，收集每个请求的结果。 */ func(key string) {
			_, err := l.Mutate(context.Background(), MutationOptions{UserID: 1, Operation: "patch", Key: key, Fingerprint: key} /* 仅更新指定旧版本的资产，并将未命中的更新识别为版本冲突。 */, func(tx *sql.Tx) (MutationResult, error) {
				res, e := tx.Exec(`UPDATE assets SET amount=amount+100 WHERE id=1 AND version=1`)
				if e != nil {
					return MutationResult{}, e
				}
				n, _ := res.RowsAffected()
				if n == 0 {
					return MutationResult{}, &APIError{Status: 409, Code: "version_conflict", Message: "conflict"}
				}
				return MutationResult{Status: 200, Body: json.RawMessage(`{}`)}, nil
			})
			results <- err
		}(key)
	}
	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else {
			var e *APIError
			if !errors.As(err, &e) || e.Status != 409 {
				t.Fatal(err)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("successful updates: %d", successes)
	}
}
