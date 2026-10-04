// 命令行 API 集成测试：通过真实 HTTP 服务验证令牌流程及旧网页表单兼容性。

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"fulibu-go/internal/database"
)

// TestCLIAPIHTTPWorkflow 验证真实 HTTP 环境中的令牌认证、资产业务、幂等写入与组合查询流程。
func TestCLIAPIHTTPWorkflow(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(New(db))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	cli := &http.Client{Timeout: 5 * time.Second}
	call := /* 通过真实 HTTP 客户端提交请求，并解析状态码与 JSON 响应。 */ func(client *http.Client, bearer, method, path, payload, key string) (int, map[string]any) {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		v := map[string]any{}
		if json.NewDecoder(resp.Body).Decode(&v) != nil {
			t.Fatal("invalid JSON response")
		}
		return resp.StatusCode, v
	}
	status, v := call(browser, "", "POST", "/api/auth/register", `{"displayName":"测试用户","email":"test@example.com","password":"password123"}`, "")
	if status != 201 {
		t.Fatalf("register %d %v", status, v)
	}
	status, v = call(browser, "", "POST", "/api/auth/tokens", `{"name":"cli","scope":"write"}`, "")
	if status != 201 {
		t.Fatalf("token %d %v", status, v)
	}
	tok := v["token"].(string)
	status, v = call(cli, tok, "POST", "/api/assets", `{"name":"cash","category":"deposit","amount":1000}`, "add")
	if status != 201 {
		t.Fatalf("create %d %v", status, v)
	}
	id := strconv.Itoa(int(v["asset"].(map[string]any)["id"].(float64)))
	patch := `{"id":` + id + `,"version":1,"amount":1200}`
	status, v = call(cli, tok, "PATCH", "/api/assets?dryRun=true", patch, "")
	if status != 200 || v["dryRun"] != true {
		t.Fatalf("preview %d %v", status, v)
	}
	status, v = call(cli, tok, "PATCH", "/api/assets", patch, "edit")
	if status != 200 {
		t.Fatalf("patch %d %v", status, v)
	}
	status, _ = call(cli, tok, "PATCH", "/api/assets", patch, "edit")
	if status != 200 {
		t.Fatalf("replay %d", status)
	}
	status, v = call(cli, tok, "GET", "/api/portfolio/summary", "", "")
	if status != 200 || v["totalCnyMinor"] != float64(120000) {
		t.Fatalf("summary %d %v", status, v)
	}
	status, _ = call(cli, tok, "POST", "/api/assets/archive", `{"id":`+id+`,"version":2}`, "archive")
	if status != 200 {
		t.Fatalf("archive %d", status)
	}
	status, v = call(cli, tok, "GET", "/api/portfolio/summary", "", "")
	if status != 200 || v["totalCnyMinor"] != float64(0) {
		t.Fatalf("archived summary %d %v", status, v)
	}
	status, _ = call(browser, "", "DELETE", "/api/auth/tokens?id=1", "", "")
	if status != 200 {
		t.Fatalf("revoke %d", status)
	}
	status, _ = call(cli, tok, "GET", "/api/assets", "", "")
	if status != 401 {
		t.Fatalf("revoked %d", status)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM operation_logs`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit count=%d err=%v", n, err)
	}
}

// TestCLIAPICookieAssetWorkflow 验证网页登录会话通过当前契约创建、修改和归档资产。
func TestCLIAPICookieAssetWorkflow(t *testing.T) {
	_, h, c := apiFixture(t)
	// Exercises the current webpage contract with explicit keys and versions.
	status, v := requestAPI(t, h, c, "", "POST", "/api/assets", `{"name":"存款","category":"deposit","code":"","amount":100,"currency":"CNY","annualRate":2,"note":"","investmentStrategy":"none"}`, "create")
	if status != 201 {
		t.Fatalf("create %d %v", status, v)
	}
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/assets", `{"id":1,"version":1,"amount":150,"currency":"CNY"}`, "amount")
	if status != 200 || v["after"].(map[string]any)["amount"] != float64(15000) {
		t.Fatalf("patch %d %v", status, v)
	}
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/assets", `{"id":1,"version":2,"annualRate":3}`, "rate")
	if status != 200 || v["after"].(map[string]any)["annual_rate"] != float64(3) {
		t.Fatalf("rate %d %v", status, v)
	}
	for _, payload := range []string{`{"id":1,"note":null}`, `{"id":1,"amount":1} {}`, `null`, `[]`, `{"id":1}`} {
		status, _ = requestAPI(t, h, c, "", "PATCH", "/api/assets", payload, "")
		if status != 400 {
			t.Fatalf("invalid partial %s=%d", payload, status)
		}
	}
}
