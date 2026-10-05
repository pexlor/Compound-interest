package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// exportRequest 通过实际路由发送导出请求，支持会话或只读令牌认证。
func exportRequest(h http.Handler, cookie *http.Cookie, token, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestExportCSVIsolationAndUnits 验证只读令牌导出、用户隔离、金额单位和 CSV 公式保护。
func TestExportCSVIsolationAndUnits(t *testing.T) {
	db, h, c := apiFixture(t)
	for _, q := range []string{
		`INSERT INTO assets(id,user_id,name,category,code,amount,quantity,currency,note) VALUES(1,1,'=danger,"quoted"','stock','USQQQ',12345,2,'USD','line1'||char(10)||'line2')`,
		`INSERT INTO assets(id,user_id,name,category,code,amount,currency) VALUES(2,2,'private','stock','MSFT',999,'USD')`,
		`INSERT INTO assets(id,user_id,name,category,amount,currency,archived_at) VALUES(3,1,'archived','deposit',100,'CNY','2026-10-01')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tok := issueToken(t, h, c, "read")
	w := exportRequest(h, nil, tok, "GET", "/api/export?datasets=assets")
	if w.Code != 200 {
		t.Fatalf("export status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/csv") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Header())
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !strings.Contains(w.Body.String(), "123.45") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "archived") {
		t.Fatal(records)
	}
	if records[1][1] != "'=danger,\"quoted\"" || !strings.Contains(w.Body.String(), "line1\nline2") {
		t.Fatal(records)
	}
	w = exportRequest(h, c, "", "GET", "/api/export?datasets=assets&includeArchived=true")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "archived") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestExportZipScopeAndDateRange 验证多文件打包、证券去重、真实日期过滤和个人快照隔离。
func TestExportZipScopeAndDateRange(t *testing.T) {
	db, h, c := apiFixture(t)
	for _, q := range []string{
		`INSERT INTO assets(id,user_id,name,category,code,amount,currency) VALUES(1,1,'QQQ','stock','USQQQ',100,'USD'),(2,1,'duplicate','fund','QQQ',100,'USD'),(3,2,'other','stock','MSFT',100,'USD')`,
		`INSERT INTO market_daily_prices(category,code,price_date,price,return_price,currency,source,fetched_at) VALUES('stock','QQQ','2026-10-01',100,99,'USD','test','now'),('stock','QQQ','2026-10-02',101,100,'USD','test','now'),('stock','MSFT','2026-10-02',200,199,'USD','test','now')`,
		`INSERT INTO asset_daily_snapshots(user_id,asset_id,snapshot_date,amount,quantity,currency,annual_rate,price_date,source,fetched_at) VALUES(1,1,'2026-10-02',12345,1,'USD',2,'2026-10-02','test','now'),(2,3,'2026-10-02',88888,1,'USD',2,'2026-10-02','test','now')`,
		`INSERT INTO market_returns(category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,start_date,end_date,source) VALUES('stock','QQQ',365,'2026-10-02',2,2,365,365,'2025-10-02','2026-10-02','test')`,
		`INSERT INTO exchange_rate_history(currency,cny_rate,rate_date,source,fetched_at) VALUES('USD',7,'2026-10-02','test','now'),('EUR',8,'2026-10-02','test','now')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	w := exportRequest(h, c, "", "GET", "/api/export?datasets=assets,prices,returns,snapshots,rates&from=2026-10-02&to=2026-10-02")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		files[f.Name] = string(b)
	}
	if len(files) != 6 {
		t.Fatal(files)
	}
	price := files["prices.csv"]
	records, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(price, "\ufeff"))).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	if len(records) != 2 || strings.Contains(price, "MSFT") || strings.Contains(price, "2026-10-01") {
		t.Fatal(price)
	}
	if strings.Contains(files["snapshots.csv"], "888.88") || !strings.Contains(files["snapshots.csv"], "123.45") {
		t.Fatal(files["snapshots.csv"])
	}
	if strings.Contains(files["rates.csv"], "EUR") || !strings.Contains(files["rates.csv"], "USD") {
		t.Fatal(files["rates.csv"])
	}
	if !strings.Contains(files["returns.csv"], "QQQ") {
		t.Fatal(files["returns.csv"])
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM market_daily_prices`).Scan(&n)
	if n != 3 {
		t.Fatal("export mutated cache")
	}
}

// TestExportRejectsInvalidRequests 验证未登录、非法范围和他人资产选择均不能下载数据。
func TestExportRejectsInvalidRequests(t *testing.T) {
	db, h, c := apiFixture(t)
	if _, e := db.Exec(`INSERT INTO assets(id,user_id,name,category,amount,currency) VALUES(1,2,'other','deposit',100,'CNY')`); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path, method string
		cookie       *http.Cookie
		status       int
	}{
		{"/api/export?datasets=assets", "GET", nil, 401},
		{"/api/export?datasets=assets", "POST", c, 405},
		{"/api/export?datasets=secret", "GET", c, 400},
		{"/api/export?datasets=assets&from=2026-02-30", "GET", c, 400},
		{"/api/export?datasets=assets&from=2026-10-03&to=2026-10-01", "GET", c, 400},
		{"/api/export?datasets=assets,prices&format=csv", "GET", c, 400},
		{"/api/export?datasets=assets&assetIds=1", "GET", c, 404},
		{"/api/export?datasets=assets&assetIds=bad", "GET", c, 400},
	} {
		w := exportRequest(h, tc.cookie, "", tc.method, tc.path)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, w.Code, tc.status)
		}
	}
}

// TestExportOwnedSubsetAndEmptyHistory 验证指定本人资产会排除其他本人持仓，空历史仍提供完整表头。
func TestExportOwnedSubsetAndEmptyHistory(t *testing.T) {
	db, h, c := apiFixture(t)
	for _, q := range []string{
		`INSERT INTO assets(id,user_id,name,category,code,amount,currency) VALUES(1,1,'selected','stock','QQQ',100,'USD'),(2,1,'excluded','stock','MSFT',200,'USD')`,
		`INSERT INTO market_daily_prices(category,code,price_date,price,return_price,currency,source,fetched_at) VALUES('stock','QQQ','2026-10-02',100,99,'USD','test','now'),('stock','MSFT','2026-10-02',200,199,'USD','test','now')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"assets", "prices"} {
		w := exportRequest(h, c, "", "GET", "/api/export?datasets="+name+"&assetIds=1")
		if w.Code != 200 || strings.Contains(w.Body.String(), "excluded") || strings.Contains(w.Body.String(), "MSFT") {
			t.Fatal(w.Code, w.Body.String())
		}
		rows, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
		if e != nil || len(rows) != 2 {
			t.Fatal(rows, e)
		}
	}
	w := exportRequest(h, c, "", "GET", "/api/export?datasets=prices&from=2025-01-01&to=2025-01-02")
	rows, e := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if w.Code != 200 || e != nil || len(rows) != 1 || len(rows[0]) != 10 {
		t.Fatal(w.Code, rows, e)
	}
}

// TestExportCellPrecisionAndFormulaSafety 验证整数金额极值精度及多种公式前缀保护。
func TestExportCellPrecisionAndFormulaSafety(t *testing.T) {
	for n, want := range map[int64]string{-1: "-0.01", -12345: "-123.45", -9223372036854775808: "-92233720368547758.08", 9223372036854775807: "92233720368547758.07"} {
		if got := minorExportAmount(n); got != want {
			t.Fatalf("%d => %s want %s", n, got, want)
		}
	}
	for _, s := range []string{"=1+1", "+1", "-command", "@SUM(A1:A2)", "  =formula", "\tplain", "\n=1"} {
		if got := exportCell(s, false); got != "'"+s {
			t.Fatal(got)
		}
	}
	if got := exportCell(float64(-2.3), false); got != "-2.3" {
		t.Fatal(got)
	}
}
