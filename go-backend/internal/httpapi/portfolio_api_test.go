// 投资组合接口测试：验证汇总、历史、估值部分失败、预览与请求重放。

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"fulibu-go/internal/service"
)

// localQuoteTransport 通过函数实现测试 HTTP 传输层，将行情请求重定向到本地服务。
type localQuoteTransport func(*http.Request) (*http.Response, error)

// RoundTrip 把测试 HTTP 传输请求转交给注入的函数，以替换外部行情请求。
func (f localQuoteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestPortfolioAPISummaryAndHistory 验证投资组合汇总的分组、金额、缺失汇率和资产历史查询。
func TestPortfolioAPISummaryAndHistory(t *testing.T) {
	db, h, c := apiFixture(t)
	tok := issueToken(t, h, c, "read")
	db.Exec(`INSERT INTO assets(user_id,name,category,amount,currency) VALUES(1,'cash','deposit',10000,'CNY'),(1,'usd','money',20000,'USD')`)
	db.Exec(`INSERT INTO exchange_rates(currency,cny_rate,rate_date,updated_at) VALUES('USD',7,'2026-10-03',CURRENT_TIMESTAMP)`)
	status, v := requestAPI(t, h, nil, tok, "GET", "/api/portfolio/summary", "", "")
	if status != 200 || v["totalCnyMinor"] != float64(150000) || v["complete"] != true {
		t.Fatalf("summary %d %v", status, v)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM asset_history`).Scan(&count)
	if count != 0 {
		t.Fatal("summary wrote history")
	}
	db.Exec(`DELETE FROM exchange_rates`)
	status, v = requestAPI(t, h, nil, tok, "GET", "/api/portfolio/summary", "", "")
	if status != 200 || v["totalCnyMinor"] != nil || v["complete"] != false {
		t.Fatalf("missing rates %d %v", status, v)
	}
	for _, date := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
		db.Exec(`INSERT INTO asset_history(user_id,snapshot_date,total_cny,trigger) VALUES(1,?,100,'test')`, date)
	}
	status, v = requestAPI(t, h, nil, tok, "GET", "/api/history?from=2026-01-02&to=2026-01-03&limit=1", "", "")
	if status != 200 || len(v["history"].([]any)) != 1 || v["history"].([]any)[0].(map[string]any)["snapshot_date"] != "2026-01-03" {
		t.Fatalf("history %d %v", status, v)
	}
	for _, query := range []string{"from=2026-02-30", "from=2026-01-03&to=2026-01-01", "limit=-1", "limit=x", "to=x"} {
		status, _ = requestAPI(t, h, nil, tok, "GET", "/api/history?"+query, "", "")
		if status != 400 {
			t.Fatalf("invalid history %s = %d", query, status)
		}
	}
}

// TestPortfolioAPIValuationPartialFailurePreviewAndReplay 验证估值刷新处理部分失败、预览回滚、幂等重放及归档持仓过滤。
func TestPortfolioAPIValuationPartialFailurePreviewAndReplay(t *testing.T) {
	db, _, c := apiFixture(t)
	db.Exec(`INSERT INTO assets(id,user_id,name,category,code,amount,quantity,currency) VALUES(1,1,'ok','stock','AAPL',10000,2,'USD'),(2,1,'bad','stock','MSFT',10000,3,'USD'),(3,1,'archived','stock','TSLA',10000,1,'USD')`)
	db.Exec(`UPDATE assets SET archived_at='old' WHERE id=3`)
	db.Exec(`INSERT INTO exchange_rates(currency,cny_rate,rate_date) VALUES('USD',7,'2026-01-01')`)
	a := &app{db: db, ledger: service.NewLedger(db)}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc( /* 提供本地模拟行情响应，并检查已归档证券不会被请求。 */ func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, "TSLA") {
			t.Error("queried archived security")
		}
		if strings.Contains(r.URL.Path, "MSFT") {
			w.WriteHeader(502)
			return
		}
		fields := make([]string, 31)
		fields[3] = "150"
		fields[30] = "20261003160000"
		_, _ = w.Write([]byte(`v_usAAPL="` + strings.Join(fields, "~") + `";`))
	}))
	defer provider.Close()
	target, _ := url.Parse(provider.URL)
	quoteClient := &http.Client{Transport: localQuoteTransport( /* 将外部行情请求地址改写为本地测试服务地址。 */ func(r *http.Request) (*http.Response, error) {
		local := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		local.URL = &u
		local.Host = target.Host
		return http.DefaultTransport.RoundTrip(local)
	})}
	a.quote = /* 通过本地模拟客户端调用真实行情解析逻辑。 */ func(_ *http.Client, category, code string) (float64, string, string, error) {
		return fetchLiveQuote(quoteClient, category, code)
	}
	h := a.routes()
	tok := issueToken(t, h, c, "write")
	status, v := requestAPI(t, h, nil, tok, "POST", "/api/valuations/refresh?dryRun=true", `{}`, "")
	if status != 200 || len(v["updated"].([]any)) != 1 || len(v["errors"].([]any)) != 1 || v["snapshotRecorded"] != false {
		t.Fatalf("preview %d %v", status, v)
	}
	var amount, history int
	db.QueryRow(`SELECT amount FROM assets WHERE id=1`).Scan(&amount)
	db.QueryRow(`SELECT COUNT(*) FROM asset_history`).Scan(&history)
	if amount != 10000 || history != 0 {
		t.Fatal("preview wrote state")
	}
	status, v = requestAPI(t, h, nil, tok, "POST", "/api/valuations/refresh", `{}`, "refresh")
	if status != 200 || v["snapshotRecorded"] != true || v["complete"] != false {
		t.Fatalf("refresh %d %v", status, v)
	}
	db.QueryRow(`SELECT amount FROM assets WHERE id=1`).Scan(&amount)
	if amount != 30000 {
		t.Fatalf("wrong valuation: %d", amount)
	}
	beforeCalls := calls.Load()
	status, _ = requestAPI(t, h, nil, tok, "POST", "/api/valuations/refresh", `{}`, "refresh")
	if status != 200 || calls.Load() != beforeCalls {
		t.Fatalf("replay repeated quote: %d %d", status, calls.Load())
	}
	read := issueToken(t, h, c, "read")
	status, _ = requestAPI(t, h, nil, read, "POST", "/api/valuations/refresh", `{}`, "denied")
	if status != 403 {
		t.Fatalf("read refresh: %d", status)
	}
}
