// 回测测试：未来价格变化不能改写已经完成的历史预测。
package service

import (
	"math"
	"testing"
)

// trendingFixture 构造十二年完整月末总收益序列，供无前视测试使用。
func trendingFixture() []PriceObservation {
	rows := []PriceObservation{}
	price := 100.0
	for i := 0; i <= 144; i++ {
		at := day("2010-01-01").AddDate(0, i+1, -1)
		price *= math.Exp(.005)
		rows = append(rows, PriceObservation{Date: at.Format("2006-01-02"), Price: price, TotalPrice: price})
	}
	return rows
}

// TestBacktestForecastNeverUsesFutureTraining 验证未来价格修订只改变实际结果，不改变同一历史时点预测。
func TestBacktestForecastNeverUsesFutureTraining(t *testing.T) {
	rows := trendingFixture()
	in := ForecastInput{AsOf: day("2023-01-01"), Rates: map[string]float64{"CNY": 1}, Assets: []ForecastHolding{{ID: 1, Category: "stock", Currency: "CNY", Amount: 1000000, Class: "cn_equity", Key: "own"}}, History: map[string][]PriceObservation{"own": rows, "cn_equity": rows}}
	a, err := BacktestForecast(in, ForecastOptions{Years: 1, Paths: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Cases) == 0 {
		t.Fatal("no backtests")
	}
	mutated := append([]PriceObservation(nil), rows...)
	for i := range mutated {
		if mutated[i].Date > "2018-01-01" {
			mutated[i].TotalPrice *= 2
		}
	}
	in.History = map[string][]PriceObservation{"own": mutated, "cn_equity": mutated}
	b, err := BacktestForecast(in, ForecastOptions{Years: 1, Paths: 20})
	if err != nil {
		t.Fatal(err)
	}
	if a.Cases[0].Median != b.Cases[0].Median || a.Cases[0].P10 != b.Cases[0].P10 {
		t.Fatal("future data changed prediction")
	}
	if a.Cases[0].Actual == b.Cases[0].Actual {
		t.Fatal("fixture did not alter outcome")
	}
}

// TestBacktestLegacyUSDIncludesHistoricalFX 验证旧算法对照包含美元历史汇率收益，避免比较错基线。
func TestBacktestLegacyUSDIncludesHistoricalFX(t *testing.T) {
	rows := trendingFixture()
	fx := append([]PriceObservation(nil), rows...)
	for i := range rows {
		rows[i].TotalPrice = 100
		rows[i].Price = 100
		fx[i].TotalPrice = 7 * math.Pow(1.1, float64(i)/12)
	}
	in := ForecastInput{AsOf: day("2023-01-01"), Rates: map[string]float64{"USD": 7, "CNY": 1}, Assets: []ForecastHolding{{ID: 1, Category: "stock", Currency: "USD", Amount: 1000000, Class: "us_equity", Key: "own"}}, History: map[string][]PriceObservation{"own": rows, "us_equity": rows}, FX: map[string][]PriceObservation{"USD": fx}}
	report, err := BacktestForecast(in, ForecastOptions{Years: 1, Paths: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cases) == 0 {
		t.Fatal("no windows")
	}
	c := report.Cases[0]
	if math.Abs(float64(c.Actual-c.Legacy)) > 2 {
		t.Fatalf("legacy missed FX: %+v", c)
	}
}

// TestBacktestMoneyDoesNotInventActualReturns 验证没有历史期间货基收益时不以今天利率伪造回测结果。
func TestBacktestMoneyDoesNotInventActualReturns(t *testing.T) {
	in := ForecastInput{AsOf: day("2023-01-01"), Rates: map[string]float64{"CNY": 1}, Assets: []ForecastHolding{{ID: 1, Category: "money", Currency: "CNY", Amount: 1000000, AnnualRate: 4}}}
	out, err := BacktestForecast(in, ForecastOptions{Years: 1, Paths: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Cases) != 0 {
		t.Fatal("current money rate used as actual")
	}
}
