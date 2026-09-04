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
		if err := a.snapshotMissingCurrentAssets("startup_recovery"); err != nil {
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
		if err := a.snapshotAllCurrentAssets("daily_scheduled"); err != nil {
			log.Printf("scheduled asset snapshot failed: %v", err)
		} else {
			log.Printf("scheduled asset snapshot completed")
		}
	}
}

func (a *app) snapshotAllCurrentAssets(trigger string) error {
	rows, err := a.db.Query("SELECT id FROM users ORDER BY id")
	if err != nil {
		return err
	}
	userIDs, err := readUserIDs(rows)
	if err != nil {
		return err
	}
	var firstErr error
	for _, userID := range userIDs {
		if _, err := a.snapshotCurrentAssets(userID, trigger); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *app) snapshotMissingCurrentAssets(trigger string) error {
	date := time.Now().In(shanghai).Format("2006-01-02")
	rows, err := a.db.Query(`SELECT u.id FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM asset_history h WHERE h.user_id=u.id AND h.snapshot_date=?)
		ORDER BY u.id`, date)
	if err != nil {
		return err
	}
	userIDs, err := readUserIDs(rows)
	if err != nil {
		return err
	}
	var firstErr error
	for _, userID := range userIDs {
		if _, err := a.snapshotCurrentAssets(userID, trigger); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// readUserIDs closes the query before follow-up work. SQLite intentionally
// uses one connection, so retaining rows while refreshing asset values would
// otherwise block the subsequent queries.
func readUserIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs, rows.Err()
}

func nextDailyRun(now time.Time, hour, minute int) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, shanghai)
	if !candidate.After(now) {
		return candidate.AddDate(0, 0, 1)
	}
	return candidate
}
