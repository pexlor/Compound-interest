// 优化回归参考：冻结原始路径循环，只用于测试，不参与生产编译。
package service

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

// simulateForecastReference 保留优化前路径执行逻辑，检验金额、概率、现金与达标日期完全一致。
func simulateForecastReference(in ForecastInput, o ForecastOptions) (ForecastResult, error) {
	o.IncludeRestricted = true
	out := ForecastResult{State: "ready", Model: "joint-block-v1", AsOf: in.AsOf.Format("2006-01-02"), Options: o, Warnings: []string{}, Missing: []string{}, ValuationBasis: "stored_assets"}
	if o.Years < 1 || o.Years > 30 || !finiteRange(o.Inflation, 0, 20) || in.MonthlySavings < 0 || in.Target < 0 {
		return out, fmt.Errorf("预测年限、通胀或本金无效")
	}
	if o.Paths == 0 {
		o.Paths = 5000
	}
	if o.Paths < 1 || o.Paths > 5000 {
		return out, fmt.Errorf("模拟路径数无效")
	}
	out.Paths = o.Paths
	m, err := prepareForecastModelReference(in, o)
	if err != nil {
		return out, err
	}
	out.SampleMonths = len(m.Months)
	out.Assets = m.Assets
	out.Warnings = m.Warnings
	checkpoints := forecastCheckpoints(in, o)
	values, liquid, contrib := make([][]float64, o.Years+1), make([][]float64, o.Years+1), make([][]float64, o.Years+1)
	assetValues := make([][]float64, len(in.Assets))
	monthValues, monthContrib := []float64{}, []float64{}
	hits := []float64{}
	total, liquidTotal := 0.0, 0.0
	for _, a := range in.Assets {
		v := float64(a.Amount) * in.Rates[a.Currency]
		total += v
		liquidTotal += v
	}

	if math.IsNaN(total) || math.IsInf(total, 0) || total > 9e15 || float64(in.Target)*math.Pow(1+o.Inflation/100, float64(o.Years)+.01) > 9e15 {
		return out, fmt.Errorf("余额或目标超出安全金额范围")
	}
	ctx := in.Context
	if ctx == nil {
		ctx = context.Background()
	}
	for path := 0; path < o.Paths; path++ {
		rng := rand.New(rand.NewSource(20261005 + int64(path)))
		if path%32 == 0 {
			if err := ctx.Err(); err != nil {
				return out, err
			}
		}
		balances := make([]float64, len(in.Assets))
		for i, a := range in.Assets {
			balances[i] = float64(a.Amount)
		}
		fx := map[string]float64{}
		for c, r := range in.Rates {
			fx[c] = r
		}
		cash := 0.0
		firstHit := -1.0
		if in.Target > 0 && liquidTotal >= float64(in.Target) {
			firstHit = 0
		}
		selectedMonth, index := -1, 0
		for _, p := range checkpoints {
			if p.Month != selectedMonth {
				selectedMonth = p.Month
				if len(m.Starts) > 0 {
					if selectedMonth%3 == 0 {
						index = m.Starts[rng.Intn(len(m.Starts))]
					} else {
						index++
					}
				}
			}
			for i := range balances {
				logReturn := m.Drift[i]
				if len(m.Months) > 0 {
					logReturn += m.Residual[i][index]
				}
				balances[i] *= math.Exp(logReturn * p.Fraction)
				if math.IsNaN(balances[i]) || math.IsInf(balances[i], 0) || balances[i] > 9e15 {
					return out, fmt.Errorf("本币模拟金额超出范围，请缩短预测期限")
				}
			}
			for c, changes := range m.FX {
				fx[c] *= math.Exp(changes[index] * p.Fraction)
			}
			for _, e := range p.Events {
				if e.Amount < 0 || !positiveFinite(fx[e.Currency]) {
					return out, fmt.Errorf("现金流金额或汇率无效")
				}
				cash += float64(e.Amount) * fx[e.Currency]
			}
			if p.Savings {
				cash += float64(in.MonthlySavings)
			}
			v, lv := cash, cash
			for i, a := range in.Assets {
				amount := balances[i] * fx[a.Currency]
				v += amount
				lv += amount
			}
			if math.IsNaN(v) || math.IsInf(v, 0) || v > 9e15 || v < 0 {
				return out, fmt.Errorf("模拟金额超出范围，请缩短预测期限")
			}
			elapsed := p.Date.Sub(in.AsOf).Hours() / 24 / 365.25
			target := float64(in.Target) * math.Pow(1+o.Inflation/100, elapsed)
			if firstHit < 0 && in.Target > 0 && lv >= target {
				firstHit = p.Date.Sub(in.AsOf).Hours() / 24
			}
			if p.MonthReport {
				monthValues = append(monthValues, v)
				monthContrib = append(monthContrib, cash)
			}
			if p.Year > 0 {
				values[p.Year] = append(values[p.Year], v)
				liquid[p.Year] = append(liquid[p.Year], lv)
				contrib[p.Year] = append(contrib[p.Year], cash)
			}
		}
		if firstHit >= 0 {
			hits = append(hits, firstHit)
		}
		for i := range balances {
			assetValues[i] = append(assetValues[i], balances[i])
		}
	}
	out.Series = append(out.Series, ForecastPoint{Year: 0, Date: out.AsOf, P10: int64(math.Round(total)), P50: int64(math.Round(total)), P90: int64(math.Round(total)), LiquidP50: int64(math.Round(liquidTotal)), RealP50: int64(math.Round(total)), Target: in.Target})
	if in.Target > 0 && liquidTotal >= float64(in.Target) {
		out.Series[0].Probability = 1
	}
	for year := 1; year <= o.Years; year++ {
		date := in.AsOf.AddDate(year, 0, 0)
		factor := math.Pow(1+o.Inflation/100, date.Sub(in.AsOf).Hours()/24/365.25)
		target := float64(in.Target) * factor
		prob := 0.0
		if in.Target > 0 {
			for _, v := range liquid[year] {
				if v >= target {
					prob++
				}
			}
			prob /= float64(o.Paths)
		}
		p50 := percentileMinor(values[year], .5)
		out.Series = append(out.Series, ForecastPoint{Year: year, Date: date.Format("2006-01-02"), P10: percentileMinor(values[year], .1), P50: p50, P90: percentileMinor(values[year], .9), LiquidP50: percentileMinor(liquid[year], .5), RealP50: int64(math.Round(float64(p50) / factor)), Contributions: percentileMinor(contrib[year], .5), Target: int64(math.Round(target)), Probability: prob})
	}
	for i := range out.Assets {
		out.Assets[i].Forecast = percentileMinor(assetValues[i], .5)
	}
	monthCash := percentileMinor(monthContrib, .5)
	monthValue := percentileMinor(monthValues, .5)
	monthEnd := time.Date(in.AsOf.Year(), in.AsOf.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	out.ThisMonth = ForecastMonth{Date: monthEnd.Format("2006-01-02"), Contributions: monthCash, InvestmentGain: monthValue - int64(math.Round(total)) - monthCash, TotalGain: monthValue - int64(math.Round(total))}
	if len(monthValues) == 0 {
		out.ThisMonth = ForecastMonth{Date: out.AsOf}
	}
	if len(hits) >= (o.Paths+1)/2 {
		sort.Float64s(hits)
		out.RetirementDate = in.AsOf.AddDate(0, 0, int(hits[(o.Paths-1)/2])).Format("2006-01-02")
	}
	return out, nil
}

// prepareForecastModelReference 冻结优化前模型准备，独立验证月收益复用不改变原有结果。
func prepareForecastModelReference(in ForecastInput, o ForecastOptions) (forecastModel, error) {
	m := forecastModel{FX: map[string][]float64{}, Warnings: []string{}}
	own := make([]map[string]float64, len(in.Assets))
	base := make([]map[string]float64, len(in.Assets))
	all := []map[string]float64{}
	for i, a := range in.Assets {
		if a.Amount < 0 || !finiteRange(a.AnnualRate, -100, 1000) {
			return m, fmt.Errorf("资产金额或利率无效")
		}
		if !positiveFinite(in.Rates[a.Currency]) {
			return m, fmt.Errorf("缺少 %s 当前汇率", a.Currency)
		}
		drift := 0.0
		months := 0
		if a.Category == "stock" || a.Category == "fund" {
			if a.Class == "" {
				return m, fmt.Errorf("%s 请先选择预测基准", a.Name)
			}
			var err error
			base[i], err = MonthlyReturns(in.History[a.Class], in.AsOf)
			if err != nil {
				return m, err
			}
			if len(base[i]) < 60 {
				return m, fmt.Errorf("%s 基准完整月历史不足60个月", a.Class)
			}
			if len(in.History[a.Key]) < 2 {
				return m, fmt.Errorf("%s 缺少本币总收益历史", a.Name)
			}
			own[i], err = MonthlyReturns(in.History[a.Key], in.AsOf)
			if err != nil {
				return m, err
			}
			drift, months = EstimateDrift(own[i], base[i])
			all = append(all, base[i])
			if months >= 36 {
				all = append(all, own[i])
			} else {
				m.Warnings = append(m.Warnings, a.Name+" 历史不足三年，采用基准收益与波动")
			}
		} else if a.Category != "fixed" {
			if a.AnnualRate <= -100 {
				return m, fmt.Errorf("非证券收益率必须大于-100%%")
			}
			drift = math.Log1p(a.AnnualRate/100) / 12
		}
		m.Drift = append(m.Drift, drift)
		m.Assets = append(m.Assets, ForecastAsset{ID: a.ID, Name: a.Name, Benchmark: a.Class, Currency: a.Currency, AnnualRate: math.Expm1(drift*12) * 100, Months: months})
	}
	end := in.AsOf.AddDate(o.Years, 0, 0).Format("2006-01-02")
	needed := map[string]bool{}
	for _, a := range in.Assets {
		if a.Currency != "CNY" {
			needed[a.Currency] = true
		}
	}
	for _, e := range in.Events {
		if e.Date > in.AsOf.Format("2006-01-02") && e.Date <= end && e.Currency != "CNY" {
			needed[e.Currency] = true
		}
	}
	currencies := []string{}
	fxMonthly := map[string]map[string]float64{}
	for c := range needed {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	for _, c := range currencies {
		if !positiveFinite(in.Rates[c]) {
			return m, fmt.Errorf("缺少 %s 当前汇率", c)
		}
		series, err := MonthlyReturns(in.FX[c], in.AsOf)
		if err != nil {
			return m, err
		}
		if len(series) < 36 {
			return m, fmt.Errorf("%s 历史汇率不足36个完整月", c)
		}
		fxMonthly[c] = series
		all = append(all, series)
	}
	if len(all) > 0 {
		m.Months = commonMonths(all)
		if len(m.Months) < 36 {
			return m, fmt.Errorf("共同历史月份不足36个月")
		}
		longest, run := 1, 1
		for i := 1; i < len(m.Months); i++ {
			at, _ := time.Parse("2006-01", m.Months[i-1])
			if at.AddDate(0, 1, 0).Format("2006-01") == m.Months[i] {
				run++
			} else {
				run = 1
			}
			if run > longest {
				longest = run
			}
		}
		if longest < 36 {
			return m, fmt.Errorf("连续共同历史不足36个月")
		}
		for i := 0; i+2 < len(m.Months); i++ {
			at, _ := time.Parse("2006-01", m.Months[i])
			if at.AddDate(0, 1, 0).Format("2006-01") == m.Months[i+1] && at.AddDate(0, 2, 0).Format("2006-01") == m.Months[i+2] {
				m.Starts = append(m.Starts, i)
			}
		}
		if len(m.Starts) < 12 {
			return m, fmt.Errorf("连续三月历史片段不足12组")
		}
	}
	for i := range in.Assets {
		r := make([]float64, len(m.Months))
		if base[i] != nil {
			source := base[i]
			if m.Assets[i].Months >= 36 {
				source = own[i]
			}
			center := blockCenter(source, m.Months, m.Starts)
			for j, key := range m.Months {
				r[j] = source[key] - center
			}
		}
		m.Residual = append(m.Residual, r)
	}
	for _, c := range currencies {
		r := make([]float64, len(m.Months))
		center := blockCenter(fxMonthly[c], m.Months, m.Starts)
		for j, key := range m.Months {
			r[j] = fxMonthly[c][key] - center
		}
		m.FX[c] = r
	}
	return m, nil
}
