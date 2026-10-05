// 滚动回测：在历史月初只用当时可获得的价格训练，并和固定三年年化外推比较。
package service

import (
	"fmt"
	"math"
	"time"
)

// BacktestCase 保存一次历史预测、实际终值、旧算法终值和区间覆盖。
type BacktestCase struct {
	AsOf    string `json:"asOf"`
	Years   int    `json:"years"`
	Median  int64  `json:"median"`
	P10     int64  `json:"p10"`
	P90     int64  `json:"p90"`
	Actual  int64  `json:"actual"`
	Legacy  int64  `json:"legacy"`
	Covered bool   `json:"covered"`
}

// BacktestReport 汇总绝对相对误差、覆盖率及回测适用范围，不把合成样本当真实业绩。
type BacktestReport struct {
	Years       int            `json:"years"`
	Cases       []BacktestCase `json:"cases"`
	MedianError float64        `json:"medianError"`
	LegacyError float64        `json:"legacyError"`
	Coverage    float64        `json:"coverage"`
	Note        string         `json:"note"`
}

// observationAt 获取指定日及此前七天内的最后有效观测，拒绝过旧价格。
func observationAt(rows []PriceObservation, at time.Time) (float64, bool) {
	key := at.Format("2006-01-02")
	floor := at.AddDate(0, 0, -7).Format("2006-01-02")
	date := ""
	price := 0.0
	for _, p := range rows {
		if p.Date <= key && p.Date >= floor && p.Date > date && positiveFinite(p.TotalPrice) {
			date = p.Date
			price = p.TotalPrice
		}
	}
	return price, date != ""
}

// BacktestForecast 按年选择历史月初，用训练快照重算稳健中心，测试当前持仓固定权重的1/3/5年终值。
func BacktestForecast(in ForecastInput, o ForecastOptions) (BacktestReport, error) {
	report := BacktestReport{Years: o.Years, Cases: []BacktestCase{}, Note: "按当前持仓金额权重回测，排除收入现金流；重叠样本非独立，供应商复权历史修订及存活偏差未消除。参数尚未按独立样本校准。"}
	if o.Years != 1 && o.Years != 3 && o.Years != 5 {
		return report, fmt.Errorf("回测期限仅支持1、3、5年")
	}
	for _, a := range in.Assets {
		if a.Category == "money" {
			report.Note += " 含货币基金：缺少经核验的历史期间收益，暂不生成组合误差，避免用当前利率伪造实际终值。"
			return report, nil
		}
	}
	cutoff := time.Date(in.AsOf.Year(), in.AsOf.Month(), 1, 0, 0, 0, 0, time.UTC)
	first := cutoff.AddDate(-10, 0, 0)
	for at := first.AddDate(5, 0, 0); !at.AddDate(o.Years, 0, 0).After(cutoff); at = at.AddDate(1, 0, 0) {
		training := in
		training.AsOf = at
		training.Events = nil
		training.MonthlySavings = 0
		training.Target = 0
		training.Rates = map[string]float64{"CNY": 1}
		ok := true
		for _, a := range in.Assets {
			if a.Currency != "CNY" {
				rate, found := observationAt(in.FX[a.Currency], at)
				if !found {
					ok = false
					break
				}
				training.Rates[a.Currency] = rate
			}
		}
		if !ok {
			continue
		}
		result, err := SimulateForecast(training, o)
		if err != nil {
			continue
		}
		actual, legacy := 0.0, 0.0
		for _, a := range in.Assets {
			rate := training.Rates[a.Currency]
			endFX := rate
			if a.Currency != "CNY" {
				var found bool
				endFX, found = observationAt(in.FX[a.Currency], at.AddDate(o.Years, 0, 0))
				if !found {
					ok = false
					break
				}
			}
			ratio := 1.0
			legacyRatio := 1.0
			if a.Category == "stock" || a.Category == "fund" {
				start, found := observationAt(in.History[a.Key], at)
				if !found {
					ok = false
					break
				}
				end, found := observationAt(in.History[a.Key], at.AddDate(o.Years, 0, 0))
				if !found {
					ok = false
					break
				}
				old, found := observationAt(in.History[a.Key], at.AddDate(-3, 0, 0))
				if !found {
					ok = false
					break
				}
				ratio = end / start
				legacyBase := start / old
				if a.Currency == "USD" {
					oldFX, found := observationAt(in.FX[a.Currency], at.AddDate(-3, 0, 0))
					if !found {
						ok = false
						break
					}
					legacyBase *= rate / oldFX
				}
				legacyRatio = math.Pow(legacyBase, float64(o.Years)/3)
			} else if a.Category != "fixed" {
				ratio = math.Pow(1+a.AnnualRate/100, float64(o.Years))
				legacyRatio = ratio
			}
			actual += float64(a.Amount) * ratio * endFX
			legacy += float64(a.Amount) * legacyRatio * rate
		}
		if !ok || !positiveFinite(actual) || actual > 9e15 || legacy > 9e15 {
			continue
		}
		p := result.Series[o.Years]
		c := BacktestCase{AsOf: at.Format("2006-01-02"), Years: o.Years, Median: p.P50, P10: p.P10, P90: p.P90, Actual: int64(math.Round(actual)), Legacy: int64(math.Round(legacy)), Covered: actual >= float64(p.P10)-1 && actual <= float64(p.P90)+1}
		report.Cases = append(report.Cases, c)
		report.MedianError += math.Abs(float64(c.Median-c.Actual)) / actual
		report.LegacyError += math.Abs(float64(c.Legacy-c.Actual)) / actual
		if c.Covered {
			report.Coverage++
		}
	}
	n := float64(len(report.Cases))
	if n > 0 {
		report.MedianError /= n
		report.LegacyError /= n
		report.Coverage /= n
	} else {
		report.Note += " 缓存样本不足，暂无可评估窗口。"
	}
	return report, nil
}
