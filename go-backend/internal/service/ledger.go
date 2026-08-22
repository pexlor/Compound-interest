// Package service contains application use-cases. It deliberately has no HTTP
// types: callers supply values and receive domain-shaped results.
package service

import (
	"database/sql"
	"math"
	"time"
)

type Ledger struct{ db *sql.DB }

func NewLedger(db *sql.DB) *Ledger { return &Ledger{db: db} }

type Income struct {
	MonthlySalary  int64  `json:"monthly_salary"`
	MonthlySavings int64  `json:"monthly_savings"`
	UpdatedAt      string `json:"updated_at"`
}

// Snapshot persists the user's total using the latest complete local exchange-rate set.
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

// Retirement calculates the target progress and simulates monthly compounding
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
		if balance >= float64(out.TargetCNY) {
			years := float64(month) / 12
			out.ProjectedYears = &years
			break
		}
	}
	return out, nil
}

func (s *Ledger) SaveRetirementTarget(userID, targetCNY int64) (Retirement, error) {
	_, err := s.db.Exec(`INSERT INTO retirement_goals(user_id,target_cny,updated_at) VALUES(?,?,CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET target_cny=excluded.target_cny,updated_at=CURRENT_TIMESTAMP`, userID, targetCNY)
	if err != nil {
		return Retirement{}, err
	}
	return s.Retirement(userID)
}

func (s *Ledger) AddRetirementItem(userID int64, name, category string, amount int64, currency string) (Retirement, error) {
	_, err := s.db.Exec("INSERT INTO retirement_goal_items(user_id,name,category,amount,currency) VALUES(?,?,?,?,?)", userID, name, category, amount, currency)
	if err != nil {
		return Retirement{}, err
	}
	return s.Retirement(userID)
}
func (s *Ledger) DeleteRetirementItem(userID, id int64) (Retirement, error) {
	_, err := s.db.Exec("DELETE FROM retirement_goal_items WHERE id=? AND user_id=?", id, userID)
	if err != nil {
		return Retirement{}, err
	}
	return s.Retirement(userID)
}

// Income returns the user's persisted planning settings. A missing row is a
// valid empty setting, rather than an HTTP-layer special case.
func (s *Ledger) Income(userID int64) (Income, error) {
	var result Income
	err := s.db.QueryRow("SELECT monthly_salary, monthly_savings, updated_at FROM income_settings WHERE user_id = ?", userID).
		Scan(&result.MonthlySalary, &result.MonthlySavings, &result.UpdatedAt)
	if err == sql.ErrNoRows {
		return Income{}, nil
	}
	return result, err
}

// SaveIncome is the income-settings use case; persistence and upsert semantics
// do not leak into an HTTP handler.
func (s *Ledger) SaveIncome(userID, monthlySalary, monthlySavings int64) (Income, error) {
	_, err := s.db.Exec(`INSERT INTO income_settings(user_id, monthly_salary, monthly_savings, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET monthly_salary=excluded.monthly_salary,
monthly_savings=excluded.monthly_savings, updated_at=CURRENT_TIMESTAMP`, userID, monthlySalary, monthlySavings)
	if err != nil {
		return Income{}, err
	}
	return s.Income(userID)
}
