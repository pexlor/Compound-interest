// 资产业务逻辑：统一资产读取、金额校验、新增、部分更新和归档操作。

package service

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

// Asset 表示持久化资产记录，包含市值、持仓数量、定投设置、版本与归档状态。
type Asset struct {
	ID                 int64    `json:"id"`
	UserID             int64    `json:"user_id"`
	Name               string   `json:"name"`
	Category           string   `json:"category"`
	Code               *string  `json:"code"`
	Amount             int64    `json:"amount"`
	Quantity           *float64 `json:"quantity"`
	Currency           string   `json:"currency"`
	AnnualRate         float64  `json:"annual_rate"`
	InvestmentStrategy string   `json:"investment_strategy"`
	InvestmentAmount   *int64   `json:"investment_amount"`
	Note               string   `json:"note"`
	CreatedAt          string   `json:"created_at"`
	Version            int64    `json:"version"`
	ArchivedAt         *string  `json:"archived_at"`
}

const AssetColumns = "id,user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note,created_at,version,archived_at"

// Queryer 抽象数据库和事务共有的查询能力，供业务读取逻辑复用。
type Queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// ScanAsset 按照统一字段顺序将数据库行读取为资产对象。
func ScanAsset(s interface{ Scan(...any) error }) (Asset, error) {
	var a Asset
	err := s.Scan(&a.ID, &a.UserID, &a.Name, &a.Category, &a.Code, &a.Amount, &a.Quantity, &a.Currency, &a.AnnualRate, &a.InvestmentStrategy, &a.InvestmentAmount, &a.Note, &a.CreatedAt, &a.Version, &a.ArchivedAt)
	return a, err
}

