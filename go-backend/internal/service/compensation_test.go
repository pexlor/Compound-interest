// 收入预测测试：覆盖奖金年度、闰年折算、期权变现、输入校验与退休日期。

package service

import (
	"testing"
	"time"
)

// day 将测试日期字符串解析为 UTC 日期，便于构造固定时间的测试场景。
func day(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }

// Catches using the payment year rather than the earning year, or a 365-day divisor in a leap year.
// TestBonusEarningYearAndLeapProration 验证奖金所属年度、闰年在职比例及税后金额计算。
func TestBonusEarningYearAndLeapProration(t *testing.T) {
	i := Income{AnnualBonus: 3660000, BonusSettings: &BonusSettings{WorkStartDate: "2024-07-01", PayMonth: 2, PayDay: 28, YearOffset: 1}}
	events := CompensationEvents(i, day("2025-01-01"), day("2025-12-31"))
	if len(events) != 1 || events[0].Date != "2025-02-28" || events[0].Amount != 1656000 {
		t.Fatalf("events: %+v", events)
	}
	events = CompensationEvents(i, day("2026-01-01"), day("2026-12-31"))
	if len(events) != 1 || events[0].Amount != 3294000 {
		t.Fatalf("full year: %+v", events)
	}
}

// Catches accelerating delayed proceeds, counting held options, or replaying past proceeds.
// TestOptionsCashDateAndNetProceeds 验证期权按指定变现日期计入净收入，并排除暂不变现的批次。
func TestOptionsCashDateAndNetProceeds(t *testing.T) {
	i := Income{Options: []OptionGrant{{Name: "Grant", Currency: "USD", Quantity: 100, StrikePrice: 10, MarketPrice: 30, TaxRate: 20, Batches: []OptionBatch{
		{VestDate: "2025-01-01", Quantity: 25, CashMode: "date", CashDate: "2026-04-01"},
		{VestDate: "2026-03-01", Quantity: 25, CashMode: "hold"},
		{VestDate: "2026-05-01", Quantity: 25, CashMode: "immediate"},
		{VestDate: "2024-01-01", Quantity: 25, CashMode: "immediate"},
	}}}}
	events := CompensationEvents(i, day("2026-01-01"), day("2026-12-31"))
	if len(events) != 2 || events[0].Date != "2026-04-01" || events[1].Date != "2026-05-01" || events[0].Amount != 40000 || events[0].Currency != "USD" {
		t.Fatalf("events: %+v", events)
	}
}

// Catches allowing proceeds before vesting or assigning more shares than the grant.
// TestCompensationValidation 验证无效奖金日期、期权数量、变现安排和金额被拒绝。
func TestCompensationValidation(t *testing.T) {
	good := OptionGrant{Name: "Grant", Currency: "CNY", Quantity: 10, MarketPrice: 5, Batches: []OptionBatch{{VestDate: "2026-01-01", Quantity: 10, CashMode: "immediate"}}}
	if err := ValidateCompensation(nil, []OptionGrant{good}); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Batches = []OptionBatch{{VestDate: "2026-01-01", Quantity: 11, CashMode: "immediate"}}
	if ValidateCompensation(nil, []OptionGrant{bad}) == nil {
		t.Fatal("over allocation accepted")
	}
	bad.Batches = []OptionBatch{{VestDate: "2026-01-01", Quantity: 10, CashMode: "date", CashDate: "2025-01-01"}}
	if ValidateCompensation(nil, []OptionGrant{bad}) == nil {
		t.Fatal("cash before vest accepted")
	}
	if ValidateCompensation(&BonusSettings{WorkStartDate: "2026-01-01", PayMonth: 2, PayDay: 30}, nil) == nil {
		t.Fatal("invalid payment date accepted")
	}
}

// Catches moving income into a monthly bucket, or adding growth before it arrives.
// TestRetirementReachesTargetOnCashDate 验证退休预测在现金实际到账之日达到目标。
func TestRetirementReachesTargetOnCashDate(t *testing.T) {
	events := []Cashflow{{Date: "2026-04-15", Amount: 50000, Currency: "CNY", Kind: "option"}}
	date := ProjectRetirement(day("2026-01-01"), 10000, 60000, 0, 0, events, map[string]float64{"CNY": 1})
	if date != "2026-04-15" {
		t.Fatalf("date %s", date)
	}
	date = ProjectRetirement(day("2026-01-01"), 10000, 60000, 0, 0, events, map[string]float64{})
	if date != "" {
		t.Fatalf("missing rate gave date %s", date)
	}
}

// TestBonusWithoutScheduleDoesNotProduceProceeds 验证未配置领取日期的奖金不会生成预测现金流。
func TestBonusWithoutScheduleDoesNotProduceProceeds(t *testing.T) {
	events := CompensationEvents(Income{AnnualBonus: 100000}, day("2026-01-01"), day("2028-01-01"))
	if len(events) != 0 {
		t.Fatalf("undated bonus generated proceeds: %+v", events)
	}
}
