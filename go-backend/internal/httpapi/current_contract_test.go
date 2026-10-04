// 当前接口契约测试：验证旧请求被拒绝、写操作需要版本以及删除的幂等与预览行为。

package httpapi

import "testing"

// TestCurrentContractRejectsOldRequests 验证旧接口请求被拒绝，且拒绝后资产与操作日志均保持原值。
func TestCurrentContractRejectsOldRequests(t *testing.T) {
	cases := [] /* 定义被拒绝的旧请求及其预期响应状态。 */ struct {
		name, method, path, payload, key string
		want                             int
	}{
		{"null quantity", "POST", "/api/assets", `{"name":"cash","category":"deposit","amount":100,"quantity":null}`, "null", 400},
		{"zero quantity", "POST", "/api/assets", `{"name":"cash","category":"deposit","amount":100,"quantity":0}`, "zero", 400},
		{"missing key", "POST", "/api/assets", `{"name":"cash","category":"deposit","amount":100}`, "", 400},
		{"empty snapshot", "POST", "/api/history", "", "snapshot", 400},
		{"income put", "PUT", "/api/income", `{"version":0,"monthlySalary":100}`, "income", 405},
		{"single target", "PUT", "/api/retirement", `{"version":0,"targetCny":1000}`, "target", 405},
		{"permanent asset deletion", "DELETE", "/api/assets?id=1", "", "delete", 405},
		{"old item deletion", "DELETE", "/api/retirement?id=1", "", "delete", 405},
		{"undated bonus", "PATCH", "/api/income", `{"version":0,"annualBonus":100}`, "bonus", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name /* 逐项提交旧请求，验证被拒绝后没有资产或审计状态变更。 */, func(t *testing.T) {
			db, h, c := apiFixture(t)
			if _, err := db.Exec(`INSERT INTO assets(user_id,name,category,amount) VALUES(1,'cash','deposit',10000)`); err != nil {
				t.Fatal(err)
			}
			status, v := requestAPI(t, h, c, "", tc.method, tc.path, tc.payload, tc.key)
			if status != tc.want {
				t.Fatalf("status=%d want=%d response=%v", status, tc.want, v)
			}
			var amount, logs int
			if err := db.QueryRow(`SELECT amount FROM assets WHERE id=1`).Scan(&amount); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM operation_logs`).Scan(&logs); err != nil {
				t.Fatal(err)
			}
			if amount != 10000 || logs != 0 {
				t.Fatalf("rejected request changed state: amount=%d logs=%d", amount, logs)
			}
		})
	}
}

// TestCookieUpdatesRequireVersion 验证 Cookie 会话更新资产和收入时同样必须提供版本。
func TestCookieUpdatesRequireVersion(t *testing.T) {
	db, h, c := apiFixture(t)
	if _, err := db.Exec(`INSERT INTO assets(user_id,name,category,amount) VALUES(1,'cash','deposit',10000)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [] /* 定义缺少版本的资产、归档和收入更新请求。 */ struct{ method, path, payload string }{
		{"PATCH", "/api/assets", `{"id":1,"amount":200}`},
		{"POST", "/api/assets/archive", `{"id":1}`},
		{"PATCH", "/api/income", `{"monthlySalary":100}`},
	} {
		status, v := requestAPI(t, h, c, "", tc.method, tc.path, tc.payload, "version")
		if status != 400 {
			t.Fatalf("%s: %d %v", tc.path, status, v)
		}
	}
}

// TestGoalItemDeletionChecksVersionAndSupportsPreviewReplay 验证目标明细删除检查版本，并支持预览回滚与幂等重放。
func TestGoalItemDeletionChecksVersionAndSupportsPreviewReplay(t *testing.T) {
	db, h, c := apiFixture(t)
	status, v := requestAPI(t, h, c, "", "POST", "/api/retirement", `{"name":"目标","amount":1000}`, "create-goal")
	if status != 201 || v["target_cny"] != float64(100000) {
		t.Fatalf("create: %d %v", status, v)
	}
	for _, payload := range []string{`{"id":1}`, `{"id":1,"version":0}`} {
		status, _ = requestAPI(t, h, c, "", "DELETE", "/api/retirement/items", payload, "bad")
		if status != 400 && status != 409 {
			t.Fatalf("invalid version: %d", status)
		}
	}
	payload := `{"id":1,"version":1}`
	status, v = requestAPI(t, h, c, "", "DELETE", "/api/retirement/items?dryRun=true", payload, "")
	if status != 200 || v["dryRun"] != true || v["target_cny"] != float64(0) {
		t.Fatalf("preview: %d %v", status, v)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM retirement_goal_items`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("preview persisted: %d %v", n, err)
	}
	if _, err := db.Exec(`INSERT INTO retirement_goal_items(id,user_id,name,category,amount,currency) VALUES(2,2,'other','fixed',5000,'CNY')`); err != nil {
		t.Fatal(err)
	}
	status, _ = requestAPI(t, h, c, "", "DELETE", "/api/retirement/items", `{"id":2,"version":1}`, "other")
	if status != 404 {
		t.Fatalf("other user deletion: %d", status)
	}
	for range 2 {
		status, v = requestAPI(t, h, c, "", "DELETE", "/api/retirement/items", payload, "delete-goal")
		if status != 200 || v["target_cny"] != float64(0) {
			t.Fatalf("delete/replay: %d %v", status, v)
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM operation_logs WHERE operation='DELETE /api/retirement/items'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate audit: %d %v", n, err)
	}
}
