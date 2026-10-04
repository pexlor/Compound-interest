// 写请求处理：校验请求体、幂等键与版本，并统一输出事务结果和错误。

package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"fulibu-go/internal/service"
)

// mutationInput validates a single object, rejecting explicit nulls rather
// than silently treating them as absent partial-update fields.
// mutationInput 严格解析单个 JSON 对象，校验空值、幂等键与预览参数，并生成请求内容指纹。
func mutationInput(r *http.Request, u *user, dst any) (service.MutationOptions, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	r.Body.Close()
	if err != nil {
		return service.MutationOptions{}, badRequest("请求过大或无法读取")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return service.MutationOptions{}, badRequest("请输入一个 JSON 对象")
	}
	for _, v := range fields {
		if strings.TrimSpace(string(v)) == "null" {
			return service.MutationOptions{}, badRequest("字段不能为 null；未修改的字段请省略")
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return service.MutationOptions{}, badRequest("请求字段或类型无效")
	}
	dry := r.URL.Query().Get("dryRun")
	if dry != "" && dry != "true" && dry != "false" {
		return service.MutationOptions{}, badRequest("dryRun 必须为 true 或 false")
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 || strings.TrimSpace(key) != key {
		return service.MutationOptions{}, badRequest("无效请求编号")
	}
	if dry != "true" && key == "" {
		return service.MutationOptions{}, badRequest("写请求需要 Idempotency-Key")
	}
	canonical, _ := json.Marshal(fields)
	sum := sha256.Sum256([]byte(r.Method + "\n" + r.URL.Path + "\n" + r.URL.Query().Encode() + "\n" + string(canonical)))
	return service.MutationOptions{UserID: u.ID, Operation: r.Method + " " + r.URL.Path, Key: key, Fingerprint: hex.EncodeToString(sum[:]), DryRun: dry == "true"}, nil
}

// badRequest 构造请求参数无效的业务错误。
func badRequest(message string) error {
	return &service.APIError{Status: 400, Code: "invalid_request", Message: message}
}

// notFound 构造记录不存在的业务错误。
func checkVersion(expected *int64, actual int64) error {
	if expected == nil {
		return badRequest("修改需要提供当前 version")
	}
	if *expected != actual {
		return &service.APIError{Status: 409, Code: "version_conflict", Message: "记录已变化，请重新查询"}
	}
	return nil
}

// mutationJSON 将写操作结果序列化为 JSON，并保留对应的 HTTP 状态码。
func mutationJSON(status int, v any) (service.MutationResult, error) {
	raw, err := json.Marshal(v)
	return service.MutationResult{Status: status, Body: raw}, err
}

// writeAPIError 根据业务错误输出状态码和错误信息，对其他错误返回统一内部错误。
func writeAPIError(w http.ResponseWriter, err error) {
	var e *service.APIError
	if errors.As(err, &e) {
		apiError(w, e.Status, e.Code, e.Message)
	} else {
		apiError(w, 500, "internal_error", "操作失败")
	}
}

// writeMutation 输出事务变更结果中保存的状态码和 JSON 响应体。
func writeMutation(w http.ResponseWriter, result service.MutationResult) {
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// mutate 执行账本事务变更，并统一处理成功响应与业务错误。
func (a *app) mutate(w http.ResponseWriter, r *http.Request, o service.MutationOptions, apply func(*sql.Tx) (service.MutationResult, error)) {
	result, err := a.ledger.Mutate(r.Context(), o, apply)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeMutation(w, result)
}
