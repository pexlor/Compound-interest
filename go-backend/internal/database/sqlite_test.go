// 数据库初始化测试：验证旧表结构会被拒绝且不会被自动升级。

package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestOpenRejectsOlderSchemaWithoutAddingColumns 验证旧表结构被拒绝，且初始化过程不会自动为旧表增加字段。
func TestOpenRejectsOlderSchemaWithoutAddingColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fulibu.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE income_settings(user_id INTEGER PRIMARY KEY,monthly_salary INTEGER NOT NULL DEFAULT 0,monthly_savings INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	opened, err := Open(dir)
	if opened != nil {
		opened.Close()
	}
	if err == nil {
		t.Error("older schema was accepted and upgraded")
	}
	db, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('income_settings') WHERE name IN ('annual_bonus','compensation','version')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("added %d columns to older schema", count)
	}
}

// TestRejectedSchemaDoesNotCreateTables 验证拒绝旧表结构时不会创建额外的业务表。
func TestRejectedSchemaDoesNotCreateTables(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "fulibu.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE income_settings(user_id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	opened, err := Open(dir)
	if opened != nil {
		opened.Close()
	}
	if err == nil {
		t.Fatal("old schema accepted")
	}
	db, err = sql.Open("sqlite3", filepath.Join(dir, "fulibu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("rejection created tables: count=%d", tables)
	}
}

// TestCurrentSchemaReopensAndPreservesVersionUpdates 验证当前数据库可重新打开，且资产和计划更新仍会递增版本。
func TestCurrentSchemaReopensAndPreservesVersionUpdates(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(1,'test','test','hash','salt');
INSERT INTO assets(user_id,name,category,amount) VALUES(1,'cash','deposit',10000);
INSERT INTO income_settings(user_id) VALUES(1);
INSERT INTO retirement_goal_items(user_id,name,category,amount) VALUES(1,'goal','deposit',20000);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"assets", "income_settings", "retirement_goal_items"} {
		if _, err := db.Exec("UPDATE " + table + " SET version=version"); err != nil {
			t.Fatal(err)
		}
		var version int
		if err := db.QueryRow("SELECT version FROM " + table).Scan(&version); err != nil || version != 2 {
			t.Fatalf("%s version=%d err=%v", table, version, err)
		}
	}
}
