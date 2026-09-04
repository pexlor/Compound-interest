package httpapi

import (
	"database/sql"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// scanAsset 将数据库查询结果扫描为资产实体。
func scanAsset(s interface{ Scan(...any) error }) (asset, error) {
	var x asset
	e := s.Scan(&x.ID, &x.UserID, &x.Name, &x.Category, &x.Code, &x.Amount, &x.Quantity, &x.Currency, &x.AnnualRate, &x.InvestmentStrategy, &x.InvestmentAmount, &x.Note, &x.CreatedAt)
	return x, e
}

const assetCols = "id,user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note,created_at"

// listAssets 读取用户的全部资产。
func (a *app) listAssets(id int64) ([]asset, error) {
	rows, e := a.db.Query("SELECT "+assetCols+" FROM assets WHERE user_id=? ORDER BY id", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	v := []asset{}
	for rows.Next() {
		x, e := scanAsset(rows)
		if e != nil {
			return nil, e
		}
		v = append(v, x)
	}
	return v, rows.Err()
}

// money 校验金额并转换为以分为单位的整数。
func money(n float64) (int64, bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > maxMoney {
		return 0, false
	}
	return int64(math.Round(n * 100)), true
}

// currency 判断币种是否在系统支持范围内。
func currency(s string) bool {
	switch s {
	case "CNY", "USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF":
		return true
	}
	return false
}

// assets 分派资产的查询、新增、更新和删除请求。
func (a *app) assets(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	switch r.Method {
	case "GET":
		v, e := a.listAssets(u.ID)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		out(w, 200, map[string]any{"assets": v})
	case "POST":
		a.addAsset(w, r, u)
	case "DELETE":
		id, e := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if e != nil || id < 1 {
			fail(w, 400, "无效资产")
			return
		}
		res, e := a.db.Exec("DELETE FROM assets WHERE id=? AND user_id=?", id, u.ID)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "资产不存在")
		} else {
			snapshot, snapshotErr := a.ledger.Snapshot(u.ID, "asset_change")
			if snapshotErr != nil {
				snapshot = false
			}
			out(w, 200, map[string]any{"ok": true, "snapshot": snapshot})
		}
	case "PATCH":
		a.patchAsset(w, r, u)
	default:
		fail(w, 405, "方法不允许")
	}
}

// addAsset 校验并创建一项资产，同时记录历史快照。
func (a *app) addAsset(w http.ResponseWriter, r *http.Request, u *user) {
	var x struct {
		Name, Category, Code, Currency, Note, InvestmentStrategy string
		Amount, Quantity, AnnualRate, InvestmentAmount           float64
	}
	if body(r, &x) != nil {
		fail(w, 400, "请求无效")
		return
	}
	x.Name = strings.TrimSpace(x.Name)
	x.Category = strings.TrimSpace(x.Category)
	x.Currency = strings.ToUpper(strings.TrimSpace(x.Currency))
	if x.Currency == "" {
		x.Currency = "CNY"
	}
	m, ok := money(x.Amount)
	if !ok || x.Name == "" || len([]rune(x.Name)) > 120 || !currency(x.Currency) {
		fail(w, 400, "请填写有效的资产名称和金额")
		return
	}
	if x.Category != "stock" && x.Category != "fund" && x.Category != "money" && x.Category != "deposit" && x.Category != "housing" && x.Category != "fixed" {
		fail(w, 400, "暂不支持这个资产类别")
		return
	}
	var q any = nil
	if x.Category == "stock" || x.Category == "fund" {
		if strings.TrimSpace(x.Code) == "" || x.Quantity <= 0 {
			fail(w, 400, "请输入有效的代码和持有数量")
			return
		}
		q = x.Quantity
	}
	strategy := x.InvestmentStrategy
	if strategy == "" {
		strategy = "none"
	}
	if strategy != "none" && strategy != "monthly" && strategy != "weekly" && strategy != "yearly" && strategy != "daily" {
		fail(w, 400, "基金定投策略无效")
		return
	}
	var invest any = nil
	if strategy != "none" {
		z, valid := money(x.InvestmentAmount)
		if x.Category != "fund" || !valid {
			fail(w, 400, "基金定投策略或金额无效")
			return
		}
		invest = z
	}
	res, e := a.db.Exec("INSERT INTO assets(user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note) VALUES(?,?,?,?,?,?,?,?,?,?,?)", u.ID, x.Name, x.Category, nullString(strings.ToUpper(strings.TrimSpace(x.Code))), m, q, x.Currency, x.AnnualRate, strategy, invest, strings.TrimSpace(x.Note))
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	id, _ := res.LastInsertId()
	row, e := scanAsset(a.db.QueryRow("SELECT "+assetCols+" FROM assets WHERE id=?", id))
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	snapshot, snapshotErr := a.ledger.Snapshot(u.ID, "asset_change")
	if snapshotErr != nil {
		snapshot = false
	}
	out(w, 201, map[string]any{"asset": row, "snapshot": snapshot})
}

