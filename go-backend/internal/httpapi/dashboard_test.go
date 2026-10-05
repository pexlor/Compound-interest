package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fulibu-go/internal/service"
)

// 首页必须直接显示本地持仓；即使外部行情一直没有响应也不能阻塞打开账本。
func TestDashboardReturnsStoredAssetsWithoutWaitingForQuotes(t *testing.T) {
	db, _, cookie := apiFixture(t)
	if _, err := db.Exec(`INSERT INTO assets(user_id,name,category,code,amount,quantity,currency) VALUES(1,'Apple','stock','AAPL',20000,2,'USD')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO exchange_rates(currency,cny_rate,rate_date) VALUES('USD',7,'2026-10-04')`); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	done := make(chan struct{})
	a := &app{db: db, ledger: service.NewLedger(db), quote: func(_ *http.Client, _, _ string) (float64, string, string, error) {
		<-release
		return 150, "USD", "2026-10-04", nil
	}}
	r := httptest.NewRequest("GET", "/api/dashboard", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	defer func() { close(release); <-done }()
	go func() { a.routes().ServeHTTP(w, r); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("首页等待外部行情，无法返回本地账本")
	}
	var v struct {
		Assets []service.Asset `json:"assets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(v.Assets) != 1 || v.Assets[0].Amount != 20000 {
		t.Fatalf("首页未返回原始本地持仓: status=%d body=%s", w.Code, w.Body.String())
	}
	var amount, history int
	if err := db.QueryRow(`SELECT amount FROM assets WHERE user_id=1`).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_history WHERE user_id=1`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if amount != 20000 || history != 0 {
		t.Fatalf("打开首页意外改写本地持仓或历史: amount=%d history=%d", amount, history)
	}
}
