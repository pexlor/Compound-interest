// 投资组合汇总：按类别和币种统计人民币估值，并标记缺失或过期汇率。

package service

import (
	"math"
	"sort"
	"time"
)

// PortfolioGroup 表示按类别或币种聚合的人民币估值与占比；缺少汇率时金额可为空。
type PortfolioGroup struct {
	Name          string   `json:"name"`
	ValueCnyMinor *int64   `json:"valueCnyMinor"`
	Share         *float64 `json:"share"`
}

// PortfolioSummary 表示投资组合总额、资产分组、汇率日期和估值完整性信息。
type PortfolioSummary struct {
	TotalCnyMinor  *int64            `json:"totalCnyMinor"`
	Complete       bool              `json:"complete"`
	ByCategory     []PortfolioGroup  `json:"byCategory"`
	ByCurrency     []PortfolioGroup  `json:"byCurrency"`
	MissingRates   []string          `json:"missingRates"`
	RateDate       string            `json:"rateDate"`
	RateDates      map[string]string `json:"rateDates"`
	Stale          bool              `json:"stale"`
	ValuationBasis string            `json:"valuationBasis"`
	GeneratedAt    string            `json:"generatedAt"`
	AssetCount     int               `json:"assetCount"`
}

// PortfolioSummary 在同一个读事务中汇总用户持仓，返回类别、币种分布及汇率完整性信息。
func (s *Ledger) PortfolioSummary(userID int64) (PortfolioSummary, error) {
	// Use a single read transaction so asset rows and rates share one view.
	tx, err := s.db.Begin()
	if err != nil {
		return PortfolioSummary{}, err
	}
	defer tx.Rollback()
	assets, err := ListAssets(tx, userID, false)
	if err != nil {
		return PortfolioSummary{}, err
	}
	out := PortfolioSummary{Complete: true, ByCategory: []PortfolioGroup{}, ByCurrency: []PortfolioGroup{}, MissingRates: []string{}, RateDates: map[string]string{}, ValuationBasis: "stored_assets", GeneratedAt: time.Now().UTC().Format(time.RFC3339), AssetCount: len(assets)}
	rates := map[string]float64{"CNY": 1}
	updated := map[string]string{}
	rows, err := tx.Query(`SELECT currency,cny_rate,rate_date,updated_at FROM exchange_rates`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c, d, u string
		var v float64
		if err = rows.Scan(&c, &v, &d, &u); err != nil {
			rows.Close()
			return out, err
		}
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			rates[c] = v
			out.RateDates[c] = d
			updated[c] = u
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	categories, currencies := map[string]int64{}, map[string]int64{}
	missing := map[string]bool{}
	badCategory, badCurrency := map[string]bool{}, map[string]bool{}
	total := int64(0)
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	for _, a := range assets {
		categories[a.Category] += 0
		currencies[a.Currency] += 0
		rate, ok := rates[a.Currency]
		if !ok {
			missing[a.Currency] = true
			badCategory[a.Category] = true
			badCurrency[a.Currency] = true
			out.Complete = false
			out.Stale = true
			continue
		}
		converted := math.Round(float64(a.Amount) * rate)
		if converted < 0 || converted >= float64(math.MaxInt64) || math.IsNaN(converted) || math.IsInf(converted, 0) {
			return out, invalid("资产汇总超出可表示范围")
		}
		n := int64(converted)
		if total > math.MaxInt64-n {
			return out, invalid("资产汇总超出可表示范围")
		}
		total += n
		categories[a.Category] += n
		currencies[a.Currency] += n
		if a.Currency != "CNY" {
			d := out.RateDates[a.Currency]
			if out.RateDate == "" || d < out.RateDate {
				out.RateDate = d
			}
			if updated[a.Currency] < today+" 01:15:00" {
				out.Stale = true
			}
		}
	}
	if out.Complete {
		out.TotalCnyMinor = &total
	}
	for c := range missing {
		out.MissingRates = append(out.MissingRates, c)
	}
	sort.Strings(out.MissingRates)
	groups := /* 将分组金额转换为有序汇总列表，并对缺少汇率的分组保留空值。 */ func(values map[string]int64, bad map[string]bool) []PortfolioGroup {
		result := []PortfolioGroup{}
		for name, n := range values {
			g := PortfolioGroup{Name: name}
			value := n
			if !bad[name] {
				g.ValueCnyMinor = &value
				if out.Complete && total > 0 {
					share := float64(n) / float64(total)
					g.Share = &share
				}
			}
			result = append(result, g)
		}
		sort.Slice(result /* 按分组名称排序，保证汇总返回顺序稳定。 */, func(i, j int) bool { return result[i].Name < result[j].Name })
		return result
	}
	out.ByCategory = groups(categories, badCategory)
	out.ByCurrency = groups(currencies, badCurrency)
	return out, nil
}
