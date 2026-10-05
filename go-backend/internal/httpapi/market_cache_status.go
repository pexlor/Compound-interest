package httpapi

import (
	"net/http"
	"strings"
)

// Cache status is scoped to the caller's holdings; job records contain no
// account identities or balances. It is useful for CLI and deployment checks.
// marketCacheStatus 返回当前用户持仓的缓存覆盖情况、日快照数和当天任务状态。
func (a *app) marketCacheStatus(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "方法不允许")
		return
	}
	assets, err := a.listAssets(u.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	securities := []map[string]any{}
	seen := map[string]bool{}
	for _, asset := range assets {
		if asset.Code == nil {
			continue
		}
		cat, code, e := marketIdentity(asset.Category, *asset.Code)
		if e != nil {
			continue
		}
		key := cat + ":" + code
		if seen[key] {
			continue
		}
		seen[key] = true
		var count int
		var first, last, checked, failure, retry string
		if e = a.db.QueryRow(`SELECT COUNT(*),COALESCE(MIN(price_date),''),COALESCE(MAX(price_date),'') FROM market_daily_prices WHERE category=? AND code=?`, cat, code).Scan(&count, &first, &last); e != nil {
			fail(w, 500, e.Error())
			return
		}
		a.db.QueryRow(`SELECT checked_at,last_error,retry_after FROM market_sync_state WHERE category=? AND code=?`, cat, code).Scan(&checked, &failure, &retry)
		var returns int
		a.db.QueryRow(`SELECT COUNT(*) FROM market_returns WHERE category=? AND code=? AND calculation_date=?`, cat, code, marketDate()).Scan(&returns)
		securities = append(securities, map[string]any{"category": asset.Category, "code": strings.ToUpper(*asset.Code), "dailyRows": count, "firstDate": first, "lastDate": last, "checkedAt": checked, "error": failure, "retryAfter": retry, "returnIntervals": returns, "pending": marketCacheFor(a.db).busy(cat, code)})
	}
	jobs := []map[string]any{}
	rows, err := a.db.Query(`SELECT slot,job_state,started_at,finished_at,success_count,failure_count,last_error FROM market_refresh_runs WHERE run_date=? ORDER BY slot`, marketDate())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	for rows.Next() {
		var slot, state, start, end, message string
		var good, bad int
		if err = rows.Scan(&slot, &state, &start, &end, &good, &bad, &message); err != nil {
			rows.Close()
			fail(w, 500, err.Error())
			return
		}
		jobs = append(jobs, map[string]any{"slot": slot, "state": state, "startedAt": start, "finishedAt": end, "successCount": good, "failureCount": bad, "error": message})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	var snapshots int
	a.db.QueryRow(`SELECT COUNT(*) FROM asset_daily_snapshots WHERE user_id=? AND snapshot_date=?`, u.ID, marketDate()).Scan(&snapshots)
	out(w, 200, map[string]any{"date": marketDate(), "timezone": "Asia/Shanghai", "securities": securities, "scheduledRuns": jobs, "assetSnapshots": snapshots})
}
