// 资产接口测试：覆盖部分更新、幂等重放、归档、定投与金额校验。

package httpapi

import (
	"strconv"
	"testing"
)

// TestAssetsAPIRejectsUnrepresentableSnapshot 验证资产变更导致快照金额超出可表示范围时会拒绝并回滚。
func TestAssetsAPIRejectsUnrepresentableSnapshot(t *testing.T) {
	db, h, c := apiFixture(t)
	tok := issueToken(t, h, c, "write")
	if _, err := db.Exec(`INSERT INTO exchange_rates(currency,cny_rate,rate_date) VALUES('USD',1e12,'2026-10-04')`); err != nil {
		t.Fatal(err)
	}
	status, v := requestAPI(t, h, nil, tok, "POST", "/api/assets", `{"name":"usd","category":"money","amount":100000000,"currency":"USD"}`, "overflow")
	if status != 400 {
		t.Fatalf("invalid conversion persisted: %d %v", status, v)
	}
	status, v = requestAPI(t, h, nil, tok, "POST", "/api/assets?dryRun=true", `{"name":"usd","category":"money","amount":100000000,"currency":"USD"}`, "")
	if status != 400 {
		t.Fatalf("preview accepted invalid conversion: %d %v", status, v)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&count)
	if count != 0 {
		t.Fatal("overflow write did not roll back")
	}
}

// TestAssetsAPIPartialUpdatesReplayAndArchive 验证资产部分更新、幂等请求重放、版本检查与归档行为。
func TestAssetsAPIPartialUpdatesReplayAndArchive(t *testing.T) {
	db, h, c := apiFixture(t)
	tok := issueToken(t, h, c, "write")
	payload := `{"name":"现金","category":"deposit","amount":100,"currency":"CNY"}`
	status, v := requestAPI(t, h, nil, tok, "POST", "/api/assets", payload, "create")
	if status != 201 {
		t.Fatalf("create %d %v", status, v)
	}
	item := v["asset"].(map[string]any)
	id := strconv.Itoa(int(item["id"].(float64)))
	status, replay := requestAPI(t, h, nil, tok, "POST", "/api/assets", payload, "create")
	if status != 201 || replay["asset"].(map[string]any)["id"] != item["id"] {
		t.Fatalf("replay %d %v", status, replay)
	}
	status, _ = requestAPI(t, h, nil, tok, "POST", "/api/assets", `{"name":"other","category":"deposit","amount":200}`, "create")
	if status != 409 {
		t.Fatalf("different payload: %d", status)
	}
	patch := `{"id":` + id + `,"version":1,"note":"备注"}`
	status, v = requestAPI(t, h, nil, tok, "PATCH", "/api/assets?dryRun=true", patch, "")
	if status != 200 || v["dryRun"] != true {
		t.Fatalf("preview %d %v", status, v)
	}
	var version int
	db.QueryRow(`SELECT version FROM assets WHERE id=?`, id).Scan(&version)
	if version != 1 {
		t.Fatal("preview advanced version")
	}
	status, v = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", patch, "patch")
	if status != 200 {
		t.Fatalf("patch %d %v", status, v)
	}
	after := v["after"].(map[string]any)
	if after["amount"] != float64(10000) || after["note"] != "备注" || after["version"] != float64(2) {
		t.Fatalf("partial: %v", after)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", patch, "patch")
	if status != 200 {
		t.Fatalf("replay stale version: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", patch, "other")
	if status != 409 {
		t.Fatalf("version: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "GET", "/api/assets?id=999", "", "")
	if status != 404 {
		t.Fatalf("not found: %d", status)
	}
	db.Exec(`INSERT INTO asset_history(user_id,snapshot_date,total_cny,trigger) VALUES(1,'2000-01-01',999,'old')`)
	status, v = requestAPI(t, h, nil, tok, "POST", "/api/assets/archive", `{"id":`+id+`,"version":2}`, "archive")
	if status != 200 {
		t.Fatalf("archive %d %v", status, v)
	}
	status, v = requestAPI(t, h, nil, tok, "GET", "/api/assets", "", "")
	if status != 200 || len(v["assets"].([]any)) != 0 {
		t.Fatalf("archived listed: %v", v)
	}
	status, v = requestAPI(t, h, nil, tok, "GET", "/api/assets?includeArchived=true", "", "")
	if status != 200 || len(v["assets"].([]any)) != 1 {
		t.Fatalf("archive missing: %v", v)
	}
	var oldTotal int
	db.QueryRow(`SELECT total_cny FROM asset_history WHERE snapshot_date='2000-01-01'`).Scan(&oldTotal)
	if oldTotal != 999 {
		t.Fatal("old history changed")
	}
	status, v = requestAPI(t, h, nil, tok, "GET", "/api/retirement", "", "")
	if status != 200 || v["current_cny"] != float64(0) {
		t.Fatalf("retirement includes archived: %v", v)
	}
}

// TestAssetsAPIInvestmentAndValidation 验证资产定投设置和字段校验规则。
func TestAssetsAPIInvestmentAndValidation(t *testing.T) {
	db, h, c := apiFixture(t)
	tok := issueToken(t, h, c, "write")
	if _, err := db.Exec(`INSERT INTO assets(id,user_id,name,category,code,amount,quantity,currency) VALUES(1,1,'fund','fund','000001',10000,100,'CNY'),(2,2,'other','deposit',NULL,1000,NULL,'CNY')`); err != nil {
		t.Fatal(err)
	}
	status, v := requestAPI(t, h, nil, tok, "PATCH", "/api/assets", `{"id":1,"version":1,"investmentStrategy":"monthly","investmentAmount":1000}`, "investment")
	if status != 200 {
		t.Fatalf("independent investment %d %v", status, v)
	}
	for i, payload := range []string{`{"id":1,"version":2,"quantity":-1}`, `{"id":1,"version":2,"currency":"ZZZ"}`, `{"id":1,"version":2,"note":null}`, `{"id":1,"version":2}`, `{"id":1,"version":2,"amount":0}`, `{"id":1,"version":2,"unknown":1}`, `{"id":1,"version":2,"note":"x"} {}`} {
		status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", payload, "invalid"+strconv.Itoa(i))
		if status != 400 {
			t.Fatalf("invalid %s = %d", payload, status)
		}
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", `{"id":2,"version":1,"note":"x"}`, "other")
	if status != 404 {
		t.Fatalf("cross-user: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", `{"id":1,"note":"x"}`, "missing-version")
	if status != 400 {
		t.Fatalf("missing version: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/assets", `{"id":1,"version":2,"note":"x"}`, "")
	if status != 400 {
		t.Fatalf("missing key: %d", status)
	}
}
