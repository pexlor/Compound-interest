// 预测执行准备：预计算路径共享因子，将币种查找移出内层循环，限制单次收益表内存。
package service

import (
	"context"
	"math"
	"sort"
)

// forecastGrowthBudget 将每次请求共享的收益因子表限制在8MiB，超出时继续直接计算。
const forecastGrowthBudget = 8 << 20

// forecastStep 保存检查点对应的日期、通胀目标及可被全部路径复用的收益因子。
type forecastStep struct {
	forecastCheckpoint
	Days            float64
	Target          float64
	Growth          []float64 // 按历史月份、资产及变动汇率顺序排列，同月连续读取。
	EventCurrencies []int
}

// forecastExecution 保存当前请求独立的币种索引、初始汇率和共享检查点，不缓存用户结果。
type forecastExecution struct {
	Steps           []forecastStep
	InitialFX       []float64
	AssetCurrencies []int
	FXCurrencies    []int
	FXChanges       [][]float64
	Columns         int
}

// prepareForecastExecution 保持原运算表达式，预计算日期及重复收益因子；内存不足时退回直接指数运算。
func prepareForecastExecution(ctx context.Context, in ForecastInput, o ForecastOptions, m forecastModel, checkpoints []forecastCheckpoint, budget int) (forecastExecution, error) {
	x := forecastExecution{Columns: len(in.Assets) + len(m.FX)}
	currencies := make([]string, 0, len(in.Rates))
	for currency := range in.Rates {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	currencyIndex := make(map[string]int, len(currencies))
	for _, currency := range currencies {
		currencyIndex[currency] = len(x.InitialFX)
		x.InitialFX = append(x.InitialFX, in.Rates[currency])
	}
	for _, a := range in.Assets {
		x.AssetCurrencies = append(x.AssetCurrencies, currencyIndex[a.Currency])
	}
	for _, currency := range currencies {
		if changes, ok := m.FX[currency]; ok {
			x.FXCurrencies = append(x.FXCurrencies, currencyIndex[currency])
			x.FXChanges = append(x.FXChanges, changes)
		}
	}
	months := len(m.Months)
	if months == 0 {
		months = 1
	}
	cached := map[float64][]float64{}
	remaining := budget / 8
	for _, p := range checkpoints {
		if err := ctx.Err(); err != nil {
			return x, err
		}
		days := p.Date.Sub(in.AsOf).Hours() / 24
		step := forecastStep{forecastCheckpoint: p, Days: days, Target: float64(in.Target) * math.Pow(1+o.Inflation/100, days/365.25)}
		for _, e := range p.Events {
			index, ok := currencyIndex[e.Currency]
			if !ok {
				index = -1
			}
			step.EventCurrencies = append(step.EventCurrencies, index)
		}
		growth, ok := cached[p.Fraction]
		if !ok && x.Columns > 0 && x.Columns <= remaining/months {
			growth = make([]float64, x.Columns*months)
			for month := 0; month < months; month++ {
				if err := ctx.Err(); err != nil {
					return x, err
				}
				row := growth[month*x.Columns : (month+1)*x.Columns]
				for i := range in.Assets {
					logReturn := m.Drift[i]
					if len(m.Months) > 0 {
						logReturn += m.Residual[i][month]
					}
					row[i] = math.Exp(logReturn * p.Fraction)
				}
				for i, changes := range x.FXChanges {
					row[len(in.Assets)+i] = math.Exp(changes[month] * p.Fraction)
				}
			}
			remaining -= len(growth)
			cached[p.Fraction] = growth
		}
		step.Growth = growth
		x.Steps = append(x.Steps, step)
	}
	return x, nil
}
