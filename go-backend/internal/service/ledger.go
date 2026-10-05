// 账本服务：管理汇率缓存、每日资产快照、历史记录、收入与退休计划。
// Package service contains application use-cases. It deliberately has no HTTP
// types: callers supply values and receive domain-shaped results.
package service

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// Ledger 持有数据库依赖，为资产账本、历史快照和退休计划提供业务服务。
type Ledger struct{ db *sql.DB }

// NewLedger 创建账本服务，并注入数据库依赖。
func NewLedger(db *sql.DB) *Ledger { return &Ledger{db: db} }

// Income 表示收入设置及据此生成的未来现金流和预测基准日期。
type Income struct {
	BonusSettings  *BonusSettings `json:"bonus_settings"`
	Options        []OptionGrant  `json:"options"`
	Cashflows      []Cashflow     `json:"cashflows"`
	ForecastAsOf   string         `json:"forecast_as_of"`
	Version        int64          `json:"version"`
	MonthlySalary  int64          `json:"monthly_salary"`
	MonthlySavings int64          `json:"monthly_savings"`
	AnnualBonus    int64          `json:"annual_bonus"`
	UpdatedAt      string         `json:"updated_at"`
}

// Snapshot 按当前汇率汇总资产并写入当天的历史快照。
// 它使用本地最新且完整的汇率集持久化用户资产总额。
// It is deliberately transactional so an asset mutation cannot leave a partial history row.
func (s *Ledger) Snapshot(userID int64, trigger string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	ok, err := SnapshotTx(tx, userID, trigger)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return ok, nil
}

// SnapshotTx joins a business mutation's transaction so the daily observation
// cannot outlive a rolled-back asset change.
// SnapshotTx 在业务变更事务中记录当天资产快照，使资产写入和快照一起提交或回滚。
func SnapshotTx(tx *sql.Tx, userID int64, trigger string) (bool, error) {
	rows, err := tx.Query("SELECT amount,currency FROM assets WHERE user_id=? AND archived_at IS NULL", userID)
	if err != nil {
		return false, err
	}
	rates := map[string]float64{"CNY": 1}
	rateDate := ""
	r, err := tx.Query("SELECT currency,cny_rate,rate_date FROM exchange_rates")
	if err != nil {
		rows.Close()
		return false, err
	}
	for r.Next() {
		var c, d string
		var rate float64
		if err := r.Scan(&c, &rate, &d); err != nil {
			r.Close()
			rows.Close()
			return false, err
		}
		rates[c] = rate
		if d > rateDate {
			rateDate = d
		}
	}
	r.Close()
	var total int64
	for rows.Next() {
		var amount int64
		var currency string
		if err := rows.Scan(&amount, &currency); err != nil {
			rows.Close()
			return false, err
		}
		rate, ok := rates[currency]
		if !ok || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			rows.Close()
			return false, nil
		}
		converted := math.Round(float64(amount) * rate)
		if converted < 0 || converted >= float64(math.MaxInt64) || math.IsNaN(converted) || math.IsInf(converted, 0) {
			rows.Close()
			return false, invalid("资产汇总超出可表示范围")
		}
		n := int64(converted)
		if total > math.MaxInt64-n {
			rows.Close()
			return false, invalid("资产汇总超出可表示范围")
		}
		total += n
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	date := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	if _, err = tx.Exec(`INSERT INTO asset_history(user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at) VALUES(?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,snapshot_date) DO UPDATE SET total_cny=excluded.total_cny,trigger=excluded.trigger,rate_date=excluded.rate_date,updated_at=CURRENT_TIMESTAMP`, userID, date, total, trigger, rateDate); err != nil {
		return false, err
	}
	return true, nil
}

