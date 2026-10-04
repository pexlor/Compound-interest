// 每日任务调度：按上海时间刷新汇率、记录资产快照并在启动时补录。

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
// StartDailyJobs 启动进程内每日任务，先补录缺失快照；取消上下文时停止调度。
func StartDailyJobs(ctx context.Context, db *sql.DB) {
	a := &app{db: db, ledger: service.NewLedger(db)}
	go /* 在后台补录当天缺失快照，然后进入每日任务调度循环。 */ func() {
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

// runDailyJobs 按上海时间在每日 05:00 记录快照、09:16 刷新汇率，并响应退出信号。
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

// snapshotAllCurrentAssets 逐个刷新用户持仓估值并记录资产快照，失败后继续处理其余用户。
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

// snapshotMissingCurrentAssets 只为当天没有快照的用户刷新估值并补录快照。
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
// readUserIDs 读取用户编号后关闭查询，释放 SQLite 单连接以便后续查询继续执行。
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

// nextDailyRun 计算上海时区中下一次指定时分的运行时间，今天已过时则顺延一天。
func nextDailyRun(now time.Time, hour, minute int) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, shanghai)
	if !candidate.After(now) {
		return candidate.AddDate(0, 0, 1)
	}
	return candidate
}