// GetAsset 按用户和资产编号读取记录，并根据参数决定是否包含已归档资产。
func GetAsset(q Queryer, userID, id int64, includeArchived bool) (Asset, error) {
	query := "SELECT " + AssetColumns + " FROM assets WHERE user_id=? AND id=?"
	if !includeArchived {
		query += " AND archived_at IS NULL"
	}
	a, err := ScanAsset(q.QueryRow(query, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, &APIError{404, "not_found", "资产不存在"}
	}
	return a, err
}

// ListAssets 查询指定用户的资产列表，并处理归档过滤与结果扫描错误。
func ListAssets(q Queryer, userID int64, includeArchived bool) ([]Asset, error) {
	query := "SELECT " + AssetColumns + " FROM assets WHERE user_id=?"
	if !includeArchived {
		query += " AND archived_at IS NULL"
	}
	query += " ORDER BY id"
	rows, err := q.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Asset{}
	for rows.Next() {
		a, e := ScanAsset(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// Currency 判断币种是否属于系统支持的币种集合。
func Currency(s string) bool {
	switch s {
	case "CNY", "USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF":
		return true
	}
	return false
}

// invalid 构造带有请求错误状态码和说明的业务错误。
func invalid(message string) error { return &APIError{400, "invalid_request", message} }

// Money 校验主货币单位金额的范围，并将金额四舍五入转换为最小货币单位。
func Money(v float64, allowZero bool) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > float64(1<<53-1)/100 || (!allowZero && v == 0) {
		return 0, invalid("金额无效")
	}
	n := int64(math.Round(v * 100))
	if !allowZero && n == 0 {
		return 0, invalid("金额至少为 0.01")
	}
	return n, nil
}

// validAsset 校验资产类别、名称、币种、数量、收益率和定投设置。
func validAsset(a Asset) error {
	if strings.TrimSpace(a.Name) == "" || len([]rune(a.Name)) > 120 || len([]rune(a.Note)) > 10000 || !Currency(a.Currency) || a.Amount <= 0 {
		return invalid("资产名称、金额或币种无效")
	}
	switch a.Category {
	case "stock", "fund", "money", "deposit", "housing", "fixed":
	default:
		return invalid("资产类别无效")
	}
	if math.IsNaN(a.AnnualRate) || math.IsInf(a.AnnualRate, 0) || a.AnnualRate < -100 || a.AnnualRate > 1000 {
		return invalid("年化收益率无效")
	}
	if a.Category == "stock" || a.Category == "fund" {
		if a.Code == nil || strings.TrimSpace(*a.Code) == "" || len(*a.Code) > 32 || a.Quantity == nil || *a.Quantity <= 0 || math.IsNaN(*a.Quantity) || math.IsInf(*a.Quantity, 0) {
			return invalid("证券代码或持有数量无效")
		}
	} else if a.Quantity != nil {
		return invalid("此资产类别不支持持有数量")
	}
	switch a.InvestmentStrategy {
	case "none", "daily", "weekly", "monthly", "yearly":
	default:
		return invalid("定投周期无效")
	}
	if a.InvestmentStrategy != "none" && (a.Category != "fund" || a.InvestmentAmount == nil || *a.InvestmentAmount <= 0) {
		return invalid("定投仅支持基金，且必须提供有效金额")
	}
	return nil
}

// AssetCreate 承载新增资产输入，金额以主货币单位传入，可选数值通过指针表示。
type AssetCreate struct {
	Name, Category, Code, Currency, Note, InvestmentStrategy string
	Amount, AnnualRate                                       float64
	Quantity, InvestmentAmount                               *float64
}

// CreateAsset 在传入事务中校验并新增资产，返回数据库保存后的完整记录。
func CreateAsset(tx *sql.Tx, userID int64, x AssetCreate) (Asset, error) {
	amount, err := Money(x.Amount, false)
	if err != nil {
		return Asset{}, err
	}
	a := Asset{UserID: userID, Name: strings.TrimSpace(x.Name), Category: x.Category, Amount: amount, Quantity: x.Quantity, Currency: strings.ToUpper(strings.TrimSpace(x.Currency)), AnnualRate: x.AnnualRate, Note: strings.TrimSpace(x.Note), InvestmentStrategy: x.InvestmentStrategy}
	if a.Currency == "" {
		a.Currency = "CNY"
	}
	if a.InvestmentStrategy == "" {
		a.InvestmentStrategy = "none"
	}
	if code := strings.ToUpper(strings.TrimSpace(x.Code)); code != "" {
		a.Code = &code
	}
	if x.InvestmentAmount != nil {
		n, e := Money(*x.InvestmentAmount, false)
		if e != nil {
			return a, e
		}
		a.InvestmentAmount = &n
	}
	if a.InvestmentStrategy == "none" {
		a.InvestmentAmount = nil
	}
	if err = validAsset(a); err != nil {
		return a, err
	}
	res, err := tx.Exec(`INSERT INTO assets(user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, userID, a.Name, a.Category, a.Code, a.Amount, a.Quantity, a.Currency, a.AnnualRate, a.InvestmentStrategy, a.InvestmentAmount, a.Note)
	if err != nil {
		return a, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return a, err
	}
	return GetAsset(tx, userID, id, false)
}

// AssetPatch 承载资产部分更新输入，指针区分未提供字段与显式零值。
type AssetPatch struct {
	ID                 int64    `json:"id"`
	Version            *int64   `json:"version"`
	Name               *string  `json:"name"`
	Note               *string  `json:"note"`
	Amount             *float64 `json:"amount"`
	Quantity           *float64 `json:"quantity"`
	Currency           *string  `json:"currency"`
	AnnualRate         *float64 `json:"annualRate"`
	InvestmentStrategy *string  `json:"investmentStrategy"`
	InvestmentAmount   *float64 `json:"investmentAmount"`
}

// PatchAsset 合并已提供的修改字段，校验后在事务中保存资产并返回最新记录。
func PatchAsset(tx *sql.Tx, userID int64, before Asset, x AssetPatch) (Asset, error) {
	after := before
	if x.Name == nil && x.Note == nil && x.Amount == nil && x.Quantity == nil && x.Currency == nil && x.AnnualRate == nil && x.InvestmentStrategy == nil && x.InvestmentAmount == nil {
		return after, invalid("至少提供一个修改字段")
	}
	if x.Name != nil {
		after.Name = strings.TrimSpace(*x.Name)
	}
	if x.Note != nil {
		after.Note = strings.TrimSpace(*x.Note)
	}
	if x.Amount != nil {
		n, err := Money(*x.Amount, false)
		if err != nil {
			return after, err
		}
		after.Amount = n
	}
	if x.Quantity != nil {
		after.Quantity = x.Quantity
	}
	if x.Currency != nil {
		after.Currency = strings.ToUpper(strings.TrimSpace(*x.Currency))
	}
	if x.AnnualRate != nil {
		after.AnnualRate = *x.AnnualRate
	}
	if x.InvestmentStrategy != nil {
		after.InvestmentStrategy = *x.InvestmentStrategy
	}
	if x.InvestmentAmount != nil {
		n, err := Money(*x.InvestmentAmount, false)
		if err != nil {
			return after, err
		}
		after.InvestmentAmount = &n
	}
	if after.InvestmentStrategy == "none" {
		after.InvestmentAmount = nil
	}
	if err := validAsset(after); err != nil {
		return after, err
	}
	_, err := tx.Exec(`UPDATE assets SET name=?,note=?,amount=?,quantity=?,currency=?,annual_rate=?,investment_strategy=?,investment_amount=? WHERE id=? AND user_id=?`, after.Name, after.Note, after.Amount, after.Quantity, after.Currency, after.AnnualRate, after.InvestmentStrategy, after.InvestmentAmount, before.ID, userID)
	if err != nil {
		return after, err
	}
	return GetAsset(tx, userID, before.ID, false)
}

// ArchiveAsset 设置资产归档时间，并返回包含归档状态的记录。
func ArchiveAsset(tx *sql.Tx, userID, id int64) (Asset, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := tx.Exec(`UPDATE assets SET archived_at=? WHERE id=? AND user_id=? AND archived_at IS NULL`, now, id, userID)
	if err != nil {
		return Asset{}, err
	}
	return GetAsset(tx, userID, id, true)
}