// LatestRates 读取本地缓存的各币种兑人民币汇率及最新日期。
func (s *Ledger) LatestRates() (map[string]float64, string, error) {
	rates := map[string]float64{"CNY": 1}
	date := ""
	rows, err := s.db.Query("SELECT currency,cny_rate,rate_date FROM exchange_rates")
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var currency, rateDate string
		var rate float64
		if err := rows.Scan(&currency, &rate, &rateDate); err != nil {
			return nil, "", err
		}
		rates[currency] = rate
		if rateDate > date {
			date = rateDate
		}
	}
	return rates, date, rows.Err()
}

// RatesCachedSince reports whether the complete non-CNY rate cache was written
// at or after since.  The market's quote date is intentionally not used here:
// it does not advance on weekends and holidays even when the cache was just
// refreshed.
// RatesCachedSince 检查每种预期外币的缓存更新时间是否均不早于指定时刻。
// 使用抓取时间而非行情日期，避免周末和节假日的旧行情日期误判新缓存。
func (s *Ledger) RatesCachedSince(since time.Time, expectedCurrencies int) (bool, error) {
	var count int
	var oldest sql.NullString
	if err := s.db.QueryRow(`SELECT COUNT(*), MIN(updated_at) FROM exchange_rates`).Scan(&count, &oldest); err != nil {
		return false, err
	}
	if count != expectedCurrencies || !oldest.Valid {
		return false, nil
	}
	updated, err := time.ParseInLocation("2006-01-02 15:04:05", oldest.String, time.UTC)
	if err != nil {
		return false, err
	}
	return !updated.Before(since.UTC()), nil
}

// SnapshotAll writes one daily snapshot per registered user.  A failure for
// one account is returned while the other accounts are still attempted.
// SnapshotAll 尝试为每个注册用户写入当天资产快照，并返回遇到的首个错误。
func (s *Ledger) SnapshotAll(trigger string) error {
	rows, err := s.db.Query("SELECT id FROM users ORDER BY id")
	if err != nil {
		return err
	}
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var firstErr error
	for _, userID := range userIDs {
		if _, err := s.Snapshot(userID, trigger); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SnapshotMissingTodayAll fills in today's snapshot for accounts that do not
// have one yet.  It lets a server started after the scheduled run recover
// without replacing a snapshot already captured earlier in the day.
// SnapshotMissingTodayAll 补录尚无当天快照的用户，保留当天已经记录的快照。
func (s *Ledger) SnapshotMissingTodayAll(trigger string) error {
	date := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT u.id FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM asset_history h WHERE h.user_id=u.id AND h.snapshot_date=?)
		ORDER BY u.id`, date)
	if err != nil {
		return err
	}
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var firstErr error
	for _, userID := range userIDs {
		if _, err := s.Snapshot(userID, trigger); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// History 按时间顺序读取指定数量的资产历史快照。
func (s *Ledger) History(userID int64, limit int) ([]map[string]any, error) {
	return s.HistoryRange(userID, limit, "", "")
}

// HistoryRange 按可选起止日期查询最近的资产快照，并以日期升序返回。
func (s *Ledger) HistoryRange(userID int64, limit int, from, to string) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at FROM (SELECT id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at FROM asset_history WHERE user_id=? AND (?='' OR snapshot_date>=?) AND (?='' OR snapshot_date<=?) ORDER BY snapshot_date DESC LIMIT ?) ORDER BY snapshot_date ASC`, userID, from, from, to, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, uid, total int64
		var date, trigger, created, updated string
		var rate sql.NullString
		if err := rows.Scan(&id, &uid, &date, &total, &trigger, &rate, &created, &updated); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": id, "user_id": uid, "snapshot_date": date, "total_cny": total, "trigger": trigger, "rate_date": nullable(rate), "created_at": created, "updated_at": updated})
	}
	return result, rows.Err()
}

