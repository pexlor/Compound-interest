// 预测引擎测试：验证逐项收益、现金流、联合波动、通胀与流动资产口径。
package service

import (
	"math"
	"reflect"
	"testing"
)

// TestForecastCompoundsAssetsSeparately 验证五年逐项复利不会被加权平均复利替代。
func TestForecastCompoundsAssetsSeparately(t *testing.T) {
	in := ForecastInput{AsOf: day("2026-01-01"), Rates: map[string]float64{"CNY": 1}, Assets: []ForecastHolding{{ID: 1, Category: "deposit", Currency: "CNY", Amount: 50000000, AnnualRate: 20}, {ID: 2, Category: "deposit", Currency: "CNY", Amount: 50000000}}}
	out, err := SimulateForecast(in, ForecastOptions{Years: 5, Paths: 20})
	if err != nil {
		t.Fatal(err)
	}
	end := out.Series[5]
	if math.Abs(float64(end.P50)-174416000) > 1 {
		t.Fatalf("%+v", end)
	}
}

// TestForecastDatedPrincipalInflationAndLiquidity 验证现金不计息、日期边界、通胀及固定资产排除。
func TestForecastDatedPrincipalInflationAndLiquidity(t *testing.T) {
	in := ForecastInput{AsOf: day("2026-01-01"), Rates: map[string]float64{"CNY": 1}, Target: 1200000, Assets: []ForecastHolding{{ID: 1, Category: "fixed", Currency: "CNY", Amount: 1000000, AnnualRate: 50}}, MonthlySavings: 10000, Events: []Cashflow{{Date: "2026-01-01", Amount: 999000, Currency: "CNY"}, {Date: "2026-02-15", Amount: 100000, Currency: "CNY"}, {Date: "2027-01-02", Amount: 999000, Currency: "CNY"}}}
	out, err := SimulateForecast(in, ForecastOptions{Years: 1, Inflation: 10, Paths: 10})
	if err != nil {
		t.Fatal(err)
	}
	p := out.Series[1]
	if p.P50 != 1220000 || p.LiquidP50 != 220000 || p.Contributions != 220000 || p.Probability != 0 || out.RetirementDate != "" {
		t.Fatalf("%+v", out)
	}
	if out.ThisMonth.Contributions != 10000 || out.ThisMonth.InvestmentGain != 0 {
		t.Fatalf("month %+v", out.ThisMonth)
	}
	if p.RealP50 >= p.P50 || p.Target <= 1200000 {
		t.Fatal("inflation ignored")
	}
}

// monthlyFixture 构造十年末日价格，交替上涨下跌用于检验联合抽样。
func monthlyFixture(rate float64) []PriceObservation {
	out := []PriceObservation{}
	price := 100.0
	for i := 0; i <= 120; i++ {
		date := day("2015-01-01").AddDate(0, i+1, -1)
		if i > 0 {
			v := rate
			if i%2 == 0 {
				v = -rate
			}
			price *= math.Exp(v)
		}
		out = append(out, PriceObservation{Date: date.Format("2006-01-02"), Price: price, TotalPrice: price})
	}
	return out
}

// TestForecastJointPathsAndReproducibility 验证同收益资产比例保持固定，随机结果可复现。
func TestForecastJointPathsAndReproducibility(t *testing.T) {
	h := monthlyFixture(.05)
	in := ForecastInput{AsOf: day("2025-02-01"), Rates: map[string]float64{"CNY": 1}, History: map[string][]PriceObservation{"cn_equity": h, "own": h}, Assets: []ForecastHolding{{ID: 1, Category: "stock", Currency: "CNY", Amount: 100000, Class: "cn_equity", Key: "own"}, {ID: 2, Category: "stock", Currency: "CNY", Amount: 200000, Class: "cn_equity", Key: "own"}}}
	a, err := SimulateForecast(in, ForecastOptions{Years: 1, Paths: 100})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := SimulateForecast(in, ForecastOptions{Years: 1, Paths: 100})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("nonrepeatable forecast")
	}
	if a.Series[1].P10 >= a.Series[1].P90 {
		t.Fatal("volatility lost")
	}
	if math.Abs(float64(a.Assets[1].Forecast)/float64(a.Assets[0].Forecast)-2) > .0001 {
		t.Fatal("independent asset draws")
	}
	in.History["cn_equity"] = h[:10]
	if _, err = SimulateForecast(in, ForecastOptions{Years: 1, Paths: 100}); err == nil {
		t.Fatal("short benchmark accepted")
	}
}

// TestForecastForeignPrincipalNeedsRates 验证区间内外币到账缺失汇率不可默认为零。
func TestForecastForeignPrincipalNeedsRates(t *testing.T) {
	in := ForecastInput{AsOf: day("2026-01-01"), Rates: map[string]float64{"CNY": 1}, Events: []Cashflow{{Date: "2026-04-01", Amount: 10000, Currency: "USD"}}}
	if _, err := SimulateForecast(in, ForecastOptions{Years: 1, Paths: 10}); err == nil {
		t.Fatal("missing FX accepted")
	}
	in.Events[0].Date = "2028-01-01"
	if _, err := SimulateForecast(in, ForecastOptions{Years: 1, Paths: 10}); err != nil {
		t.Fatal(err)
	}
}

// TestForecastHorizonDoesNotChangeEarlierYears 验证切换预测期限不会改变相同年份的模拟结果。
func TestForecastHorizonDoesNotChangeEarlierYears(t *testing.T) {
	h := monthlyFixture(.05)
	in := ForecastInput{AsOf: day("2025-02-01"), Rates: map[string]float64{"CNY": 1}, History: map[string][]PriceObservation{"cn_equity": h, "own": h}, Assets: []ForecastHolding{{ID: 1, Category: "stock", Currency: "CNY", Amount: 100000, Class: "cn_equity", Key: "own"}}}
	one, err := SimulateForecast(in, ForecastOptions{Years: 1, Paths: 100})
	if err != nil {
		t.Fatal(err)
	}
	three, err := SimulateForecast(in, ForecastOptions{Years: 3, Paths: 100})
	if err != nil {
		t.Fatal(err)
	}
	if one.Series[1] != three.Series[1] {
		t.Fatalf("horizon changed year1: %+v %+v", one.Series[1], three.Series[1])
	}
}
