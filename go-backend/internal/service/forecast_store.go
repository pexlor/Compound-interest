// 预测数据装配：从一致快照读取当前用户资产、共享行情和收入，计算过程不改写账本。
package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ForecastSecurity 表示后台需要补齐的共享证券日线，不携带用户余额。
type ForecastSecurity struct{ Category, Code string }

// ForecastResponse 同时返回预测范围和同一情景下的退休进度。
type ForecastResponse struct {
	ForecastResult
	Retirement   Retirement         `json:"retirement"`
	Rates        map[string]float64 `json:"rates"`
	RateDate     string             `json:"rateDate"`
	StoredAssets []Asset            `json:"storedAssets"`
}

// ForecastBenchmarks 返回风险类别和代表性宽基/国债ETF的对应关系。
func ForecastBenchmarks() map[string]ForecastSecurity {
	return map[string]ForecastSecurity{"cn_equity": {"stock", "SH510300"}, "us_equity": {"stock", "SPY"}, "hk_equity": {"stock", "02800.HK"}, "cn_bond": {"stock", "SH511010"}}
}

// ForecastIdentity 将持仓代码规范化成已有日线缓存键，场外基金与交易所基金分别处理。
func ForecastIdentity(category, code string) ForecastSecurity {
	code = strings.ToUpper(strings.TrimSpace(code))
	digits := true
	for _, r := range code {
		if r < '0' || r > '9' {
			digits = false
		}
	}
	exchange := false
	for _, prefix := range []string{"51", "52", "56", "58", "15", "16"} {
		if strings.HasPrefix(code, prefix) {
			exchange = true
		}
	}
	if len(code) == 6 && digits && (category == "money" || (category == "fund" && !exchange)) {
		return ForecastSecurity{category, code}
	}
	if strings.HasPrefix(code, "US") {
		code = strings.TrimPrefix(code, "US")
	}
	if strings.HasPrefix(code, "HK") && len(code) == 7 {
		code = code[2:] + ".HK"
	}
	if strings.HasSuffix(code, ".SS") {
		code = "SH" + strings.TrimSuffix(code, ".SS")
	}
	if strings.HasSuffix(code, ".SZ") {
		code = "SZ" + strings.TrimSuffix(code, ".SZ")
	}
	if len(code) == 6 && digits {
		prefix := "SZ"
		if strings.HasPrefix(code, "5") || strings.HasPrefix(code, "6") || strings.HasPrefix(code, "9") {
			prefix = "SH"
		}
		code = prefix + code
	}
	if strings.HasSuffix(code, ".HK") {
		n, err := strconv.Atoi(strings.TrimSuffix(code, ".HK"))
		if err == nil {
			code = fmt.Sprintf("%05d.HK", n)
		}
	}
	return ForecastSecurity{"stock", code}
}

// ForecastAssetClass 对普通股票按报价币种映射，基金必须显式选择基准以免类型误判。
func ForecastAssetClass(a Asset, o ForecastOptions) string {
	if selected, ok := o.Benchmarks[a.ID]; ok {
		return selected
	}
	if a.Category != "stock" {
		return ""
	}
	switch a.Currency {
	case "USD":
		return "us_equity"
	case "HKD":
		return "hk_equity"
	case "CNY":
		return "cn_equity"
	}
	return ""
}

