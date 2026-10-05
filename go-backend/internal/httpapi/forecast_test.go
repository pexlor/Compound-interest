// 预测接口测试：覆盖认证、用户隔离、缺数据、输入校验和退休同一口径。
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestForecastAPIUsesSameRetirementEngine 验证存款预测与退休接口使用同一流动资产和通胀情景。
func TestForecastAPIUsesSameRetirementEngine(t *testing.T) {
	db, h, c := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,amount,currency,annual_rate) VALUES(1,'deposit','deposit',1000000,'CNY',10),(2,'private','deposit',900000000,'CNY',50)`)
	db.Exec(`INSERT INTO retirement_goal_items(user_id,name,category,amount,currency) VALUES(1,'goal','fixed',1100000,'CNY')`)
	status, v := requestAPI(t, h, c, "", "GET", "/api/forecast?years=10", "", "")
	if status != 200 || v["state"] != "ready" {
		t.Fatalf("forecast %d %v", status, v)
	}
	series := v["series"].([]any)
	if series[0].(map[string]any)["p50"] != float64(1000000) {
		t.Fatal("cross-user balance leaked")
	}
	r := v["retirement"].(map[string]any)
	status, ret := requestAPI(t, h, c, "", "GET", "/api/retirement", "", "")
	if status != 200 || r["projected_date"] != ret["projected_date"] || r["annual_rate"] != ret["annual_rate"] {
		t.Fatalf("different engines %v %v", r, ret)
	}
}

// TestForecastAPIRejectsInvalidScenarios 验证非法参数及未登录请求不会启动模拟。
func TestForecastAPIRejectsInvalidScenarios(t *testing.T) {
	_, h, c := apiFixture(t)
	for _, path := range []string{"/api/forecast?years=0", "/api/forecast?years=31", "/api/forecast?inflation=NaN", "/api/forecast?includeRestricted=yes", "/api/forecast?benchmarks=bad", "/api/forecast?benchmarks=%7B%221%22%3A%22invalid%22%7D"} {
		status, v := requestAPI(t, h, c, "", "GET", path, "", "")
		if status != 400 {
			t.Fatalf("%s: %d %v", path, status, v)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/forecast", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("anonymous status %d", w.Code)
	}
}

// TestForecastAPIMissingGoalFXBlocksProjection 验证缺少目标外币汇率时不返回偏小目标的达标预测。
func TestForecastAPIMissingGoalFXBlocksProjection(t *testing.T) {
	db, h, c := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,amount,currency) VALUES(1,'cash','deposit',1000000,'CNY')`)
	db.Exec(`INSERT INTO retirement_goal_items(user_id,name,category,amount,currency) VALUES(1,'goal','fixed',100000,'USD')`)
	status, v := requestAPI(t, h, c, "", "GET", "/api/forecast", "", "")
	if status != 200 || v["state"] != "unavailable" || len(v["missing"].([]any)) == 0 {
		t.Fatalf("%d %v", status, v)
	}
}

// TestForecastAPIRequiresExplicitFundBenchmark 验证没有基金类型时不把债券基金静默当股票预测。
func TestForecastAPIRequiresExplicitFundBenchmark(t *testing.T) {
	db, h, c := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,code,amount,currency) VALUES(1,'bond fund','fund','021000',1000000,'CNY')`)
	status, v := requestAPI(t, h, c, "", "GET", "/api/forecast", "", "")
	if status != 200 || v["state"] != "unavailable" {
		t.Fatalf("%d %v", status, v)
	}
}

// TestForecastAPICompleteLocalHistory 验证完整本地日线可直接生成范围，基准没有持仓也可复用缓存。
func TestForecastAPICompleteLocalHistory(t *testing.T) {
	db, h, cookie := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,code,amount,quantity,currency) VALUES(1,'ETF','fund','510300',1000000,1000,'CNY')`)
	rows := serviceMonthlyFixture()
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO market_daily_prices(category,code,price_date,price,return_price,currency,source,fetched_at) VALUES('stock','SH510300',?,?,?,'CNY','test',?)`, row.date, row.price, row.price, cacheTime()); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`INSERT INTO market_sync_state(category,code,covered_from,checked_at,input_version,observation_count) VALUES('stock','SH510300',?,?,1,?)`, rows[0].date, cacheTime(), len(rows))
	db.Exec(`INSERT INTO market_quotes(category,code,price,currency,price_date,source,fetched_at) VALUES('stock','SH510300',1,'CNY',?,'test',?)`, marketDate(), cacheTime())
	status, v := requestAPI(t, h, cookie, "", "GET", "/api/forecast?years=3&benchmarks=%7B%221%22%3A%22cn_equity%22%7D", "", "")
	if status != 200 || v["state"] != "ready" || v["paths"] != float64(5000) {
		t.Fatalf("%d %v", status, v)
	}
	p := v["series"].([]any)[3].(map[string]any)
	if p["p10"].(float64) > p["p50"].(float64) || p["p90"].(float64) < p["p50"].(float64) {
		t.Fatal("invalid range")
	}
}

// forecastFixturePoint 描述测试生成的完整月末价格。
type forecastFixturePoint struct {
	date  string
	price float64
}

// serviceMonthlyFixture 生成不依赖网络的十年历史，最后一个月为上个完整月。
func serviceMonthlyFixture() []forecastFixturePoint {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month()-121, 1, 0, 0, 0, 0, time.UTC)
	rows := []forecastFixturePoint{}
	price := 1.0
	for i := 0; i <= 120; i++ {
		date := start.AddDate(0, i+1, -1)
		price *= 1.005
		rows = append(rows, forecastFixturePoint{date.Format("2006-01-02"), price})
	}
	return rows
}
