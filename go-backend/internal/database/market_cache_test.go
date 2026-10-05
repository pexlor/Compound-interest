package database

import "testing"

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
