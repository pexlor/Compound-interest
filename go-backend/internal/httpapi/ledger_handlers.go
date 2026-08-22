package httpapi

import (
	"math"
	"net/http"
	"strconv"
)

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
	var x struct{ MonthlySalary, MonthlySavings float64 }
	if body(r, &x) != nil || x.MonthlySalary < 0 || x.MonthlySavings < 0 || x.MonthlySalary > maxMoney || x.MonthlySavings > maxMoney {
		fail(w, 400, "请输入有效的工资和预计储蓄额")
		return
	}
	s, ss := int64(math.Round(x.MonthlySalary*100)), int64(math.Round(x.MonthlySavings*100))
	income, e := a.ledger.SaveIncome(u.ID, s, ss)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	out(w, 200, map[string]any{"income": income})
}
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
func (a *app) dashboard(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
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
	out(w, 200, map[string]any{"user": u, "assets": assets, "history": history, "income": income, "rates": rates, "date": date, "stale": date == ""})
}
