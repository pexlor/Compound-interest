package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

var supportedCurrencies = []string{"USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"}

type frankfurterRate struct {
	Date, Base, Quote string
	Rate              float64
}

// refreshRates 从汇率服务拉取汇率并更新本地缓存。
func (a *app) refreshRates() (map[string]float64, string, error) {
	endpoint := "https://api.frankfurter.dev/v2/rates?" + url.Values{
		"base": {"USD"}, "quotes": {"CNY,HKD,EUR,JPY,GBP,SGD,AUD,CAD,CHF"},
	}.Encode()
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("汇率服务不可用: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("汇率服务返回 HTTP %d", response.StatusCode)
	}
	var rows []frankfurterRate
	if err := json.NewDecoder(response.Body).Decode(&rows); err != nil {
		return nil, "", fmt.Errorf("汇率响应无效: %w", err)
	}
	quotes := map[string]float64{}
	dates := []string{}
	for _, row := range rows {
		if row.Base == "USD" && row.Rate > 0 {
			quotes[row.Quote] = row.Rate
			dates = append(dates, row.Date)
		}
	}
	usdCny := quotes["CNY"]
	if usdCny <= 0 {
		return nil, "", fmt.Errorf("未取得 USD/CNY 汇率")
	}
	rates := map[string]float64{"CNY": 1, "USD": usdCny}
	for _, currency := range supportedCurrencies {
		if currency == "USD" {
			continue
		}
		quote := quotes[currency]
		if quote <= 0 {
			return nil, "", fmt.Errorf("未取得 USD/%s 汇率", currency)
		}
		rates[currency] = usdCny / quote
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return nil, "", fmt.Errorf("汇率日期缺失")
	}
	date := dates[len(dates)-1]
	tx, err := a.db.Begin()
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	for currency, rate := range rates {
		if currency == "CNY" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO exchange_rates(currency,cny_rate,rate_date,updated_at) VALUES(?,?,?,CURRENT_TIMESTAMP) ON CONFLICT(currency) DO UPDATE SET cny_rate=excluded.cny_rate,rate_date=excluded.rate_date,updated_at=CURRENT_TIMESTAMP`, currency, rate, date); err != nil {
			return nil, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return rates, date, nil
}

// rates 返回汇率缓存，并在需要时主动刷新。
func (a *app) rates(w http.ResponseWriter, r *http.Request) {
	if a.need(w, r) == nil {
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "方法不允许")
		return
	}
	rates, date, err := a.ledger.LatestRates()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	fresh, err := a.rateCacheFresh(time.Now())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	stale := !fresh
	if stale || r.URL.Query().Get("refresh") == "1" {
		if refreshed, latest, refreshErr := a.refreshRates(); refreshErr == nil {
			rates, date, stale = refreshed, latest, false
		}
	}
	out(w, 200, map[string]any{"rates": rates, "date": date, "stale": stale, "cached": !stale})
}

var shanghai = time.FixedZone("CST", 8*3600)

// rateCacheFresh reports whether every supported foreign-currency rate was
// refreshed after today's 09:15 in Shanghai.  Thus API callers normally use
// the scheduled 09:16 cache, while an unavailable scheduler can self-heal.
func (a *app) rateCacheFresh(now time.Time) (bool, error) {
	local := now.In(shanghai)
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 9, 15, 0, 0, shanghai)
	return a.ledger.RatesCachedSince(cutoff, len(supportedCurrencies))
}
