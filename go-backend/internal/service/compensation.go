// 收入预测逻辑：校验年终奖和期权计划，生成未来现金流并预测退休日期。

package service

import (
	"math"
	"sort"
	"strings"
	"time"
)

var planningZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// BonusSettings 描述入职日期、奖金领取月日和奖金所属年度偏移。
type BonusSettings struct {
	WorkStartDate string `json:"workStartDate"`
	PayMonth      int    `json:"payMonth"`
	PayDay        int    `json:"payDay"`
	YearOffset    int    `json:"yearOffset"`
}

// OptionBatch 描述一批期权的归属日期、数量和变现方式。
type OptionBatch struct {
	VestDate string  `json:"vestDate"`
	Quantity float64 `json:"quantity"`
	CashMode string  `json:"cashMode"`
	CashDate string  `json:"cashDate"`
}

// OptionGrant 描述一份期权授予的币种、价格、税率与归属批次。
type OptionGrant struct {
	Name        string        `json:"name"`
	Currency    string        `json:"currency"`
	Quantity    float64       `json:"quantity"`
	StrikePrice float64       `json:"strikePrice"`
	MarketPrice float64       `json:"marketPrice"`
	TaxRate     float64       `json:"taxRate"`
	Batches     []OptionBatch `json:"batches"`
}

