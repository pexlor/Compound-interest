// 收入计划接口测试：验证薪酬持久化、现金到账日期、预览和缺失汇率处理。

package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestIncomeCompensationPersistenceAndValidation 验证奖金与期权设置可持久化，并拒绝无效薪酬请求。
func TestIncomeCompensationPersistenceAndValidation(t *testing.T) {
	_, h, c := apiFixture(t)
	payload := `{"version":0,"monthlySalary":20000,"monthlySavings":5000,"annualBonus":100000,"bonusSettings":{"workStartDate":"2024-07-01","payMonth":2,"payDay":28,"yearOffset":1},"options":[{"name":"Grant","currency":"USD","quantity":100,"strikePrice":10,"marketPrice":30,"taxRate":20,"batches":[{"vestDate":"2027-01-01","quantity":100,"cashMode":"date","cashDate":"2027-03-01"}]}]}`
	status, v := requestAPI(t, h, c, "", "PATCH", "/api/income", payload, "income")
	if status != 200 {
		t.Fatalf("save %d %v", status, v)
	}
	status, v = requestAPI(t, h, c, "", "GET", "/api/income", "", "")
	i := v["income"].(map[string]any)
	if status != 200 || i["bonus_settings"] == nil || len(i["options"].([]any)) != 1 || len(i["cashflows"].([]any)) == 0 {
		t.Fatalf("read %d %v", status, v)
	}
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/income", `{"version":1,"monthlySavings":6000}`, "savings")
	if status != 200 || len(v["income"].(map[string]any)["options"].([]any)) != 1 {
		t.Fatalf("partial update %d %v", status, v)
	}
	status, _ = requestAPI(t, h, c, "", "PATCH", "/api/income", `{"version":2,"options":[{"name":"bad","currency":"CNY","quantity":1,"strikePrice":0,"marketPrice":10,"taxRate":0,"batches":[{"vestDate":"2027-01-01","quantity":2,"cashMode":"immediate"}]}]}`, "invalid")
	if status != 400 {
		t.Fatalf("invalid allocation %d", status)
	}
}

// Catches losing the cash date between persistence and the retirement endpoint,
// and prevents a preview from changing the stored retirement plan.
// TestCompensationRetirementDateAndPreview 验证持久化后的变现日期影响退休预测，且预览不会修改已保存计划。
func TestCompensationRetirementDateAndPreview(t *testing.T) {
	_, h, c := apiFixture(t)
	today := time.Now().In(time.FixedZone("CST", 8*3600))
	vest := today.AddDate(0, 1, 0).Format("2006-01-02")
	cash := today.AddDate(0, 2, 0).Format("2006-01-02")
	later := today.AddDate(0, 3, 0).Format("2006-01-02")
	status, v := requestAPI(t, h, c, "", "POST", "/api/retirement", `{"name":"目标","amount":50000}`, "target")
	if status != 201 {
		t.Fatalf("target %d %v", status, v)
	}
	payload := fmt.Sprintf(`{"version":0,"monthlySalary":0,"monthlySavings":0,"annualBonus":0,"options":[{"name":"Grant","currency":"CNY","quantity":100,"strikePrice":0,"marketPrice":1000,"taxRate":0,"batches":[{"vestDate":"%s","quantity":100,"cashMode":"date","cashDate":"%s"}]}]}`, vest, cash)
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/income", payload, "income")
	if status != 200 {
		t.Fatalf("income %d %v", status, v)
	}
	status, v = requestAPI(t, h, c, "", "GET", "/api/retirement", "", "")
	if status != 200 || v["projected_date"] != cash {
		t.Fatalf("cash date %d %v", status, v)
	}
	preview := strings.Replace(strings.Replace(payload, `"version":0`, `"version":1`, 1), cash, later, 1)
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/income?dryRun=true", preview, "")
	if status != 200 {
		t.Fatalf("preview %d %v", status, v)
	}
	_, v = requestAPI(t, h, c, "", "GET", "/api/retirement", "", "")
	if v["projected_date"] != cash {
		t.Fatalf("preview changed date %v", v)
	}
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/income", `{"version":1,"options":[]}`, "clear")
	if status != 200 {
		t.Fatalf("clear %d %v", status, v)
	}
	_, v = requestAPI(t, h, c, "", "GET", "/api/retirement", "", "")
	if v["projected_years"] != nil {
		t.Fatalf("removed grant still contributes %v", v)
	}
}

// Catches withholding a retirement date because of a currency needed only after
// the target has already been reached by known savings.
// TestRetirementIgnoresMissingCurrencyAfterTargetDate 验证达标日期之后才需要的缺失汇率不会阻止提前达标预测。
func TestRetirementIgnoresMissingCurrencyAfterTargetDate(t *testing.T) {
	_, h, c := apiFixture(t)
	today := time.Now().In(time.FixedZone("CST", 8*3600))
	nextMonthEnd := time.Date(today.Year(), today.Month()+1, 0, 0, 0, 0, 0, today.Location())
	if today.Day() == nextMonthEnd.Day() {
		nextMonthEnd = time.Date(today.Year(), today.Month()+2, 0, 0, 0, 0, 0, today.Location())
	}
	future := today.AddDate(1, 0, 0).Format("2006-01-02")
	status, v := requestAPI(t, h, c, "", "POST", "/api/retirement", `{"name":"目标","amount":50000}`, "target")
	if status != 201 {
		t.Fatalf("target %d %v", status, v)
	}
	payload := fmt.Sprintf(`{"version":0,"monthlySavings":50000,"options":[{"name":"Later","currency":"USD","quantity":100,"strikePrice":0,"marketPrice":10,"taxRate":0,"batches":[{"vestDate":"%s","quantity":100,"cashMode":"immediate"}]}]}`, future)
	status, v = requestAPI(t, h, c, "", "PATCH", "/api/income", payload, "income")
	if status != 200 {
		t.Fatalf("save %d %v", status, v)
	}
	_, v = requestAPI(t, h, c, "", "GET", "/api/retirement", "", "")
	if v["projected_date"] != nextMonthEnd.Format("2006-01-02") {
		t.Fatalf("later currency blocked date: %v", v)
	}
}
