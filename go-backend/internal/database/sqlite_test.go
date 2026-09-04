package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestImportLegacyIsRepeatableAndAllowsMissingOptionalTables 验证旧数据导入可重复执行且兼容缺失的可选表。
func TestImportLegacyIsRepeatableAndAllowsMissingOptionalTables(t *testing.T) {
	target, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	legacyPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, err := sql.Open("sqlite3", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY,email TEXT,display_name TEXT,password_hash TEXT,password_salt TEXT,password_iterations INTEGER,created_at TEXT);
CREATE TABLE assets(id INTEGER PRIMARY KEY,user_id INTEGER,name TEXT,category TEXT,code TEXT,amount INTEGER,quantity REAL,currency TEXT,annual_rate REAL,investment_strategy TEXT,investment_amount INTEGER,note TEXT,created_at TEXT);
INSERT INTO users VALUES(7,'user@example.com','User','hash','salt',210000,'2026-01-01');
INSERT INTO assets VALUES(9,7,'现金','deposit',NULL,12345,NULL,'CNY',0,'none',NULL,'','2026-01-01');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := ImportLegacy(target, legacyPath); err != nil {
			t.Fatal(err)
		}
	}
	var users, assets int
	if err := target.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow("SELECT COUNT(*) FROM assets").Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if users != 1 || assets != 1 {
		t.Fatalf("unexpected import count: users=%d assets=%d", users, assets)
	}
}
