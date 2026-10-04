// 计划持久化逻辑：读取和更新收入设置、退休目标及目标明细。

package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ReadIncome 读取收入与薪酬计划，并生成以上海当前日期为起点的未来现金流。
func ReadIncome(q Queryer, userID int64) (Income, error) {
	var i Income
	var raw string
	err := q.QueryRow(`SELECT monthly_salary,monthly_savings,annual_bonus,updated_at,version,compensation FROM income_settings WHERE user_id=?`, userID).Scan(&i.MonthlySalary, &i.MonthlySavings, &i.AnnualBonus, &i.UpdatedAt, &i.Version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		i.Options = []OptionGrant{}
		i.Cashflows = []Cashflow{}
		i.ForecastAsOf = time.Now().In(planningZone).Format("2006-01-02")
		return i, nil
	}
	if err != nil {
		return i, err
	}
	var c compensationSettings
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return i, err
	}
	i.BonusSettings = c.BonusSettings
	i.Options = c.Options
	if i.Options == nil {
		i.Options = []OptionGrant{}
	}
	start := time.Now().In(planningZone)
	i.ForecastAsOf = start.Format("2006-01-02")
	i.Cashflows = CompensationEvents(i, start, start.AddDate(100, 0, 0))
	return i, nil
}

// IncomePatch 承载收入计划部分更新字段和版本，指针表示字段是否由请求提供。
type IncomePatch struct {
	BonusSettings                              *BonusSettings
	Options                                    *[]OptionGrant
	Version                                    *int64
	MonthlySalary, MonthlySavings, AnnualBonus *float64
}

// PatchIncome 合并已提供的收入字段，校验并持久化奖金和期权安排。
func PatchIncome(tx *sql.Tx, userID int64, before Income, x IncomePatch) (Income, error) {
	after := before
	if x.MonthlySalary == nil && x.MonthlySavings == nil && x.AnnualBonus == nil && x.BonusSettings == nil && x.Options == nil {
		return after, invalid("至少提供一个收入字段")
	}
	fields := [] /* 关联输入金额指针和待写入的整数金额字段。 */ struct {
		src *float64
		dst *int64
	}{{x.MonthlySalary, &after.MonthlySalary}, {x.MonthlySavings, &after.MonthlySavings}, {x.AnnualBonus, &after.AnnualBonus}}
	for _, f := range fields {
		if f.src == nil {
			continue
		}
		n, e := Money(*f.src, true)
		if e != nil {
			return after, e
		}
		*f.dst = n
	}
	if x.BonusSettings != nil {
		after.BonusSettings = x.BonusSettings
	}
	if x.Options != nil {
		after.Options = *x.Options
	}
	if after.AnnualBonus > 0 && after.BonusSettings == nil {
		return after, invalid("年终奖金额大于零时必须提供领取日期与入职日期")
	}
	if err := ValidateCompensation(after.BonusSettings, after.Options); err != nil {
		return after, err
	}
	raw, err := json.Marshal(compensationSettings{after.BonusSettings, after.Options})
	if err != nil {
		return after, err
	}
	_, err = tx.Exec(`INSERT INTO income_settings(user_id,monthly_salary,monthly_savings,annual_bonus,compensation) VALUES(?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET monthly_salary=excluded.monthly_salary,monthly_savings=excluded.monthly_savings,annual_bonus=excluded.annual_bonus,compensation=excluded.compensation,updated_at=CURRENT_TIMESTAMP`, userID, after.MonthlySalary, after.MonthlySavings, after.AnnualBonus, string(raw))
	if err != nil {
		return after, err
	}
	return ReadIncome(tx, userID)
}

// GoalItemInput 承载退休目标明细的新增或更新字段，以及用于冲突检查的版本。
type GoalItemInput struct {
	ID                       int64
	Version                  *int64
	Name, Category, Currency *string
	Amount                   *float64
}

// GetGoalItem 按用户和编号读取退休目标明细，不存在时返回业务错误。
func GetGoalItem(q Queryer, userID, id int64) (RetirementItem, error) {
	var x RetirementItem
	err := q.QueryRow(`SELECT id,name,category,amount,currency,version FROM retirement_goal_items WHERE id=? AND user_id=?`, id, userID).Scan(&x.ID, &x.Name, &x.Category, &x.Amount, &x.Currency, &x.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return x, &APIError{404, "not_found", "目标明细不存在"}
	}
	return x, err
}

// WriteGoalItem 合并并校验目标明细字段，在事务中创建或更新明细。
func WriteGoalItem(tx *sql.Tx, userID int64, before RetirementItem, x GoalItemInput, create bool) (RetirementItem, error) {
	after := before
	if x.Name == nil && x.Category == nil && x.Currency == nil && x.Amount == nil {
		return after, invalid("至少提供一个目标明细字段")
	}
	if x.Name != nil {
		after.Name = strings.TrimSpace(*x.Name)
	}
	if x.Category != nil {
		after.Category = strings.TrimSpace(*x.Category)
	}
	if x.Currency != nil {
		after.Currency = strings.ToUpper(strings.TrimSpace(*x.Currency))
	}
	if create && after.Currency == "" {
		after.Currency = "CNY"
	}
	if x.Amount != nil {
		n, e := Money(*x.Amount, false)
		if e != nil {
			return after, e
		}
		after.Amount = n
	}
	if after.Name == "" || len([]rune(after.Name)) > 120 || len([]rune(after.Category)) > 120 || after.Amount <= 0 || !Currency(after.Currency) {
		return after, invalid("目标明细名称、金额或币种无效")
	}
	if create {
		res, err := tx.Exec(`INSERT INTO retirement_goal_items(user_id,name,category,amount,currency) VALUES(?,?,?,?,?)`, userID, after.Name, after.Category, after.Amount, after.Currency)
		if err != nil {
			return after, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return after, err
		}
		return GetGoalItem(tx, userID, id)
	}
	_, err := tx.Exec(`UPDATE retirement_goal_items SET name=?,category=?,amount=?,currency=? WHERE id=? AND user_id=?`, after.Name, after.Category, after.Amount, after.Currency, before.ID, userID)
	if err != nil {
		return after, err
	}
	return GetGoalItem(tx, userID, before.ID)
}
