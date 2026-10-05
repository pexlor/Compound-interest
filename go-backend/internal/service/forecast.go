// 联合预测引擎：按月抽取共同历史片段，逐项计算资产、现金和退休积累目标。
package service

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

// ForecastHolding 是预测所需的本币余额、收益设定、历史键和基准类别。
type ForecastHolding struct {
	ID                                   int64
	Name, Category, Currency, Class, Key string
	Amount                               int64
	AnnualRate                           float64
}

// ForecastInput 保存同一快照中的资产、价格、外汇、目标和未来本金。
type ForecastInput struct {
	Context                context.Context
	AsOf                   time.Time
	Assets                 []ForecastHolding
	StoredAssets           []Asset
	RateDate               string
	History                map[string][]PriceObservation
	FX                     map[string][]PriceObservation
	Rates                  map[string]float64
	MonthlySavings, Target int64
	Events                 []Cashflow
}

// ForecastOptions 定义情景假设，Paths仅在内部测试和回测中允许改变。
type ForecastOptions struct {
	Years             int              `json:"years"`
	Inflation         float64          `json:"inflation"`
	IncludeRestricted bool             `json:"includeRestricted"` // 兼容旧接口字段；全部资产始终纳入退休目标。
	Benchmarks        map[int64]string `json:"benchmarks"`
	Paths             int              `json:"-"`
}

// DefaultForecastOptions 返回十年、2%通胀、纳入全部资产的默认情景。
func DefaultForecastOptions() ForecastOptions {
	return ForecastOptions{Years: 10, Inflation: 2, IncludeRestricted: true}
}

// ForecastPoint 是年度资产分位数、实际购买力、全部目标资产和期末达标概率。
type ForecastPoint struct {
	Year          int     `json:"year"`
	Date          string  `json:"date"`
	P10           int64   `json:"p10"`
	P50           int64   `json:"p50"`
	P90           int64   `json:"p90"`
	LiquidP50     int64   `json:"liquidP50"`
	RealP50       int64   `json:"realP50"`
	Contributions int64   `json:"contributions"`
	Target        int64   `json:"target"`
	Probability   float64 `json:"probability"`
}

// ForecastAsset 描述本币预测中位数、稳健年化和基准覆盖，用于单项详情。
type ForecastAsset struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Benchmark  string  `json:"benchmark"`
	Currency   string  `json:"currency"`
	AnnualRate float64 `json:"annualRate"`
	Months     int     `json:"months"`
	Forecast   int64   `json:"forecast"`
}

// ForecastMonth 区分本月投资收益、到账本金和总变化，金额均为人民币分。
type ForecastMonth struct {
	Date           string `json:"date"`
	InvestmentGain int64  `json:"investmentGain"`
	Contributions  int64  `json:"contributions"`
	TotalGain      int64  `json:"totalGain"`
}

// ForecastResult 保存可重现的模拟结果及透明模型假设；不可用时不返回伪造区间。
type ForecastResult struct {
	State          string          `json:"state"`
	Model          string          `json:"model"`
	AsOf           string          `json:"asOf"`
	Paths          int             `json:"paths"`
	SampleMonths   int             `json:"sampleMonths"`
	Options        ForecastOptions `json:"options"`
	Series         []ForecastPoint `json:"series"`
	Assets         []ForecastAsset `json:"assets"`
	ThisMonth      ForecastMonth   `json:"thisMonth"`
	RetirementDate string          `json:"retirementDate,omitempty"`
	Warnings       []string        `json:"warnings"`
	Missing        []string        `json:"missing"`
	ValuationBasis string          `json:"valuationBasis"`
}

// forecastModel 保存已对齐的资产残差和外汇变化；同一列索引代表同一个历史月份。
type forecastModel struct {
	Months   []string
	Starts   []int
	Drift    []float64
	Residual [][]float64
	FX       map[string][]float64
	Assets   []ForecastAsset
	Warnings []string
}

// prepareForecastModel 用稳健中心和共同日期构造联合抽样矩阵，拒绝不足的基准和外汇历史。
func prepareForecastModel(in ForecastInput, o ForecastOptions) (forecastModel, error) {
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

// blockCenter 按真实三月起点及偏移权重计算中心，避免端点样本改变设定漂移。
func blockCenter(series map[string]float64, months []string, starts []int) float64 {
	sum := 0.0
	for _, start := range starts {
		for offset := 0; offset < 3; offset++ {
			sum += series[months[start+offset]]
		}
	}
	return sum / float64(len(starts)*3)
}

// forecastCheckpoint 描述月内一个现金/报告/边界日期与对应历史抽样月份。
type forecastCheckpoint struct {
	Date        time.Time
	Month       int
	Fraction    float64
	Savings     bool
	Year        int
	Events      []Cashflow
	MonthReport bool
}

// forecastCheckpoints 以月初边界分段，保证周年、现金日和月末不会重复计息或重复到账。
func forecastCheckpoints(in ForecastInput, o ForecastOptions) []forecastCheckpoint {
	end := in.AsOf.AddDate(o.Years, 0, 0)
	start := time.Date(in.AsOf.Year(), in.AsOf.Month(), 1, 0, 0, 0, 0, time.UTC)
	result := []forecastCheckpoint{}
	for month, at := 0, start; at.Before(end); month, at = month+1, at.AddDate(0, 1, 0) {
		next := at.AddDate(0, 1, 0)
		stop := next
		if stop.After(end) {
			stop = end
		}
		prev := at
		if prev.Before(in.AsOf) {
			prev = in.AsOf
		}
		dates := map[string]*forecastCheckpoint{}
		// addDate 合并同一天的收入、报告和日历边界。
		addDate := func(date time.Time) *forecastCheckpoint {
			if !date.After(prev) || date.After(stop) {
				return nil
			}
			key := date.Format("2006-01-02")
			if dates[key] == nil {
				dates[key] = &forecastCheckpoint{Date: date, Month: month, Year: -1}
			}
			return dates[key]
		}
		addDate(stop)
		monthEnd := next.AddDate(0, 0, -1)
		if p := addDate(monthEnd); p != nil {
			p.Savings = true
			p.MonthReport = month == 0
		}
		for year := 1; year <= o.Years; year++ {
			if p := addDate(in.AsOf.AddDate(year, 0, 0)); p != nil {
				p.Year = year
			}
		}
		for _, e := range in.Events {
			date, err := time.Parse("2006-01-02", e.Date)
			if err == nil {
				if p := addDate(date); p != nil {
					p.Events = append(p.Events, e)
				}
			}
		}
		keys := []string{}
		for key := range dates {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			p := *dates[key]
			p.Fraction = p.Date.Sub(prev).Hours() / 24 / (next.Sub(at).Hours() / 24)
			result = append(result, p)
			prev = p.Date
		}
	}
	return result
}

// percentileMinor 排序后按最近秩取分位数并四舍五入到分；返回前验证数值范围。
func percentileMinor(values []float64, q float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return int64(math.Round(values[int(math.Round(q*float64(len(values)-1)))]))
}

// SimulateForecast 执行固定种子的5000条三月联合抽样路径，分别累积资产和未来现金本金。
func SimulateForecast(in ForecastInput, o ForecastOptions) (ForecastResult, error) {
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
	m, err := prepareForecastModel(in, o)
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
