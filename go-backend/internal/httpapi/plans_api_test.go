// 计划接口测试：验证收入字段部分更新以及退休目标和明细的维护。

package httpapi

import "testing"

// TestPlansAPIIncomePartialUpdates 验证收入接口部分更新保留未提供字段，并支持版本和预览处理。
func TestPlansAPIIncomePartialUpdates(t *testing.T) {
	db, h, c := apiFixture(t)
	tok := issueToken(t, h, c, "write")
	db.Exec(`INSERT INTO income_settings(user_id,monthly_salary,monthly_savings,annual_bonus,compensation) VALUES(1,2000000,500000,1000000,'{"bonusSettings":{"workStartDate":"2024-01-01","payMonth":2,"payDay":28,"yearOffset":1}}')`)
	payload := `{"version":1,"monthlySavings":0}`
	status, v := requestAPI(t, h, nil, tok, "PATCH", "/api/income?dryRun=true", payload, "")
	if status != 200 {
		t.Fatalf("preview %d %v", status, v)
	}
	status, v = requestAPI(t, h, nil, tok, "PATCH", "/api/income", payload, "income")
	if status != 200 {
		t.Fatalf("patch %d %v", status, v)
	}
	after := v["after"].(map[string]any)
	if after["monthly_salary"] != float64(2000000) || after["monthly_savings"] != float64(0) || after["annual_bonus"] != float64(1000000) {
		t.Fatalf("cleared fields: %v", after)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/income", payload, "income")
	if status != 200 {
		t.Fatalf("replay: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/income", payload, "stale")
	if status != 409 {
		t.Fatalf("version: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/income", `{"version":2,"monthlySavings":-1}`, "bad")
	if status != 400 {
		t.Fatalf("negative: %d", status)
	}
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/income", `{"version":2,"monthlySalary":100}`, "cookie-income")
	if status != 200 || v["income"].(map[string]any)["monthly_salary"] != float64(10000) {
		t.Fatalf("cookie update %d %v", status, v)
	}

}

// TestPlansAPIRetirementItems 验证退休目标明细的创建、修改及接口限制。
func TestPlansAPIRetirementItems(t *testing.T) {
	db, h, c := apiFixture(t)
	tok := issueToken(t, h, c, "write")
	status, v := requestAPI(t, h, nil, tok, "POST", "/api/retirement", `{"name":"住房","category":"housing","currency":"CNY","amount":500000}`, "item")
	if status != 201 {
		t.Fatalf("item %d %v", status, v)
	}
	status, v = requestAPI(t, h, nil, tok, "PATCH", "/api/retirement/items", `{"id":1,"version":1,"amount":600000}`, "update")
	if status != 200 {
		t.Fatalf("update %d %v", status, v)
	}
	if v["after"].(map[string]any)["amount"] != float64(60000000) {
		t.Fatalf("item amount: %v", v)
	}
	db.Exec(`INSERT INTO retirement_goal_items(id,user_id,name,category,amount,currency) VALUES(2,2,'other','fixed',100,'CNY')`)
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/retirement/items", `{"id":2,"version":1,"name":"x"}`, "other")
	if status != 404 {
		t.Fatalf("isolation: %d", status)
	}
	status, _ = requestAPI(t, h, nil, tok, "PATCH", "/api/retirement/items?dryRun=true", `{"id":1,"version":2,"amount":700000}`, "")
	if status != 200 {
		t.Fatalf("preview: %d", status)
	}
	var amount int
	db.QueryRow(`SELECT amount FROM retirement_goal_items WHERE id=1`).Scan(&amount)
	if amount != 60000000 {
		t.Fatal("preview persisted")
	}
}
