package database

import (
	"context"
	"testing"
)

// TestMarketCacheMigrationAndReopen 验证新缓存表及备份注册存在，并确认重新打开数据库不会丢失数据。
func TestMarketCacheMigrationAndReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"market_daily_prices", "market_quotes", "market_sync_state", "market_return_cache", "asset_daily_snapshots", "market_refresh_runs"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("cache table %s: %v", table, err)
		}
		if _, ok := findBackupTable(table); !ok {
			t.Fatalf("backup missing %s", table)
		}
	}
	if _, err := db.Exec(`INSERT INTO market_daily_prices(category,code,price_date,price,return_price,currency,source,fetched_at) VALUES('fund','021000','2026-09-30',2,2,'CNY','test','now') ON CONFLICT(category,code,price_date) DO UPDATE SET price=excluded.price`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var p float64
	if err = db.QueryRow(`SELECT price FROM market_daily_prices`).Scan(&p); err != nil || p != 2 {
		t.Fatalf("cache lost: %v %v", p, err)
	}
}

// TestExistingBackupAddsCaptureForMarketCacheTables 验证已有备份源补装新表捕获触发器并保留源身份。
func TestExistingBackupAddsCaptureForMarketCacheTables(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := EnableBackup(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-cache application's existing backup marker and triggers.
	for _, op := range []string{"insert", "update", "delete"} {
		if _, err = db.Exec("DROP TRIGGER backup_capture_market_quotes_" + op); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`DELETE FROM backup_outbox`)
	if _, err = db.Exec(`INSERT INTO market_quotes(category,code,price,currency,price_date,source,fetched_at) VALUES('stock','AAPL',100,'USD','2026-10-04','test','now')`); err != nil {
		t.Fatal(err)
	}
	again, err := EnableBackup(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if again != source {
		t.Fatal("source identity changed")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM backup_outbox WHERE table_name='market_quotes'`).Scan(&n)
	if n != 1 {
		t.Fatalf("existing cache row not seeded: %d", n)
	}
	db.Exec(`UPDATE market_quotes SET price=200`)
	db.QueryRow(`SELECT COUNT(*) FROM backup_outbox WHERE table_name='market_quotes'`).Scan(&n)
	if n != 2 {
		t.Fatal("new cache updates not captured")
	}
}
