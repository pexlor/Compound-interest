package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func waitBackup(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("backup did not reach expected state before timeout")
}

func targetTestDSN(t *testing.T, target *sql.DB) string {
	t.Helper()
	var name string
	if err := target.QueryRow("SELECT DATABASE()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(testMySQLDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	return cfg.FormatDSN()
}

func TestBackupWorkerDisabledAndUnavailable(t *testing.T) {
	ctx := context.Background()
	primary := backupTestDB(t)
	disabled, err := StartMySQLBackup(ctx, primary, "")
	if err != nil {
		t.Fatal(err)
	}
	defer disabled.Close()
	var count int
	primary.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='backup_outbox'").Scan(&count)
	if count != 0 {
		t.Fatal("disabled worker installed capture")
	}
	broken, err := StartMySQLBackup(ctx, primary, "never-log-this-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	execBackupTest(t, primary, `INSERT INTO exchange_rates VALUES('USD',7,'2026-10-04','2026-10-04 00:00:00')`)
	waitBackup(t, func() bool { status, err := broken.Status(ctx); return err == nil && status.LastError != "" })
	status, err := broken.Status(ctx)
	if err != nil || status.Pending != 1 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("status leaked DSN")
	}
	stopped := time.Now()
	broken.Close()
	if time.Since(stopped) > 2*time.Second {
		t.Fatal("shutdown blocked")
	}
	closed := backupTestDB(t)
	closed.Close()
	if _, err := StartMySQLBackup(ctx, closed, "configured"); err == nil {
		t.Fatal("closed primary accepted")
	}
}

// Losing persistent tasks, reading backup as primary, or skipping retry breaks recovery.
func TestBackupWorkerRecoveryAndShutdown(t *testing.T) {
	ctx := context.Background()
	primary := backupTestDB(t)
	target := backupMySQLTestDB(t)
	seedBackupApplication(t, primary)
	worker, err := StartMySQLBackup(ctx, primary, targetTestDSN(t, target))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	waitBackup(t, func() bool { s, e := worker.Status(ctx); return e == nil && s.Pending == 0 && s.LastSuccess != "" })
	compareBackupTables(t, primary, target)
	execBackupTest(t, target, `RENAME TABLE backup_checkpoint TO unavailable_checkpoint`)
	execBackupTest(t, primary, `UPDATE exchange_rates SET cny_rate=12 WHERE currency='USD'`)
	waitBackup(t, func() bool { s, e := worker.Status(ctx); return e == nil && s.Pending > 0 && s.LastError != "" })
	execBackupTest(t, target, `RENAME TABLE unavailable_checkpoint TO backup_checkpoint`)
	waitBackup(t, func() bool { s, e := worker.Status(ctx); return e == nil && s.Pending == 0 && s.LastError == "" })
	compareBackupTables(t, primary, target)
	worker.Close()
	disabled, err := StartMySQLBackup(ctx, primary, "")
	if err != nil {
		t.Fatal(err)
	}
	execBackupTest(t, primary, `UPDATE exchange_rates SET cny_rate=13 WHERE currency='USD'`)
	status, err := disabled.Status(ctx)
	if err != nil || status.Enabled || status.Pending != 1 {
		t.Fatalf("disabled=%+v err=%v", status, err)
	}
	disabled.Close()
	worker, err = StartMySQLBackup(ctx, primary, targetTestDSN(t, target))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	waitBackup(t, func() bool { s, e := worker.Status(ctx); return e == nil && s.Pending == 0 && s.LastSuccess != "" })
	compareBackupTables(t, primary, target)
}

func TestBackupReinitializeNewEmptyTarget(t *testing.T) {
	ctx := context.Background()
	primary := backupTestDB(t)
	target := backupMySQLTestDB(t)
	seedBackupApplication(t, primary)
	source, err := EnableBackup(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(ctx, target, source, false); err != nil {
		t.Fatal(err)
	}
	applyAllTestEvents(t, primary, target, source)
	fresh := backupMySQLTestDB(t)
	if err := InitializeBackupTarget(ctx, fresh, source, true); err == nil {
		t.Fatal("changed target accepted")
	}
	if _, err := ReinitializeMySQLBackup(ctx, primary, target); err == nil {
		t.Fatal("nonempty target accepted")
	}
	var same string
	primary.QueryRow("SELECT source_id FROM backup_metadata WHERE id=1").Scan(&same)
	if same != source {
		t.Fatal("failed reinitialize changed source")
	}
	next, err := ReinitializeMySQLBackup(ctx, primary, fresh)
	if err != nil || next == source {
		t.Fatalf("source=%s err=%v", next, err)
	}
	if err := InitializeBackupTarget(ctx, fresh, next, false); err != nil {
		t.Fatal(err)
	}
	applyAllTestEvents(t, primary, fresh, next)
	compareBackupTables(t, primary, fresh)
}

func TestBackupDataLockExcludesRunningServer(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireDataLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := AcquireDataLock(dir); err == nil {
		second.Close()
		t.Fatal("concurrent owner accepted")
	}
	first.Close()
	next, err := AcquireDataLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

// A restored backup behind the acknowledged watermark cannot fill the missing events.
func TestBackupReplicationRejectsCheckpointRegression(t *testing.T) {
	ctx := context.Background()
	primary := backupTestDB(t)
	target := backupMySQLTestDB(t)
	seedBackupApplication(t, primary)
	source, err := EnableBackup(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(ctx, target, source, false); err != nil {
		t.Fatal(err)
	}
	applyAllTestEvents(t, primary, target, source)
	var acknowledged int64
	primary.QueryRow("SELECT acknowledged FROM backup_metadata WHERE id=1").Scan(&acknowledged)
	execBackupTest(t, target, "UPDATE backup_checkpoint SET sequence=0 WHERE id=1")
	execBackupTest(t, target, "DELETE FROM assets WHERE id=1")
	execBackupTest(t, primary, "UPDATE exchange_rates SET cny_rate=14 WHERE currency='USD'")
	if err := RunBackupBatch(ctx, primary, target, source); err == nil {
		t.Fatal("checkpoint regression was silently accepted with a nonempty queue")
	}
	var after int64
	primary.QueryRow("SELECT acknowledged FROM backup_metadata WHERE id=1").Scan(&after)
	events, _ := ReadBackupEvents(ctx, primary, 100)
	if after != acknowledged || len(events) != 1 {
		t.Fatal("regressed target acknowledged pending data")
	}
	var rate float64
	target.QueryRow("SELECT cny_rate FROM exchange_rates WHERE currency='USD'").Scan(&rate)
	if rate != 7.25 {
		t.Fatal("modified regressed backup")
	}
}
