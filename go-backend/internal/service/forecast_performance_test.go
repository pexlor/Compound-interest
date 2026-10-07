// 性能回归：用混合币种、到账日期和原始循环核对优化结果及分配开销。
package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// performanceForecastInput 构造风险资产、固定利率、两种外币及月内多笔收入的合成快照。
func performanceForecastInput(count int) ForecastInput {
	h := monthlyFixture(.05)
	in := ForecastInput{AsOf: day("2025-02-05"), Rates: map[string]float64{"CNY": 1, "USD": 7.1, "HKD": .91}, History: map[string][]PriceObservation{"cn_equity": h, "us_equity": h, "hk_equity": h, "own": h}, FX: map[string][]PriceObservation{"USD": monthlyFixture(.01), "HKD": monthlyFixture(.002)}, MonthlySavings: 10000, Target: 10000000}
	for i := 0; i < count; i++ {
		a := ForecastHolding{ID: int64(i + 1), Name: fmt.Sprintf("资产%d", i+1), Category: "stock", Currency: "CNY", Amount: 100000, Class: "cn_equity", Key: "own"}
		switch i % 5 {
		case 1:
			a.Currency = "USD"
			a.Class = "us_equity"
		case 2:
			a.Currency = "HKD"
			a.Class = "hk_equity"
		case 3:
			a.Category = "deposit"
			a.AnnualRate = 2
		case 4:
			a.Category = "fixed"
		}
		in.Assets = append(in.Assets, a)
	}
	in.Events = []Cashflow{{Date: "2025-02-05", Currency: "USD", Amount: 999999}, {Date: "2025-02-15", Currency: "USD", Amount: 30000}, {Date: "2025-02-28", Currency: "HKD", Amount: 20000}, {Date: "2025-03-01", Currency: "CNY", Amount: 10000}, {Date: "2028-02-29", Currency: "USD", Amount: 30000}}
	return in
}

// TestForecastOptimizedMatchesReference 防止收益表、币种索引或随机源复用改变原有逐分输出。
func TestForecastOptimizedMatchesReference(t *testing.T) {
	for _, at := range []string{"2025-02-05", "2025-02-28", "2024-02-29"} {
		for _, years := range []int{1, 10, 30} {
			t.Run(fmt.Sprintf("%s/%d", at, years), func(t *testing.T) {
				in := performanceForecastInput(10)
				in.AsOf = day(at)
				o := ForecastOptions{Years: years, Inflation: 2, Paths: 128}
				want, e := simulateForecastReference(in, o)
				if e != nil {
					t.Fatal(e)
				}
				got, e := SimulateForecast(in, o)
				if e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("optimized result differs from reference\ngot=%+v\nwant=%+v", got, want)
				}
			})
		}
	}
	for _, count := range []int{0, 1} {
		t.Run(fmt.Sprintf("fixed-only/%d", count), func(t *testing.T) {
			in := ForecastInput{AsOf: day("2026-01-31"), Rates: map[string]float64{"CNY": 1}, MonthlySavings: 10000}
			if count > 0 {
				in.Assets = []ForecastHolding{{Category: "deposit", Currency: "CNY", Amount: 100000, AnnualRate: 3}}
			}
			o := ForecastOptions{Years: 1, Paths: 10}
			want, e := simulateForecastReference(in, o)
			if e != nil {
				t.Fatal(e)
			}
			got, e := SimulateForecast(in, o)
			if e != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("deterministic mismatch: %v", e)
			}
		})
	}
}

// TestForecastAllocationsDoNotScaleWithPaths 限制完整5000路径的分配次数，防止每条路径重新分配随机源和余额。
func TestForecastAllocationsDoNotScaleWithPaths(t *testing.T) {
	in := performanceForecastInput(10)
	o := DefaultForecastOptions()
	var err error
	allocations := testing.AllocsPerRun(1, func() { _, err = SimulateForecast(in, o) })
	if err != nil {
		t.Fatal(err)
	}
	if allocations > 3000 {
		t.Fatalf("5000 paths allocated %.0f objects; want <=3000", allocations)
	}
}

// TestForecastCancelledInputStops 验证预计算及路径执行仍遵守请求取消。
func TestForecastCancelledInputStops(t *testing.T) {
	in := performanceForecastInput(10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in.Context = ctx
	if _, err := SimulateForecast(in, DefaultForecastOptions()); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

// BenchmarkForecastPerformance 比较完整5000路径下的原始与优化循环，输入准备不计入耗时。
func BenchmarkForecastPerformance(b *testing.B) {
	for _, years := range []int{1, 10, 30} {
		for _, reference := range []bool{true, false} {
			b.Run(fmt.Sprintf("years%d/reference%t", years, reference), func(b *testing.B) {
				in := performanceForecastInput(10)
				o := DefaultForecastOptions()
				o.Years = years
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var err error
					if reference {
						_, err = simulateForecastReference(in, o)
					} else {
						_, err = SimulateForecast(in, o)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// TestForecastGrowthBudgetFallbackMatchesReference 验证大量资产超过8MiB收益表预算时直接计算仍保持同一输出。
func TestForecastGrowthBudgetFallbackMatchesReference(t *testing.T) {
	in := performanceForecastInput(3000)
	o := ForecastOptions{Years: 1, Inflation: 2, Paths: 3}
	want, err := simulateForecastReference(in, o)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SimulateForecast(in, o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("growth budget fallback changed forecast")
	}
}

// TestForecastDefaultPathsMatchReference 验证默认5000路径、只有外币收入和短历史回退也保持结果一致。
func TestForecastDefaultPathsMatchReference(t *testing.T) {
	for _, kind := range []string{"mixed", "fx-only", "short-history"} {
		t.Run(kind, func(t *testing.T) {
			in := performanceForecastInput(10)
			if kind == "fx-only" {
				in.Assets = nil
			}
			if kind == "short-history" {
				in.History["own"] = in.History["own"][100:]
			}
			o := DefaultForecastOptions()
			o.Years = 1
			want, err := simulateForecastReference(in, o)
			if err != nil {
				t.Fatal(err)
			}
			got, err := SimulateForecast(in, o)
			if err != nil {
				t.Fatal(err)
			}
			if got.Paths != 5000 || !reflect.DeepEqual(got, want) {
				t.Fatalf("default paths differ: %s", kind)
			}
		})
	}
}

// TestForecastGrowthTablesRespectMemoryBudget 验证不同检查点共享的收益表实际占用不超预算，预算不足时保留直接计算路径。
func TestForecastGrowthTablesRespectMemoryBudget(t *testing.T) {
	in := performanceForecastInput(10)
	o := DefaultForecastOptions()
	o.Years = 1
	m, err := prepareForecastModel(in, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{0, 1, 15000, forecastGrowthBudget} {
		t.Run(fmt.Sprintf("bytes%d", budget), func(t *testing.T) {
			x, err := prepareForecastExecution(context.Background(), in, o, m, forecastCheckpoints(in, o), budget)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[*float64]bool{}
			used := 0
			fallback := false
			for _, p := range x.Steps {
				if len(p.Growth) == 0 {
					fallback = true
					continue
				}
				first := &p.Growth[0]
				if !seen[first] {
					seen[first] = true
					used += len(p.Growth) * 8
				}
			}
			if used > budget {
				t.Fatalf("growth tables use %d bytes, budget %d", used, budget)
			}
			if budget < 15000 && !fallback {
				t.Fatal("small budget did not fall back to direct computation")
			}
			if budget == forecastGrowthBudget && used == 0 {
				t.Fatal("sufficient budget did not prepare growth tables")
			}
		})
	}
}
