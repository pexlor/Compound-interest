package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

type BackupStatus struct {
	Enabled     bool   `json:"enabled"`
	Pending     int64  `json:"pending"`
	LastSuccess string `json:"last_success"`
	LastError   string `json:"last_error"`
}

type Backup struct {
	primary *sql.DB
	enabled bool
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

// SQLite capture errors fail startup. MySQL configuration/connection failures only
// affect the backup worker, which leaves committed tasks in the primary database.
func StartMySQLBackup(ctx context.Context, primary *sql.DB, dsn string) (*Backup, error) {
	ctx, cancel := context.WithCancel(ctx)
	b := &Backup{primary: primary, enabled: strings.TrimSpace(dsn) != "", cancel: cancel, done: make(chan struct{})}
	if !b.enabled {
		close(b.done)
		return b, nil
	}
	source, err := EnableBackup(ctx, primary)
	if err != nil {
		cancel()
		return nil, err
	}
	go b.run(ctx, dsn, source)
	return b, nil
}

func (b *Backup) Close() error {
	b.once.Do(b.cancel)
	<-b.done
	return nil
}

func (b *Backup) Status(ctx context.Context) (BackupStatus, error) {
	return ReadBackupStatus(ctx, b.primary, b.enabled)
}

// ReadBackupStatus exposes only queue health, including persisted state when disabled.
func ReadBackupStatus(ctx context.Context, primary *sql.DB, enabled bool) (BackupStatus, error) {
	status := BackupStatus{Enabled: enabled}
	var exists int
	if err := primary.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='backup_metadata'`).Scan(&exists); err != nil {
		return status, err
	}
	if exists == 0 {
		return status, nil
	}
	if err := primary.QueryRowContext(ctx, `SELECT last_success,last_error,(SELECT COUNT(*) FROM backup_outbox) FROM backup_metadata WHERE id=1`).Scan(&status.LastSuccess, &status.LastError, &status.Pending); err != nil {
		return status, err
	}
	return status, nil
}

func RunBackupBatch(ctx context.Context, primary, target *sql.DB, sourceID string) error {
	var acknowledged int64
	if err := primary.QueryRowContext(ctx, `SELECT acknowledged FROM backup_metadata WHERE id=1`).Scan(&acknowledged); err != nil {
		return err
	}
	events, err := ReadBackupEvents(ctx, primary, 100)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		var source string
		var checkpoint, acknowledged int64
		var lastSuccess, lastError string
		if err := target.QueryRowContext(ctx, `SELECT source_id,sequence FROM backup_checkpoint WHERE id=1`).Scan(&source, &checkpoint); err != nil {
			return err
		}
		if err := primary.QueryRowContext(ctx, `SELECT acknowledged,last_success,last_error FROM backup_metadata WHERE id=1`).Scan(&acknowledged, &lastSuccess, &lastError); err != nil {
			return err
		}
		if checkpoint < acknowledged {
			return ErrBackupCheckpointRegressed
		}
		if source != sourceID || checkpoint != acknowledged {
			return ErrBackupSourceConflict
		}
		if lastSuccess == "" || lastError != "" {
			return AckBackupEvents(ctx, primary, checkpoint)
		}
		return nil
	}
	checkpoint, err := applyBackupEventsAtCheckpoint(ctx, target, sourceID, events, acknowledged)
	if err != nil {
		return err
	}
	return AckBackupEvents(ctx, primary, checkpoint)
}

func (b *Backup) run(ctx context.Context, dsn, source string) {
	defer close(b.done)
	var target *sql.DB
	defer func() {
		if target != nil {
			target.Close()
		}
	}()
	initialized := false
	delay := time.Second
	previousError := ""
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		if target == nil {
			target, err = OpenBackupMySQL(dsn)
		}
		if err == nil && !initialized {
			var acknowledged int64
			var lastSuccess string
			err = b.primary.QueryRowContext(attempt, `SELECT acknowledged,last_success FROM backup_metadata WHERE id=1`).Scan(&acknowledged, &lastSuccess)
			if err == nil {
				err = InitializeBackupTarget(attempt, target, source, acknowledged > 0 || lastSuccess != "")
			}
			initialized = err == nil
		}
		if err == nil {
			err = RunBackupBatch(attempt, b.primary, target, source)
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		wait := time.Second
		if err != nil {
			initialized = false
			message := safeBackupError(err)
			saveCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			_, saveErr := b.primary.ExecContext(saveCtx, `UPDATE backup_metadata SET last_error=? WHERE id=1 AND last_error<>?`, message, message)
			stop()
			if message != previousError {
				log.Printf("MySQL backup: %s", message)
				previousError = message
			}
			if saveErr != nil {
				log.Print("MySQL backup: unable to persist backup status")
			}
			wait = delay
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		} else {
			delay = time.Second
			if previousError != "" {
				log.Print("MySQL backup: recovered")
				previousError = ""
			}
			if status, e := b.Status(ctx); e == nil && status.Pending > 0 {
				continue
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func safeBackupError(err error) string {
	for _, known := range []error{ErrBackupSourceConflict, ErrBackupUnownedTarget, ErrBackupReinitialize, ErrBackupSchema, ErrBackupCheckpointRegressed} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return fmt.Sprintf("MySQL error %d; backup retained for retry", mysqlErr.Number)
	}
	if err.Error() == "invalid MYSQL_DSN" || err.Error() == "invalid MySQL backup configuration" {
		return "invalid MYSQL_DSN; backup retained for retry"
	}
	return "backup unavailable; committed changes retained for retry"
}

// ReinitializeMySQLBackup is explicitly invoked under the application's data lock.
// Verify the target BEFORE replacing the local source and snapshot queue.
func ReinitializeMySQLBackup(ctx context.Context, primary, target *sql.DB) (string, error) {
	if err := target.PingContext(ctx); err != nil {
		return "", err
	}
	var tables int
	if err := target.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()`).Scan(&tables); err != nil {
		return "", err
	}
	if tables != 0 {
		return "", ErrBackupUnownedTarget
	}
	return ReinitializeBackup(ctx, primary)
}
