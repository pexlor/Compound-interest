// 预测历史测试：识别累计净值误用、分红漏算、月份缺口和未来数据泄漏。
package service

import (
	"math"
	"testing"
)

// TestFundTotalReturnIgnoresOldDividends 验证区间前分红不稀释区间收益，区间内分红按再投资计算。
func TestFundTotalReturnIgnoresOldDividends(t *testing.T) {
	rows := []PriceObservation{{Date: "2020-01-01", Price: 1, TotalPrice: 1.5}, {Date: "2020-01-02", Price: 1.1, TotalPrice: 1.6}, {Date: "2020-01-03", Price: 1, TotalPrice: 1.6}}
	got, err := FundTotalReturn(rows)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got[1].TotalPrice-1.1) > 1e-9 || math.Abs(got[2].TotalPrice-1.1) > 1e-9 {
		t.Fatalf("returns %+v", got)
	}
	rows[2].TotalPrice = 1
	if _, err = FundTotalReturn(rows); err == nil {
		t.Fatal("ambiguous split accepted")
	}
}

// TestMonthlyReturnsNeedsConsecutiveCompletedMonths 验证缺失月份不拼接，当前未结束月份与未来数据不参与估计。
func TestMonthlyReturnsNeedsConsecutiveCompletedMonths(t *testing.T) {
	rows := []PriceObservation{{Date: "2024-12-31", TotalPrice: 100}, {Date: "2025-01-31", TotalPrice: 110}, {Date: "2025-03-31", TotalPrice: 121}, {Date: "2025-04-30", TotalPrice: 133.1}, {Date: "2025-05-31", TotalPrice: 200}}
	got, err := MonthlyReturns(rows, day("2025-05-10"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || math.Abs(got["2025-01"]-math.Log(1.1)) > 1e-9 || math.Abs(got["2025-04"]-math.Log(1.1)) > 1e-9 {
		t.Fatal(got)
	}
	if _, ok := got["2025-03"]; ok {
		t.Fatal("gap bridged")
	}
}

// TestEstimateDriftShrinksYoungHistory 验证不足三年忽略超额收益，长样本仍折减单项高收益。
func TestEstimateDriftShrinksYoungHistory(t *testing.T) {
	own, base := map[string]float64{}, map[string]float64{}
	for i := 0; i < 120; i++ {
		key := day("2010-01-01").AddDate(0, i, 0).Format("2006-01")
		base[key] = .005
		if i < 35 {
			own[key] = .1
		}
	}
	drift, n := EstimateDrift(own, base)
	if n != 35 || math.Abs(drift-.005) > 1e-9 {
		t.Fatalf("short drift=%f n=%d", drift, n)
	}
	for key := range base {
		own[key] = .015
	}
	drift, n = EstimateDrift(own, base)
	if n != 120 || math.Abs(drift-.01) > 1e-9 {
		t.Fatalf("long drift=%f n=%d", drift, n)
	}
}