// Cashflow 表示指定日期到账的收入，金额以最小货币单位记录。
type Cashflow struct {
	Date        string  `json:"date"`
	Amount      int64   `json:"amount"`
	Currency    string  `json:"currency"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	WorkRatio   float64 `json:"work_ratio,omitempty"`
	EarningYear int     `json:"earning_year,omitempty"`
}

// compensationSettings 封装以 JSON 形式持久化的奖金和期权计划。
type compensationSettings struct {
	BonusSettings *BonusSettings `json:"bonusSettings"`
	Options       []OptionGrant  `json:"options"`
}

// validDate 判断字符串是否为有效的 YYYY-MM-DD 格式日期。
func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return len(s) == 10 && err == nil
}

// finiteRange 判断数值是否有限且位于允许范围内，排除 NaN 和无穷值。
func finiteRange(n, min, max float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= min && n <= max
}

// ValidateCompensation 校验奖金领取规则、期权价格和税率，以及归属数量和变现日期。
func ValidateCompensation(b *BonusSettings, grants []OptionGrant) error {
	if b != nil {
		// A recurring February 29 payment falls on February 28 in non-leap years.
		if !validDate(b.WorkStartDate) || b.PayMonth < 1 || b.PayMonth > 12 || b.PayDay < 1 || b.PayDay > time.Date(2000, time.Month(b.PayMonth)+1, 0, 0, 0, 0, 0, time.UTC).Day() || b.YearOffset < 0 || b.YearOffset > 1 {
			return invalid("年终奖入职日期、领取月日或所属年度无效")
		}
	}
	if len(grants) > 100 {
		return invalid("期权授予最多 100 份")
	}
	for _, g := range grants {
		if strings.TrimSpace(g.Name) == "" || len([]rune(g.Name)) > 120 || !Currency(g.Currency) || !finiteRange(g.Quantity, 0.000001, 1e9) || !finiteRange(g.StrikePrice, 0, 1e9) || !finiteRange(g.MarketPrice, 0, 1e9) || !finiteRange(g.TaxRate, 0, 100) || len(g.Batches) > 1200 {
			return invalid("期权名称、币种、数量、价格或税率无效")
		}
		sum := 0.0
		for _, batch := range g.Batches {
			if !validDate(batch.VestDate) || !finiteRange(batch.Quantity, 0.000001, g.Quantity) {
				return invalid("归属日期或数量无效")
			}
			switch batch.CashMode {
			case "immediate", "hold":
			case "date":
				if !validDate(batch.CashDate) || batch.CashDate < batch.VestDate {
					return invalid("变现日期不能早于归属日期")
				}
			default:
				return invalid("请选择归属即变现、指定日期或暂不变现")
			}
			sum += batch.Quantity
		}
		if sum > g.Quantity+1e-6 || g.Quantity*math.Max(0, g.MarketPrice-g.StrikePrice)*100 > 9e15 {
			return invalid("归属数量超过授予总量或预估金额过大")
		}
	}
	return nil
}

// CompensationEvents returns future proceeds in minor currency units. The current
// asset balance already represents all proceeds on or before the as-of date.
// CompensationEvents 生成预测起点之后、截止日期以内的奖金和期权净收入，金额使用最小货币单位。
// 起点当日及此前已到账的收入由当前资产余额体现，避免重复计入。
func CompensationEvents(i Income, start, end time.Time) []Cashflow {
	events := []Cashflow{}
	startKey, endKey := start.Format("2006-01-02"), end.Format("2006-01-02")
	if i.AnnualBonus > 0 {
		if b := i.BonusSettings; b != nil {
			work, _ := time.Parse("2006-01-02", b.WorkStartDate)
			for year := start.Year(); year <= end.Year(); year++ {
				last := time.Date(year, time.Month(b.PayMonth)+1, 0, 0, 0, 0, 0, time.UTC).Day()
				pay := time.Date(year, time.Month(b.PayMonth), min(b.PayDay, last), 0, 0, 0, 0, time.UTC)
				key := pay.Format("2006-01-02")
				if key <= startKey || key > endKey {
					continue
				}
				earningYear := year - b.YearOffset
				a := time.Date(earningYear, 1, 1, 0, 0, 0, 0, time.UTC)
				z := a.AddDate(1, 0, 0)
				from := a
				if work.After(from) {
					from = work
				}
				ratio := math.Max(0, z.Sub(from).Hours()/z.Sub(a).Hours())
				amount := int64(math.Round(float64(i.AnnualBonus) * ratio * 0.9))
				if amount > 0 {
					events = append(events, Cashflow{Date: key, Amount: amount, Currency: "CNY", Kind: "bonus", Name: "年终奖", WorkRatio: ratio, EarningYear: earningYear})
				}
			}

		}
	}
	for _, g := range i.Options {
		for _, b := range g.Batches {
			if b.CashMode == "hold" {
				continue
			}
			key := b.VestDate
			if b.CashMode == "date" {
				key = b.CashDate
			}
			if key <= startKey || key > endKey {
				continue
			}
			amount := int64(math.Round(b.Quantity * math.Max(0, g.MarketPrice-g.StrikePrice) * 100 * (1 - g.TaxRate/100)))
			if amount > 0 {
				events = append(events, Cashflow{Date: key, Amount: amount, Currency: g.Currency, Kind: "option", Name: g.Name})
			}
		}
	}
	sort.SliceStable(events /* 按现金到账日期稳定排序，使同一天的收入保持原有顺序。 */, func(a, b int) bool { return events[a].Date < events[b].Date })
	return events
}

// ProjectRetirement 仅对现有资产按日复利，未来储蓄及现金流按到账日累计本金。
func ProjectRetirement(start time.Time, current, target int64, annualRate float64, monthly int64, events []Cashflow, rates map[string]float64) string {
	if target <= 0 {
		return ""
	}
	if current >= target {
		return start.Format("2006-01-02")
	}
	byDate := map[string]float64{}
	for _, e := range events {
		if rate, ok := rates[e.Currency]; ok && rate > 0 {
			byDate[e.Date] += float64(e.Amount) * rate
		}
	}
	existing := float64(current)
	contributions := 0.0
	growth := math.Pow(math.Max(0, 1+annualRate/100), 1/365.25)
	end := start.AddDate(100, 0, 0)
	for date := start.AddDate(0, 0, 1); !date.After(end); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		existing *= growth
		contributions += byDate[key]
		if date.AddDate(0, 0, 1).Month() != date.Month() {
			contributions += float64(monthly)
		}
		if existing+contributions >= float64(target) {
			return key
		}
	}
	return ""
}