// nullString 将空字符串转换为数据库 NULL。
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// patchAsset 更新资产市值、持有数量、币种和定投设置。
func (a *app) patchAsset(w http.ResponseWriter, r *http.Request, u *user) {
	var x struct {
		ID                 int64    `json:"id"`
		Amount             *float64 `json:"amount"`
		Quantity           *float64 `json:"quantity"`
		Currency           *string  `json:"currency"`
		AnnualRate         *float64 `json:"annualRate"`
		InvestmentStrategy *string  `json:"investmentStrategy"`
		InvestmentAmount   *float64 `json:"investmentAmount"`
	}
	if body(r, &x) != nil || x.ID < 1 {
		fail(w, 400, "无效资产")
		return
	}
	if x.Amount != nil {
		m, ok := money(*x.Amount)
		if !ok || x.Currency == nil || !currency(strings.ToUpper(*x.Currency)) {
			fail(w, 400, "请填写有效的当前市值")
			return
		}
		var category string
		if err := a.db.QueryRow("SELECT category FROM assets WHERE id=? AND user_id=?", x.ID, u.ID).Scan(&category); err != nil {
			if err == sql.ErrNoRows {
				fail(w, 404, "资产不存在")
			} else {
				fail(w, 500, err.Error())
			}
			return
		}
		strategy := "none"
		var investment any = nil
		if x.InvestmentStrategy != nil {
			strategy = strings.TrimSpace(*x.InvestmentStrategy)
			if strategy != "none" && strategy != "monthly" && strategy != "weekly" && strategy != "yearly" && strategy != "daily" {
				fail(w, 400, "基金定投策略无效")
				return
			}
			if strategy != "none" {
				if category != "fund" {
					fail(w, 400, "只有基金支持定投")
					return
				}
				if x.InvestmentAmount == nil {
					fail(w, 400, "请输入有效定投金额")
					return
				}
				value, valid := money(*x.InvestmentAmount)
				if !valid {
					fail(w, 400, "请输入有效定投金额")
					return
				}
				investment = value
			}
		}
		var res sql.Result
		var e error
		if x.InvestmentStrategy != nil {
			res, e = a.db.Exec("UPDATE assets SET amount=?,quantity=COALESCE(?,quantity),currency=?,investment_strategy=?,investment_amount=? WHERE id=? AND user_id=?", m, x.Quantity, strings.ToUpper(*x.Currency), strategy, investment, x.ID, u.ID)
		} else {
			res, e = a.db.Exec("UPDATE assets SET amount=?,quantity=COALESCE(?,quantity),currency=? WHERE id=? AND user_id=?", m, x.Quantity, strings.ToUpper(*x.Currency), x.ID, u.ID)
		}
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, 404, "资产不存在")
			return
		}
		snapshot, snapshotErr := a.ledger.Snapshot(u.ID, "asset_change")
		if snapshotErr != nil {
			snapshot = false
		}
		response := map[string]any{"ok": true, "amount": m, "quantity": x.Quantity, "currency": strings.ToUpper(*x.Currency), "snapshot": snapshot}
		if x.InvestmentStrategy != nil {
			response["investmentStrategy"] = strategy
			response["investmentAmount"] = investment
		}
		out(w, 200, response)
		return
	}
	if x.AnnualRate == nil || *x.AnnualRate < -100 || *x.AnnualRate > 1000 {
		fail(w, 400, "无效的收益率数据")
		return
	}
	res, e := a.db.Exec("UPDATE assets SET annual_rate=? WHERE id=? AND user_id=?", *x.AnnualRate, x.ID, u.ID)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "资产不存在")
		return
	}
	out(w, 200, map[string]any{"ok": true, "annualRate": *x.AnnualRate})
}