// readPriceHistory 读取实际日期的原价和收益价，不把缓存获取日期当交易日期。
func readPriceHistory(q Queryer, s ForecastSecurity, asOf time.Time) ([]PriceObservation, error) {
	rows, err := q.Query(`SELECT price_date,price,return_price,income FROM market_daily_prices WHERE category=? AND code=? AND price_date<=? ORDER BY price_date`, s.Category, s.Code, asOf.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PriceObservation{}
	for rows.Next() {
		var p PriceObservation
		if err = rows.Scan(&p.Date, &p.Price, &p.TotalPrice, &p.Income); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LoadForecastInput 读取用户隔离的持仓与目标，并装配所需总收益和外汇序列。
func LoadForecastInput(q Queryer, userID int64, o ForecastOptions, asOf time.Time) (ForecastInput, Retirement, []ForecastSecurity, []string, error) {
	in := ForecastInput{AsOf: asOf, History: map[string][]PriceObservation{}, FX: map[string][]PriceObservation{}, Rates: map[string]float64{"CNY": 1}}
	summary, err := RetirementFor(q, userID)
	if err != nil {
		return in, summary, nil, nil, err
	}
	warnings := []string{"当前余额按已保存估值计算；预测区间为模型情景，未覆盖所有极端风险。", "存款、公积金按输入利率保持不变；未来到账资金只计本金。"}
	assets, err := ListAssets(q, userID, false)
	if err != nil {
		return in, summary, nil, warnings, err
	}
	in.StoredAssets = assets
	rates, err := q.Query(`SELECT currency,cny_rate,rate_date FROM exchange_rates`)
	if err != nil {
		return in, summary, nil, warnings, err
	}
	for rates.Next() {
		var c, d string
		var r float64
		if err = rates.Scan(&c, &r, &d); err != nil {
			rates.Close()
			return in, summary, nil, warnings, err
		}
		if positiveFinite(r) {
			in.Rates[c] = r
			if d > in.RateDate {
				in.RateDate = d
			}
			if d < asOf.AddDate(0, 0, -7).Format("2006-01-02") {
				warnings = append(warnings, c+" 当前汇率超过七天未更新")
			}
		}
	}
	err = rates.Err()
	rates.Close()
	if err != nil {
		return in, summary, nil, warnings, err
	}
	income, err := ReadIncome(q, userID)
	if err != nil {
		return in, summary, nil, warnings, err
	}
	in.MonthlySavings = income.MonthlySavings
	in.Events = CompensationEvents(income, asOf, asOf.AddDate(o.Years, 0, 0))
	in.Target = summary.TargetCNY
	securities := []ForecastSecurity{}
	seen := map[ForecastSecurity]bool{}
	// load 复用相同证券日线，避免多笔持仓重复读取。
	load := func(s ForecastSecurity) ([]PriceObservation, error) {
		if !seen[s] {
			securities = append(securities, s)
			seen[s] = true
		}
		return readPriceHistory(q, s, asOf)
	}
	for _, a := range assets {
		h := ForecastHolding{ID: a.ID, Name: a.Name, Category: a.Category, Currency: a.Currency, Amount: a.Amount, AnnualRate: a.AnnualRate, Class: ForecastAssetClass(a, o)}
		if a.Code != nil && (a.Category == "stock" || a.Category == "fund" || a.Category == "money") {
			s := ForecastIdentity(a.Category, *a.Code)
			h.Key = s.Category + ":" + s.Code
			raw, e := load(s)
			if e != nil {
				return in, summary, securities, warnings, e
			}
			if len(raw) > 0 && raw[len(raw)-1].Date < asOf.AddDate(0, 0, -10).Format("2006-01-02") {
				warnings = append(warnings, a.Name+" 历史行情超过十天未更新")
			}
			if s.Category == "fund" {
				raw, e = FundTotalReturn(raw)
				if e != nil {
					warnings = append(warnings, a.Name+": "+e.Error())
					raw = nil
				}
			}
			in.History[h.Key] = raw
			if a.Category == "money" {
				sum := 0.0
				count := 0
				from := asOf.AddDate(0, 0, -90).Format("2006-01-02")
				for _, p := range raw {
					if p.Date >= from {
						sum += p.Income
						count++
					}
				}
				if count >= 30 {
					h.AnnualRate = sum / float64(count) * 365 / 100
				} else {
					warnings = append(warnings, a.Name+" 货币基金历史不足30天，暂用输入利率")
				}
			}
		}
		if b, ok := ForecastBenchmarks()[h.Class]; ok {
			raw, e := load(b)
			if e != nil {
				return in, summary, securities, warnings, e
			}
			in.History[h.Class] = raw
		}
		in.Assets = append(in.Assets, h)
	}
	currencies := map[string]bool{}
	for _, a := range in.Assets {
		if a.Currency != "CNY" {
			currencies[a.Currency] = true
		}
	}
	for _, e := range in.Events {
		if e.Currency != "CNY" {
			currencies[e.Currency] = true
		}
	}
	for c := range currencies {
		rows, e := q.Query(`SELECT rate_date,cny_rate FROM exchange_rate_history WHERE currency=? AND rate_date<=? ORDER BY rate_date`, c, asOf.Format("2006-01-02"))
		if e != nil {
			return in, summary, securities, warnings, e
		}
		for rows.Next() {
			var p PriceObservation
			if e = rows.Scan(&p.Date, &p.TotalPrice); e != nil {
				rows.Close()
				return in, summary, securities, warnings, e
			}
			in.FX[c] = append(in.FX[c], p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return in, summary, securities, warnings, e
		}
	}
	return in, summary, securities, warnings, nil
}

// ForecastFromSnapshot 计算已装配快照，缺数据时返回明确不可用状态而非零收益。
func ForecastFromSnapshot(in ForecastInput, summary Retirement, warnings []string, o ForecastOptions) ForecastResponse {
	result, err := SimulateForecast(in, o)
	if !summary.Complete {
		err = fmt.Errorf("目标或资产缺少当前汇率：%s", strings.Join(summary.MissingCurrencies, "、"))
	}
	if err != nil {
		result = ForecastResult{State: "unavailable", Model: "joint-block-v1", AsOf: in.AsOf.Format("2006-01-02"), Options: o, Series: []ForecastPoint{}, Assets: []ForecastAsset{}, Warnings: []string{}, Missing: []string{err.Error()}, ValuationBasis: "stored_assets"}
	}
	result.Warnings = append(warnings, result.Warnings...)
	summary.ProjectedYears = nil
	summary.ProjectedDate = ""
	summary.AnnualRate = 0
	summary.ForecastState = result.State
	summary.LiquidCNY = 0
	for _, a := range in.Assets {
		if liquidHolding(a, o.IncludeRestricted) {
			summary.LiquidCNY += int64(math.Round(float64(a.Amount) * in.Rates[a.Currency]))
		}
	}
	if summary.TargetCNY > 0 {
		summary.Progress = math.Min(100, float64(summary.LiquidCNY)/float64(summary.TargetCNY)*100)
	}
	if result.State == "ready" {
		weighted, total := 0.0, 0.0
		for i, a := range in.Assets {
			value := float64(a.Amount) * in.Rates[a.Currency]
			total += value
			weighted += value * result.Assets[i].AnnualRate
		}
		if total > 0 {
			summary.AnnualRate = weighted / total
		}
		summary.ProjectedDate = result.RetirementDate
		if result.RetirementDate != "" {
			date, _ := time.Parse("2006-01-02", result.RetirementDate)
			years := date.Sub(in.AsOf).Hours() / 24 / 365.25
			summary.ProjectedYears = &years
		}
	}
	// 不确定的远期外币收入不能阻止此前已由已知资金达标的日期。
	if result.State != "ready" && summary.Complete {
		known := in
		known.Events = nil
		firstUnknown := ""
		for _, e := range in.Events {
			fx, fxErr := MonthlyReturns(in.FX[e.Currency], in.AsOf)
			if e.Currency != "CNY" && (!positiveFinite(in.Rates[e.Currency]) || fxErr != nil || len(fx) < 36) {
				if firstUnknown == "" || e.Date < firstUnknown {
					firstUnknown = e.Date
				}
			} else {
				known.Events = append(known.Events, e)
			}
		}
		if firstUnknown != "" {
			prefix, e := SimulateForecast(known, o)
			if e == nil && prefix.RetirementDate != "" && prefix.RetirementDate < firstUnknown {
				summary.ProjectedDate = prefix.RetirementDate
				date, _ := time.Parse("2006-01-02", prefix.RetirementDate)
				years := date.Sub(in.AsOf).Hours() / 24 / 365.25
				summary.ProjectedYears = &years
				summary.ForecastState = "partial"
				result.Warnings = append(result.Warnings, "退休目标可在缺少汇率的远期收入到账前达成；完整资产预测仍等待数据。")
			}
		}
	}
	return ForecastResponse{ForecastResult: result, Retirement: summary, StoredAssets: in.StoredAssets, Rates: in.Rates, RateDate: in.RateDate}
}

// Forecast 在短只读事务内装配快照，释放数据库连接后运行可取消的模拟。
func (s *Ledger) Forecast(ctx context.Context, userID int64, o ForecastOptions, asOf time.Time) (ForecastResponse, []ForecastSecurity, ForecastInput, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ForecastResponse{}, nil, ForecastInput{}, err
	}
	defer tx.Rollback()
	in, summary, securities, warnings, err := LoadForecastInput(tx, userID, o, asOf)
	if err != nil {
		return ForecastResponse{}, nil, in, err
	}
	if err = tx.Commit(); err != nil {
		return ForecastResponse{}, nil, in, err
	}
	in.Context = ctx
	out := ForecastFromSnapshot(in, summary, warnings, o)
	return out, securities, in, nil
}
