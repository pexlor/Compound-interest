// 预测 HTTP 接口：校验情景、返回同一引擎的资产范围与退休概率，并异步补齐共享数据。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fulibu-go/internal/service"
)

var forecastSlots = make(chan struct{}, 2)

// forecastOptions 严格读取有界情景参数，拒绝未知基准与非法数字。
func forecastOptions(r *http.Request) (service.ForecastOptions, error) {
	o := service.DefaultForecastOptions()
	q := r.URL.Query()
	if q.Has("years") {
		n, e := strconv.Atoi(q.Get("years"))
		if e != nil || n < 1 || n > 30 {
			return o, fmt.Errorf("预测年限必须为1至30年")
		}
		o.Years = n
	}
	if q.Has("inflation") {
		n, e := strconv.ParseFloat(q.Get("inflation"), 64)
		if e != nil || n < 0 || n > 20 || !finitePositive(n+1) {
			return o, fmt.Errorf("通胀必须为0至20%%")
		}
		o.Inflation = n
	}
	if q.Has("includeRestricted") {
		v := q.Get("includeRestricted")
		if v != "true" && v != "false" {
			return o, fmt.Errorf("公积金选项必须为true或false")
		}
		o.IncludeRestricted = v == "true"
	}
	if q.Has("benchmarks") {
		raw := q.Get("benchmarks")
		if len(raw) > 10000 || json.Unmarshal([]byte(raw), &o.Benchmarks) != nil || strings.TrimSpace(raw) == "null" {
			return o, fmt.Errorf("基准选择格式无效")
		}
		for id, class := range o.Benchmarks {
			if _, ok := service.ForecastBenchmarks()[class]; !ok || id < 1 {
				return o, fmt.Errorf("预测基准无效")
			}
		}
	}
	return o, nil
}

// forecast 以只读快照运行有界模拟，匿名访问和超额并发不会消耗预测计算资源。
func (a *app) forecast(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodGet {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	o, err := forecastOptions(r)
	if err != nil {
		apiError(w, 400, "invalid_request", err.Error())
		return
	}
	if r.URL.Query().Has("backtest") && r.URL.Query().Get("backtest") != "1" {
		apiError(w, 400, "invalid_request", "回测选项必须为1")
		return
	}
	select {
	case forecastSlots <- struct{}{}:
		defer func() { <-forecastSlots }()
	default:
		apiError(w, 503, "forecast_busy", "预测正在计算，请稍后重试")
		return
	}
	now := time.Now().In(shanghai)
	at := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	result, securities, in, err := a.ledger.Forecast(r.Context(), u.ID, o, at)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	c := marketCacheFor(a.db)
	for _, s := range securities {
		_, _ = c.get(s.Category, s.Code, 3650, false)
	}
	currencies := map[string]bool{}
	for _, asset := range in.Assets {
		if asset.Currency != "CNY" {
			currencies[asset.Currency] = true
		}
	}
	for _, event := range in.Events {
		if event.Currency != "CNY" {
			currencies[event.Currency] = true
		}
	}
	for currency := range currencies {
		c.warmForecastFX(currency, at)
	}
	if r.URL.Query().Get("backtest") == "1" {
		reports := []service.BacktestReport{}
		for _, years := range []int{1, 3, 5} {
			options := o
			options.Years = years
			report, e := service.BacktestForecast(in, options)
			if e != nil {
				writeAPIError(w, e)
				return
			}
			reports = append(reports, report)
		}
		out(w, 200, map[string]any{"forecast": result, "backtests": reports})
		return
	}
	out(w, 200, result)
}

// warmForecastFX 合并同币种的后台补齐，失败冷却五分钟并复用行情并发限制。
func (c *marketCache) warmForecastFX(currency string, at time.Time) {
	key := "fx:" + currency
	c.mu.Lock()
	if c.flights[key] != nil {
		c.mu.Unlock()
		return
	}
	var checked, failure string
	c.db.QueryRow(`SELECT checked_at,last_error FROM market_sync_state WHERE category='fx' AND code=?`, currency).Scan(&checked, &failure)
	last, _ := time.Parse(time.RFC3339Nano, checked)
	if failure != "" && time.Since(last) < 5*time.Minute {
		c.mu.Unlock()
		return
	}
	done := make(chan struct{})
	c.flights[key] = done
	parent := c.ctx
	c.mu.Unlock()
	go func() {
		defer func() { c.mu.Lock(); delete(c.flights, key); close(done); c.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		select {
		case c.slots <- struct{}{}:
			defer func() { <-c.slots }()
		case <-ctx.Done():
			return
		}
		err := c.ensureFX(ctx, currency, at.AddDate(-10, 0, -10).Format("2006-01-02"), at.Format("2006-01-02"), false)
		if err != nil {
			c.db.Exec(`INSERT INTO market_sync_state(category,code,checked_at,last_error) VALUES('fx',?,?,?) ON CONFLICT(category,code) DO UPDATE SET checked_at=excluded.checked_at,last_error=excluded.last_error`, currency, cacheTime(), err.Error())
		}
	}()
}
