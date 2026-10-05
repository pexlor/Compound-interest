// 可选真实行情验证：只使用公开基准、临时数据库及标准本金，不读取用户账户。
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"fulibu-go/internal/service"
)

// TestForecastPublicBenchmarkLive 对公开沪深300ETF缓存执行真实1/3/5年回测，常规测试不访问外网。
func TestForecastPublicBenchmarkLive(t *testing.T) {
	if os.Getenv("FULIBU_FORECAST_LIVE") != "1" {
		t.Skip("需显式启用公开行情验证")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	at := time.Now().UTC()
	from := at.AddDate(-10, 0, -45)
	series, err := fetchMarketSeries(ctx, &http.Client{Timeout: 12 * time.Second}, "stock", "SH510300", from)
	if err != nil {
		t.Fatal(err)
	}
	history := []service.PriceObservation{}
	for _, r := range series.Rows {
		history = append(history, service.PriceObservation{Date: r.Date, Price: r.Price, TotalPrice: r.ReturnPrice})
	}
	in := service.ForecastInput{Context: ctx, AsOf: at, Rates: map[string]float64{"CNY": 1}, History: map[string][]service.PriceObservation{"cn_equity": history, "own": history}, Assets: []service.ForecastHolding{{ID: 1, Name: "公开沪深300ETF标准本金", Category: "stock", Currency: "CNY", Amount: 1000000, Class: "cn_equity", Key: "own"}}}
	reports := []service.BacktestReport{}
	for _, years := range []int{1, 3, 5} {
		o := service.DefaultForecastOptions()
		o.Years = years
		report, e := service.BacktestForecast(in, o)
		if e != nil {
			t.Fatal(e)
		}
		reports = append(reports, report)
	}
	raw, _ := json.MarshalIndent(reports, "", "  ")
	if path := os.Getenv("FULIBU_FORECAST_REPORT"); path != "" {
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("公开日线%d条，回测窗口：%d/%d/%d", len(history), len(reports[0].Cases), len(reports[1].Cases), len(reports[2].Cases))
}

// TestForecastHKBenchmarkLive 验证公开港股基准能够补齐本币复权历史，避免含分红元数据的腾讯记录导致不可用。
func TestForecastHKBenchmarkLive(t *testing.T) {
	if os.Getenv("FULIBU_FORECAST_LIVE") != "1" {
		t.Skip("需显式启用公开行情验证")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	at := time.Now().UTC()
	s, err := fetchMarketSeries(ctx, &http.Client{Timeout: 12 * time.Second}, "stock", "02800.HK", at.AddDate(-10, 0, -45))
	if err != nil {
		t.Fatal(err)
	}
	rows := []service.PriceObservation{}
	for _, r := range s.Rows {
		rows = append(rows, service.PriceObservation{Date: r.Date, Price: r.Price, TotalPrice: r.ReturnPrice})
	}
	monthly, err := service.MonthlyReturns(rows, at)
	if err != nil || s.Currency != "HKD" || len(monthly) < 60 {
		t.Fatalf("HK sample %d, currency %s, error %v", len(monthly), s.Currency, err)
	}
	t.Logf("港股本币日线%d条，完整月收益%d个月", len(rows), len(monthly))
}
