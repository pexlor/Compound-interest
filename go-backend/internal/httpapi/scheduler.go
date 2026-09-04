package httpapi

import (
	"context"
	"database/sql"
	"log"
	"time"

	"fulibu-go/internal/service"
)

// StartDailyJobs starts the application's in-process daily jobs.  It is safe
// to call once for a server process; cancelling ctx stops the scheduler.
func StartDailyJobs(ctx context.Context, db *sql.DB) {
	a := &app{db: db, ledger: service.NewLedger(db)}
	go func() {
		// A process may be started or restarted after 05:00.  Fill only a
		// missing row, so this recovery step never replaces today's scheduled
		// or user-opened snapshot.
		if err := a.ledger.SnapshotMissingTodayAll("startup_recovery"); err != nil {
			log.Printf("startup asset snapshot recovery failed: %v", err)
		} else {
			log.Printf("startup asset snapshot recovery completed")
		}
		a.runDailyJobs(ctx)
	}()
}

func (a *app) runDailyJobs(ctx context.Context) {
	for {
		now := time.Now().In(shanghai)
		rateAt := nextDailyRun(now, 9, 16)
		snapshotAt := nextDailyRun(now, 5, 0)
		next, job := rateAt, "rates"
		if snapshotAt.Before(rateAt) {
			next, job = snapshotAt, "snapshot"
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if job == "rates" {
			if _, _, err := a.refreshRates(); err != nil {
				log.Printf("scheduled exchange-rate refresh failed: %v", err)
			} else {
				log.Printf("scheduled exchange-rate cache refreshed")
			}
			continue
		}
		if err := a.ledger.SnapshotAll("daily_scheduled"); err != nil {
			log.Printf("scheduled asset snapshot failed: %v", err)
		} else {
			log.Printf("scheduled asset snapshot completed")
		}
	}
}

func nextDailyRun(now time.Time, hour, minute int) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, shanghai)
	if !candidate.After(now) {
		return candidate.AddDate(0, 0, 1)
	}
	return candidate
}
