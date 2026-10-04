// 账本 HTTP 接口：提供收入设置、资产历史、仪表盘数据和当前资产快照。

package httpapi

import (
	"database/sql"
	"fulibu-go/internal/service"
	"net/http"
	"strconv"
	"time"
)

// income 处理收入设置的读取和保存请求。
func (a *app) income(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method == http.MethodGet {
		v, err := a.ledger.Income(u.ID)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		out(w, 200, map[string]any{"income": v, "moneyUnit": "minor"})
		return
	}
	if r.Method != http.MethodPatch {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	var x service.IncomePatch
	o, err := mutationInput(r, u, &x)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	a.mutate(w, r, o /* 在事务中读取原收入并保存修改，返回变更前后数据。 */, func(tx *sql.Tx) (service.MutationResult, error) {
		before, err := service.ReadIncome(tx, u.ID)
		if err != nil {
			return service.MutationResult{}, err
		}
		if err = checkVersion(x.Version, before.Version); err != nil {
			return service.MutationResult{}, err
		}
		after, err := service.PatchIncome(tx, u.ID, before, x)
		if err != nil {
			return service.MutationResult{}, err
		}
		return mutationJSON(200, map[string]any{"income": after, "before": before, "after": after, "dryRun": o.DryRun, "moneyUnit": "minor"})
	})
}

// history 处理资产历史快照的查询和手动记录请求。
func (a *app) history(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method == http.MethodPost {
		var x /* 表示不需要业务字段的手动快照请求体。 */ struct{}
		o, err := mutationInput(r, u, &x)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		a.mutate(w, r, o /* 在变更事务中记录当天资产快照，并区分实际写入与预览结果。 */, func(tx *sql.Tx) (service.MutationResult, error) {
			ok, err := service.SnapshotTx(tx, u.ID, "asset_change")
			if err != nil {
				return service.MutationResult{}, err
			}
			return mutationJSON(200, map[string]any{"ok": ok, "dryRun": o.DryRun, "snapshotRecorded": ok && !o.DryRun})
		})
		return
	}
	if r.Method != http.MethodGet {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	limit := 365
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeAPIError(w, badRequest("limit 必须为正整数"))
			return
		}
		limit = n
	}
	if limit > 3650 {
		limit = 3650
	}
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	for _, date := range []string{from, to} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil || len(date) != 10 {
				writeAPIError(w, badRequest("日期必须为 YYYY-MM-DD"))
				return
			}
		}
	}
	if from != "" && to != "" && from > to {
		writeAPIError(w, badRequest("开始日期不能晚于结束日期"))
		return
	}
	v, err := a.ledger.HistoryRange(u.ID, limit, from, to)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	out(w, 200, map[string]any{"history": v, "moneyUnit": "minor"})
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
	snapshotRecorded, _ := a.snapshotCurrentAssets(u.ID, "dashboard_open")
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

// snapshotCurrentAssets refreshes quote-based holdings first, then records the
// portfolio total. It keeps the displayed dashboard and its history row on
// the same valuation basis.
// snapshotCurrentAssets 先刷新当前用户的证券市值，再按最新汇率记录当天资产快照。
func (a *app) snapshotCurrentAssets(userID int64, trigger string) (bool, error) {
	if err := a.refreshMarketAssetValues(userID); err != nil {
		return false, err
	}
	return a.ledger.Snapshot(userID, trigger)
}
