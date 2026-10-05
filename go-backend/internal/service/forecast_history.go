// 预测特征：恢复基金总收益、构造完整月收益并折减短历史的超额收益。
package service

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// PriceObservation 表示实际日期的价格、累计净值或复权价及货币基金每万份收益。
type PriceObservation struct {
	Date                      string
	Price, TotalPrice, Income float64
}

// positiveFinite 判断价格是否为有限正数。
func positiveFinite(n float64) bool { return n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }

// FundTotalReturn 从相邻净值的累计分红差恢复红利再投资指数，拒绝无法识别的净值拆分。
func FundTotalReturn(rows []PriceObservation) ([]PriceObservation, error) {
	out := append([]PriceObservation(nil), rows...)
	index := 1.0
	for i, p := range rows {
		if !positiveFinite(p.Price) || !positiveFinite(p.TotalPrice) {
			return nil, fmt.Errorf("基金净值无效")
		}
		if i > 0 {
			prev := rows[i-1]
			dividend := (p.TotalPrice - p.Price) - (prev.TotalPrice - prev.Price)
			if dividend < -0.0002 {
				return nil, fmt.Errorf("基金分红或拆分无法可靠还原")
			}
			index *= (p.Price + math.Max(0, dividend)) / prev.Price
		}
		out[i].TotalPrice = index
	}
	return out, nil
}

// MonthlyReturns 仅保留起点前十年内相邻完整月份的对数收益，不连接缺失月份。
func MonthlyReturns(rows []PriceObservation, asOf time.Time) (map[string]float64, error) {
	last := map[string]PriceObservation{}
	cutoff := time.Date(asOf.Year(), asOf.Month(), 1, 0, 0, 0, 0, time.UTC)
	from := cutoff.AddDate(-10, 0, -10)
	for _, p := range rows {
		at, err := time.Parse("2006-01-02", p.Date)
		if err != nil || !positiveFinite(p.TotalPrice) {
			return nil, fmt.Errorf("历史价格或日期无效")
		}
		if !at.Before(cutoff) || at.Before(from) {
			continue
		}
		key := at.Format("2006-01")
		if p.Date > last[key].Date {
			last[key] = p
		}
	}
	result := map[string]float64{}
	for key, p := range last {
		month, _ := time.Parse("2006-01", key)
		prev, ok := last[month.AddDate(0, -1, 0).Format("2006-01")]
		if !ok {
			continue
		}
		// 月末仍缺少超过七天的行情不能当作完整月份。
		end := month.AddDate(0, 1, -1)
		at, _ := time.Parse("2006-01-02", p.Date)
		before, _ := time.Parse("2006-01-02", prev.Date)
		prevEnd := month.AddDate(0, 0, -1)
		if end.Sub(at) > 7*24*time.Hour || prevEnd.Sub(before) > 7*24*time.Hour {
			continue
		}
		result[key] = math.Log(p.TotalPrice / prev.TotalPrice)
	}
	return result, nil
}

// meanReturns 返回月对数收益均值，空样本为零且由调用者检查覆盖。
func meanReturns(values map[string]float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	if len(values) == 0 {
		return 0
	}
	return sum / float64(len(values))
}

// EstimateDrift 用长期基准折减共同月份超额收益，不足36个月时只采用基准中心。
func EstimateDrift(own, benchmark map[string]float64) (float64, int) {
	excess := 0.0
	count := 0
	for key, v := range own {
		if b, ok := benchmark[key]; ok {
			excess += v - b
			count++
		}
	}
	drift := meanReturns(benchmark)
	if count >= 36 {
		drift += math.Min(.5, float64(count)/float64(count+60)) * excess / float64(count)
	}
	return drift, count
}

// commonMonths 取各序列共同有效月份并排序，片段抽样另行核对连续性。
func commonMonths(series []map[string]float64) []string {
	if len(series) == 0 {
		return nil
	}
	result := []string{}
	for key := range series[0] {
		valid := true
		for _, s := range series[1:] {
			if _, ok := s[key]; !ok {
				valid = false
				break
			}
		}
		if valid {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}
