// Package service contains application use-cases. It deliberately has no HTTP
// types: callers supply values and receive domain-shaped results.
package service

import (
	"database/sql"
	"math"
	"time"
)

type Ledger struct{ db *sql.DB }

// NewLedger 创建账本服务，并注入数据库依赖。
func NewLedger(db *sql.DB) *Ledger { return &Ledger{db: db} }

type Income struct {
	MonthlySalary  int64  `json:"monthly_salary"`
	MonthlySavings int64  `json:"monthly_savings"`
	AnnualBonus    int64  `json:"annual_bonus"`
	UpdatedAt      string `json:"updated_at"`
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
	rows, err := tx.Query("SELECT amount,currency FROM assets WHERE user_id=?", userID)
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
		if !ok || rate <= 0 {
			rows.Close()
			return false, nil
		}
		total += int64(math.Round(float64(amount) * rate))
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	date := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	if _, err = tx.Exec(`INSERT INTO asset_history(user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at) VALUES(?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
ON CONFLICT(user_id,snapshot_date) DO UPDATE SET total_cny=excluded.total_cny,trigger=excluded.trigger,rate_date=excluded.rate_date,updated_at=CURRENT_TIMESTAMP`, userID, date, total, trigger, rateDate); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
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
	rows, err := s.db.Query(`SELECT id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at FROM (SELECT id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at FROM asset_history WHERE user_id=? ORDER BY snapshot_date DESC LIMIT ?) ORDER BY snapshot_date ASC`, userID, limit)
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

type Retirement struct {
	TargetCNY      int64            `json:"target_cny"`
	CurrentCNY     int64            `json:"current_cny"`
	Progress       float64          `json:"progress"`
	ProjectedYears *float64         `json:"projected_years"`
	AnnualRate     float64          `json:"annual_rate"`
	Items          []RetirementItem `json:"items"`
}
type RetirementItem struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// Retirement 汇总退休目标、当前资产和预计达成时间。
// 它计算目标进度，并模拟按月复利和计划储蓄后的达成时间。
// with the user's weighted asset rate and planned monthly savings.
func (s *Ledger) Retirement(userID int64) (Retirement, error) {
	var out Retirement
	err := s.db.QueryRow("SELECT target_cny FROM retirement_goals WHERE user_id=?", userID).Scan(&out.TargetCNY)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	rates := map[string]float64{"CNY": 1}
	rows, err := s.db.Query("SELECT currency,cny_rate FROM exchange_rates")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var currency string
		var rate float64
		if rows.Scan(&currency, &rate) == nil && rate > 0 {
			rates[currency] = rate
		}
	}
	rows.Close()
	goalItems, err := s.db.Query("SELECT id,name,category,amount,currency FROM retirement_goal_items WHERE user_id=? ORDER BY id", userID)
	if err != nil {
		return out, err
	}
	defer goalItems.Close()
	var itemTarget int64
	for goalItems.Next() {
		var item RetirementItem
		if goalItems.Scan(&item.ID, &item.Name, &item.Category, &item.Amount, &item.Currency) != nil {
			continue
		}
		out.Items = append(out.Items, item)
		if rate, ok := rates[item.Currency]; ok {
			itemTarget += int64(math.Round(float64(item.Amount) * rate))
		}
	}
	if len(out.Items) > 0 {
		out.TargetCNY = itemTarget
	}
	assets, err := s.db.Query("SELECT amount,currency,annual_rate FROM assets WHERE user_id=?", userID)
	if err != nil {
		return out, err
	}
	var weighted float64
	for assets.Next() {
		var amount int64
		var currency string
		var annual float64
		if assets.Scan(&amount, &currency, &annual) != nil {
			continue
		}
		rate, ok := rates[currency]
		if !ok {
			continue
		}
		value := float64(amount) * rate
		out.CurrentCNY += int64(math.Round(value))
		weighted += value * annual
	}
	assets.Close()
	if out.CurrentCNY > 0 {
		out.AnnualRate = weighted / float64(out.CurrentCNY)
	}
	if out.TargetCNY <= 0 {
		return out, nil
	}
	out.Progress = math.Min(100, float64(out.CurrentCNY)/float64(out.TargetCNY)*100)
	if out.CurrentCNY >= out.TargetCNY {
		years := 0.0
		out.ProjectedYears = &years
		return out, nil
	}
	income, err := s.Income(userID)
	if err != nil {
		return out, err
	}
	balance := float64(out.CurrentCNY)
	monthlyRate := out.AnnualRate / 100 / 12
	for month := 1; month <= 1200; month++ {
		balance = balance*(1+monthlyRate) + float64(income.MonthlySavings)
		if month%12 == 0 {
			balance += float64(income.AnnualBonus)
		}
		if balance >= float64(out.TargetCNY) {
			years := float64(month) / 12
			out.ProjectedYears = &years
			break
		}
	}
	return out, nil
}

// SaveRetirementTarget 保存兼容旧数据的单一退休金额目标。
func (s *Ledger) SaveRetirementTarget(userID, targetCNY int64) (Retirement, error) {
	_, err := s.db.Exec(`INSERT INTO retirement_goals(user_id,target_cny,updated_at) VALUES(?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET target_cny=excluded.target_cny,updated_at=CURRENT_TIMESTAMP`, userID, targetCNY)
	if err != nil {
		return Retirement{}, err
	}
	return s.Retirement(userID)
}

// AddRetirementItem 新增一项退休目标资产并返回最新汇总。
func (s *Ledger) AddRetirementItem(userID int64, name, category string, amount int64, currency string) (Retirement, error) {
	_, err := s.db.Exec("INSERT INTO retirement_goal_items(user_id,name,category,amount,currency) VALUES(?,?,?,?,?)", userID, name, category, amount, currency)
	if err != nil {
		return Retirement{}, err
	}
	return s.Retirement(userID)
}

// DeleteRetirementItem 删除用户的一项退休目标资产并返回最新汇总。
func (s *Ledger) DeleteRetirementItem(userID, id int64) (Retirement, error) {
	_, err := s.db.Exec("DELETE FROM retirement_goal_items WHERE id=? AND user_id=?", id, userID)
	if err != nil {
		return Retirement{}, err
	}
	return s.Retirement(userID)
}

// Income 读取用户的收入与储蓄规划设置。
// 缺少记录时返回空设置，而非交给 HTTP 层做特殊处理。
// valid empty setting, rather than an HTTP-layer special case.
func (s *Ledger) Income(userID int64) (Income, error) {
	var result Income
	err := s.db.QueryRow("SELECT monthly_salary, monthly_savings, annual_bonus, updated_at FROM income_settings WHERE user_id = ?", userID).
		Scan(&result.MonthlySalary, &result.MonthlySavings, &result.AnnualBonus, &result.UpdatedAt)
	if err == sql.ErrNoRows {
		return Income{}, nil
	}
	return result, err
}

// SaveIncome 保存用户的收入与储蓄规划设置。
// 持久化和 upsert 语义封装在业务层，不泄漏给 HTTP 处理器。
// do not leak into an HTTP handler.
func (s *Ledger) SaveIncome(userID, monthlySalary, monthlySavings, annualBonus int64) (Income, error) {
	_, err := s.db.Exec(`INSERT INTO income_settings(user_id, monthly_salary, monthly_savings, annual_bonus, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET monthly_salary=excluded.monthly_salary,
monthly_savings=excluded.monthly_savings, annual_bonus=excluded.annual_bonus,
updated_at=CURRENT_TIMESTAMP`, userID, monthlySalary, monthlySavings, annualBonus)
	if err != nil {
		return Income{}, err
	}
	return s.Income(userID)
}