// nullable 将可空字符串转换为便于 JSON 序列化的值。
func nullable(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

// Retirement 表示退休目标、当前资产、进度、预计达标时间和目标明细。
type Retirement struct {
	Complete          bool             `json:"complete"`
	LiquidCNY         int64            `json:"liquid_cny"`
	ForecastState     string           `json:"forecast_state"`
	ProjectedDate     string           `json:"projected_date,omitempty"`
	MissingCurrencies []string         `json:"missing_currencies,omitempty"`
	TargetCNY         int64            `json:"target_cny"`
	CurrentCNY        int64            `json:"current_cny"`
	Progress          float64          `json:"progress"`
	ProjectedYears    *float64         `json:"projected_years"`
	AnnualRate        float64          `json:"annual_rate"`
	Items             []RetirementItem `json:"items"`
}

// RetirementItem 表示一项退休目标支出或资产需求，记录金额、币种与版本。
type RetirementItem struct {
	Version  int64  `json:"version"`
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// Retirement 汇总退休目标、当前资产和预计达成时间。
// 它复用联合月度预测引擎，按现金日期累计未来本金。
func (s *Ledger) Retirement(userID int64) (Retirement, error) {
	now := time.Now().In(planningZone)
	at := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	result, _, _, err := s.Forecast(context.Background(), userID, DefaultForecastOptions(), at)
	return result.Retirement, err
}

// RetirementFor 读取目标、持仓与汇率，仅返回事务中的当前进度；模拟在释放事务后执行。
func RetirementFor(q Queryer, userID int64) (Retirement, error) {
	out := Retirement{Items: []RetirementItem{}, Complete: true, MissingCurrencies: []string{}}
	rates := map[string]float64{"CNY": 1}
	rows, err := q.Query("SELECT currency,cny_rate FROM exchange_rates")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c string
		var r float64
		if err = rows.Scan(&c, &r); err != nil {
			rows.Close()
			return out, err
		}
		if positiveFinite(r) {
			rates[c] = r
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	// converted 校验汇率及金额范围，缺失时标记不完整，防止产生错误达标时间。
	converted := func(amount int64, c string) (int64, error) {
		r, ok := rates[c]
		if !ok {
			out.Complete = false
			if !seen[c] {
				out.MissingCurrencies = append(out.MissingCurrencies, c)
				seen[c] = true
			}
			return 0, nil
		}
		v := math.Round(float64(amount) * r)
		if v < 0 || v > 9e15 {
			return 0, invalid("退休金额超出范围")
		}
		return int64(v), nil
	}
	goals, err := q.Query("SELECT id,name,category,amount,currency,version FROM retirement_goal_items WHERE user_id=? ORDER BY id", userID)
	if err != nil {
		return out, err
	}
	for goals.Next() {
		var item RetirementItem
		if err = goals.Scan(&item.ID, &item.Name, &item.Category, &item.Amount, &item.Currency, &item.Version); err != nil {
			goals.Close()
			return out, err
		}
		out.Items = append(out.Items, item)
		v, e := converted(item.Amount, item.Currency)
		if e != nil {
			goals.Close()
			return out, e
		}
		out.TargetCNY += v
	}
	err = goals.Err()
	goals.Close()
	if err != nil {
		return out, err
	}
	assets, err := ListAssets(q, userID, false)
	if err != nil {
		return out, err
	}
	for _, a := range assets {
		v, e := converted(a.Amount, a.Currency)
		if e != nil {
			return out, e
		}
		out.CurrentCNY += v
		if liquidHolding(ForecastHolding{Category: a.Category}, false) {
			out.LiquidCNY += v
		}
	}
	if out.TargetCNY > 0 && out.Complete {
		out.Progress = math.Min(100, float64(out.LiquidCNY)/float64(out.TargetCNY)*100)
	}
	return out, nil
}

// Income 读取用户的收入与储蓄规划设置。
// 缺少记录时返回空设置，而非交给 HTTP 层做特殊处理。
// valid empty setting, rather than an HTTP-layer special case.
func (s *Ledger) Income(userID int64) (Income, error) { return ReadIncome(s.db, userID) }
