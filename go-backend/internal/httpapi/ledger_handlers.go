package httpapi

import (
	"math"
	"net/http"
	"strconv"
)

// income 处理收入设置的读取和保存请求。
func (a *app) income(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method == "GET" {
		income, e := a.ledger.Income(u.ID)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		out(w, 200, map[string]any{"income": income})
		return
	}
	if r.Method != "PUT" {
		fail(w, 405, "方法不允许")
		return
	}
	var x struct{ MonthlySalary, MonthlySavings, AnnualBonus float64 }
	if body(r, &x) != nil || x.MonthlySalary < 0 || x.MonthlySavings < 0 || x.AnnualBonus < 0 || x.MonthlySalary > maxMoney || x.MonthlySavings > maxMoney || x.AnnualBonus > maxMoney {
		fail(w, 400, "请输入有效的工资、储蓄额和年终奖")
		return
	}
	s, ss, bonus := int64(math.Round(x.MonthlySalary*100)), int64(math.Round(x.MonthlySavings*100)), int64(math.Round(x.AnnualBonus*100))
	income, e := a.ledger.SaveIncome(u.ID, s, ss, bonus)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	out(w, 200, map[string]any{"income": income})
}

// history 处理资产历史快照的查询和手动记录请求。
func (a *app) history(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method == "POST" {
		ok, err := a.ledger.Snapshot(u.ID, "asset_change")
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out(w, 200, map[string]any{"ok": ok})
		return
	}
	if r.Method != "GET" {
		fail(w, 405, "方法不允许")
		return
	}
	limit := 365
	if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 {
		limit = n
	}
	if limit > 3650 {
		limit = 3650
	}
	v, e := a.ledger.History(u.ID, limit)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	out(w, 200, map[string]any{"history": v})
}

// dashboard 聚合仪表盘首次加载所需的数据。
func (a *app) dashboard(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	// Opening the dashboard is also a useful observation point for the user's
	// current total.  The daily uniqueness constraint makes repeated opens
	// update today's single row instead of creating duplicates.
	snapshotRecorded, _ := a.ledger.Snapshot(u.ID, "dashboard_open")
	assets, e := a.listAssets(u.ID)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	income, e := a.ledger.Income(u.ID)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	history, e := a.ledger.History(u.ID, 3650)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	rates, date, e := a.ledger.LatestRates()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	out(w, 200, map[string]any{"user": u, "assets": assets, "history": history, "income": income, "rates": rates, "date": date, "stale": date == "", "snapshotRecorded": snapshotRecorded})
}
