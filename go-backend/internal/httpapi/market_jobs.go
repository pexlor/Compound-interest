package httpapi

import (
	"context"
	"fmt"
	"log"
	"time"

	"fulibu-go/internal/service"
)

func nextMarketRun(now time.Time) time.Time {
	now = now.In(shanghai)
	morning := nextDailyRun(now, 9, 30)
	evening := nextDailyRun(now, 17, 0)
	if morning.Before(evening) {
		return morning
	}
	return evening
}
func dueMarketSlots(now time.Time) []string {
	now = now.In(shanghai)
	slots := []string{}
	if !now.Before(time.Date(now.Year(), now.Month(), now.Day(), 9, 30, 0, 0, shanghai)) {
		slots = append(slots, "09:30")
	}
	if !now.Before(time.Date(now.Year(), now.Month(), now.Day(), 17, 0, 0, 0, shanghai)) {
		slots = append(slots, "17:00")
	}
	return slots
}
func (a *app) runMarketSlots(ctx context.Context, now time.Time, refresh func(context.Context) (int, int, error)) error {
	c := marketCacheFor(a.db)
	c.jobsMu.Lock()
	defer c.jobsMu.Unlock()
	date := now.In(shanghai).Format("2006-01-02")
	missing := []string{}
	for _, slot := range dueMarketSlots(now) {
		var state, finished string
		a.db.QueryRowContext(ctx, `SELECT job_state,finished_at FROM market_refresh_runs WHERE run_date=? AND slot=?`, date, slot).Scan(&state, &finished)
		if state == "complete" {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, finished)
		if state == "failed" && now.Sub(at) < 5*time.Minute {
			continue
		}
		missing = append(missing, slot)
	}
	if len(missing) == 0 {
		return nil
	}
	for _, slot := range missing {
		if _, err := a.db.ExecContext(ctx, `INSERT INTO market_refresh_runs(run_date,slot,job_state,started_at) VALUES(?,?,'running',?) ON CONFLICT(run_date,slot) DO UPDATE SET job_state='running',started_at=excluded.started_at,finished_at='',last_error=''`, date, slot, cacheTime()); err != nil {
			return err
		}
	}
	good, bad, err := refresh(ctx)
	state, message := "complete", ""
	if err != nil || bad > 0 {
		state = "failed"
		message = fmt.Sprint(err)
		if err == nil {
			err = fmt.Errorf("%d 项行情更新失败", bad)
			message = err.Error()
		}
	}
	for _, slot := range missing {
		if _, e := a.db.Exec(`UPDATE market_refresh_runs SET job_state=?,finished_at=?,success_count=?,failure_count=?,last_error=? WHERE run_date=? AND slot=?`, state, cacheTime(), good, bad, message, date, slot); e != nil {
			return e
		}
	}
	log.Printf("[market-cache] batch slots=%v state=%s success=%d failure=%d", missing, state, good, bad)
	return err
}
func (a *app) runMarketJobs(ctx context.Context) {
	// Recovery coalesces all elapsed slots. If no slot is due, preload cache.
	now := time.Now()
	if len(dueMarketSlots(now)) == 0 {
		if _, _, err := a.refreshMarketBatch(ctx); err != nil {
			log.Printf("[market-cache] startup: %v", err)
		}
	}
	if err := a.runMarketSlots(ctx, now, a.refreshMarketBatch); err != nil {
		log.Printf("[market-cache] recovery: %v", err)
	}
	a.warmMissingMarketCache(ctx)
	// Minute checks also retry a partially failed slot after its cooldown.
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now = <-ticker.C:
			if err := a.runMarketSlots(ctx, now, a.refreshMarketBatch); err != nil {
				log.Printf("[market-cache] scheduled: %v", err)
			}
		}
	}
}
func (a *app) refreshMarketBatch(ctx context.Context) (int, int, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT DISTINCT category,code FROM assets WHERE archived_at IS NULL AND code IS NOT NULL AND category IN ('stock','fund','money') ORDER BY category,code`)
	if err != nil {
		return 0, 0, err
	}
	type key struct{ cat, code string }
	keys := []key{}
	seen := map[string]bool{}
	for rows.Next() {
		var category, code string
		if err = rows.Scan(&category, &code); err != nil {
			rows.Close()
			return 0, 0, err
		}
		cat, canonical, e := marketIdentity(category, code)
		if e != nil {
			continue
		}
		k := cat + ":" + canonical
		if !seen[k] {
			keys = append(keys, key{cat, canonical})
			seen[k] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, 0, err
	}
	c := marketCacheFor(a.db)
	dones := make([]<-chan struct{}, len(keys))
	for i, k := range keys {
		dones[i] = c.startSync(k.cat, k.code, false)
	}
	good, bad := 0, 0
	for i, done := range dones {
		select {
		case <-ctx.Done():
			return good, bad, ctx.Err()
		case <-done:
		}
		if c.lastError(keys[i].cat, keys[i].code) != "" {
			bad++
		} else {
			good++
		}
	}
	// Rates are fetched after network history work, never inside a transaction.
	if _, _, e := a.refreshRates(); e != nil {
		bad++
		log.Printf("[market-cache] rates_failed: %v", e)
	}
	if err = a.recordCachedAssetSnapshots(ctx); err != nil {
		return good, bad, err
	}
	if bad > 0 {
		return good, bad, fmt.Errorf("%d 项更新失败，保留已有数据", bad)
	}
	return good, bad, nil
}
func (a *app) recordCachedAssetSnapshots(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		return err
	}
	users, err := readUserIDs(rows)
	if err != nil {
		return err
	}
	c := marketCacheFor(a.db)
	date := marketDate()
	for _, userID := range users {
		assets, err := a.listAssets(userID)
		if err != nil {
			return err
		}
		for _, asset := range assets {
			amount, currency, rate := asset.Amount, asset.Currency, asset.AnnualRate
			source, priceDate := "用户记录", date
			quantity := 0.0
			if asset.Quantity != nil {
				quantity = *asset.Quantity
			}
			if asset.Code != nil {
				cat, code, e := marketIdentity(asset.Category, *asset.Code)
				if e == nil {
					var price float64
					var qCurrency, qDate, qSource string
					if e = c.db.QueryRowContext(ctx, `SELECT price,currency,price_date,source FROM market_quotes WHERE category=? AND code=?`, cat, code).Scan(&price, &qCurrency, &qDate, &qSource); e == nil {
						source, priceDate = qSource, qDate
						if (asset.Category == "stock" || asset.Category == "fund") && quantity > 0 {
							value, e := service.Money(quantity*price, false)
							if e == nil {
								// A successful cache refresh may update market valuations, but
								// concurrent edits/archives must keep their user's newer version.
								res, e := a.db.ExecContext(ctx, `UPDATE assets SET amount=?,currency=? WHERE id=? AND user_id=? AND version=? AND archived_at IS NULL AND (amount<>? OR currency<>?)`, value, qCurrency, asset.ID, userID, asset.Version, value, qCurrency)
								if e != nil {
									return e
								}
								changed, _ := res.RowsAffected()
								if changed > 0 {
									amount, currency = value, qCurrency
								} else if amount != value || currency != qCurrency {
									continue
								}
							}
						}
					}
					if r, ok := c.read(cat, code, 1095); ok {
						rate = r.AnnualRate
					}
				}
			}
			if _, err = a.db.ExecContext(ctx, `INSERT INTO asset_daily_snapshots(user_id,asset_id,snapshot_date,amount,quantity,currency,annual_rate,price_date,source,fetched_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,asset_id,snapshot_date) DO UPDATE SET amount=excluded.amount,quantity=excluded.quantity,currency=excluded.currency,annual_rate=excluded.annual_rate,price_date=excluded.price_date,source=excluded.source,fetched_at=excluded.fetched_at`, userID, asset.ID, date, amount, quantity, currency, rate, priceDate, source, cacheTime()); err != nil {
				return err
			}
		}
		if _, err = a.ledger.Snapshot(userID, "daily_scheduled"); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) warmMissingMarketCache(ctx context.Context) {
	rows, err := a.db.QueryContext(ctx, `SELECT DISTINCT category,code FROM assets WHERE archived_at IS NULL AND code IS NOT NULL AND category IN ('stock','fund','money')`)
	if err != nil {
		return
	}
	type item struct{ category, code string }
	items := []item{}
	for rows.Next() {
		var i item
		if rows.Scan(&i.category, &i.code) == nil {
			items = append(items, i)
		}
	}
	rows.Close()
	c := marketCacheFor(a.db)
	for _, i := range items {
		if ctx.Err() != nil {
			return
		}
		_, _ = c.get(i.category, i.code, 1095, false)
	}
}
