// API 令牌测试：验证权限、用户隔离、撤销、过期与参数校验。

package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fulibu-go/internal/database"
)

// apiFixture 创建临时数据库、测试用户和 HTTP 处理器，返回登录会话 Cookie。
func apiFixture(t *testing.T) (*sql.DB, http.Handler, *http.Cookie) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup( /* 测试结束时关闭临时数据库连接。 */ func() { db.Close() })
	for _, email := range []string{"one@example.com", "two@example.com"} {
		if _, err := db.Exec(`INSERT INTO users(email,display_name,password_hash,password_salt) VALUES(?,?,?,?)`, email, email, "hash", "salt"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,1,4102444800)`, digest("test-session")); err != nil {
		t.Fatal(err)
	}
	return db, New(db), &http.Cookie{Name: sessionName, Value: "test-session"}
}

// requestAPI 构造带 Cookie 或 Bearer 令牌的接口请求，并解析响应状态与 JSON 内容。
func requestAPI(t *testing.T, h http.Handler, cookie *http.Cookie, bearer, method, path, payload, key string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(payload))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	v := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("non-JSON response %d: %s", w.Code, w.Body.String())
	}
	return w.Code, v
}

// issueToken 通过登录会话创建指定权限的测试 API 令牌，并返回令牌明文。
func issueToken(t *testing.T, h http.Handler, c *http.Cookie, scope string) string {
	t.Helper()
	status, v := requestAPI(t, h, c, "", "POST", "/api/auth/tokens", `{"name":"cli","scope":"`+scope+`","expiresInDays":30}`, "")
	if status != 201 {
		t.Fatalf("issue token: %d %v", status, v)
	}
	tok, ok := v["token"].(string)
	if !ok || tok == "" {
		t.Fatalf("missing token: %v", v)
	}
	return tok
}

// TestAPITokenPermissionsIsolationAndRevocation 验证 API 令牌的读写权限、用户隔离和撤销后的访问限制。
func TestAPITokenPermissionsIsolationAndRevocation(t *testing.T) {
	db, h, c := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,amount) VALUES(1,'mine','deposit',100),(2,'other','deposit',200)`)
	tok := issueToken(t, h, c, "read")
	status, v := requestAPI(t, h, nil, tok, "GET", "/api/assets", "", "")
	if status != 200 || len(v["assets"].([]any)) != 1 {
		t.Fatalf("isolation: %d %v", status, v)
	}
	for _, path := range []string{"/api/assets", "/api/income", "/api/valuations/refresh"} {
		status, _ = requestAPI(t, h, nil, tok, "POST", path, `{}`, "k")
		if status != 403 {
			t.Fatalf("read write %s = %d", path, status)
		}
	}
	status, _ = requestAPI(t, h, c, "invalid", "GET", "/api/assets", "", "")
	if status != 401 {
		t.Fatalf("invalid bearer fell back to cookie: %d", status)
	}
	status, _ = requestAPI(t, h, c, tok, "POST", "/api/auth/tokens", `{}`, "")
	if status != 403 {
		t.Fatalf("bearer issued token: %d", status)
	}
	status, v = requestAPI(t, h, c, "", "GET", "/api/auth/tokens", "", "")
	if status != 200 || len(v["tokens"].([]any)) != 1 {
		t.Fatalf("list: %d %v", status, v)
	}
	if _, ok := v["tokens"].([]any)[0].(map[string]any)["token"]; ok {
		t.Fatal("list leaked plaintext")
	}
	status, _ = requestAPI(t, h, c, "", "DELETE", "/api/auth/tokens?id=1", "", "")
	if status != 200 {
		t.Fatalf("revoke: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "GET", "/api/assets", "", "")
	if status != 401 {
		t.Fatalf("revoked: %d", status)
	}
	tok = issueToken(t, h, c, "write")
	db.Exec(`UPDATE api_tokens SET expires_at=0`)
	status, _ = requestAPI(t, h, nil, tok, "GET", "/api/assets", "", "")
	if status != 401 {
		t.Fatalf("expired: %d", status)
	}
}

// TestAPITokenValidation 验证令牌名称、权限、有效期及无效认证头的处理。
func TestAPITokenValidation(t *testing.T) {
	_, h, c := apiFixture(t)
	for _, payload := range []string{`{"name":"cli","scope":"admin"}`, `{"name":"cli","scope":"write","expiresInDays":366}`, `{"name":"cli","scope":"read","expiresInDays":0}`, `{"name":"cli","scope":"read"} {}`} {
		status, _ := requestAPI(t, h, c, "", "POST", "/api/auth/tokens", payload, "")
		if status != 400 {
			t.Fatalf("accepted invalid token input: %d %s", status, payload)
		}
	}
	status, _ := requestAPI(t, h, c, "", "POST", "/api/auth/tokens", `{"name":"cli","scope":"read","expiresInDays":null}`, "")
	if status != 400 {
		t.Fatalf("null expiry accepted: %d", status)
	}
}
