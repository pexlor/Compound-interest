package service

import (
	"testing"
	"time"

	"fulibu-go/internal/database"
)

func TestRatesCachedSinceRequiresCompleteRecentCache(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger := NewLedger(db)
	cutoff := time.Date(2026, 8, 31, 9, 15, 0, 0, time.FixedZone("CST", 8*3600))
	for _, currency := range []string{"USD", "HKD"} {
		if _, err := db.Exec(`INSERT INTO exchange_rates(currency,cny_rate,rate_date,updated_at) VALUES(?,?,?,?)`, currency, 1.0, "2026-08-29", "2026-08-31 01:16:00"); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := ledger.RatesCachedSince(cutoff, 2)
	if err != nil || !fresh {
		t.Fatalf("expected fresh complete cache, fresh=%v err=%v", fresh, err)
	}
	if _, err := db.Exec(`UPDATE exchange_rates SET updated_at='2026-08-31 01:14:59' WHERE currency='USD'`); err != nil {
		t.Fatal(err)
	}
	fresh, err = ledger.RatesCachedSince(cutoff, 2)
	if err != nil || fresh {
		t.Fatalf("expected stale cache, fresh=%v err=%v", fresh, err)
	}
}

func TestSnapshotAllWritesEveryUser(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, email := range []string{"one@example.com", "two@example.com"} {
		if _, err := db.Exec(`INSERT INTO users(email,display_name,password_hash,password_salt) VALUES(?,?,?,?)`, email, email, "hash", "salt"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO assets(user_id,name,category,amount,currency) VALUES(1,'cash','deposit',12345,'CNY')`); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(db).SnapshotAll("daily_scheduled"); err != nil {
		t.Fatal(err)
	}
	var snapshots int
	if err := db.QueryRow("SELECT COUNT(*) FROM asset_history WHERE trigger='daily_scheduled'").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 {
		t.Fatalf("expected 2 snapshots, got %d", snapshots)
	}
}

func TestSnapshotMissingTodayAllDoesNotReplaceExistingSnapshot(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, email := range []string{"one@example.com", "two@example.com"} {
		if _, err := db.Exec(`INSERT INTO users(email,display_name,password_hash,password_salt) VALUES(?,?,?,?)`, email, email, "hash", "salt"); err != nil {
			t.Fatal(err)
		}
	}
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	if _, err := db.Exec(`INSERT INTO asset_history(user_id,snapshot_date,total_cny,trigger) VALUES(?,?,?,?)`, 1, today, 999, "daily_scheduled"); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(db).SnapshotMissingTodayAll("startup_recovery"); err != nil {
		t.Fatal(err)
	}
	var existingTotal int64
	var existingTrigger string
	if err := db.QueryRow(`SELECT total_cny,trigger FROM asset_history WHERE user_id=1 AND snapshot_date=?`, today).Scan(&existingTotal, &existingTrigger); err != nil {
		t.Fatal(err)
	}
	if existingTotal != 999 || existingTrigger != "daily_scheduled" {
		t.Fatalf("existing snapshot was replaced: total=%d trigger=%s", existingTotal, existingTrigger)
	}
	var recovered int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_history WHERE user_id=2 AND snapshot_date=? AND trigger='startup_recovery'`, today).Scan(&recovered); err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("expected one recovered snapshot, got %d", recovered)
	}
}
