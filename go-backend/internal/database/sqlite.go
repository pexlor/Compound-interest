// Package database owns SQLite connection setup and schema migrations.
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Open creates the local database and applies idempotent schema migrations.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "fulibu.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT NOT NULL UNIQUE,display_name TEXT NOT NULL,password_hash TEXT NOT NULL,password_salt TEXT NOT NULL,password_iterations INTEGER NOT NULL DEFAULT 210000,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL,expires_at INTEGER NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS assets(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER,name TEXT NOT NULL,category TEXT NOT NULL,code TEXT,amount INTEGER NOT NULL,quantity REAL,currency TEXT NOT NULL DEFAULT 'CNY',annual_rate REAL NOT NULL DEFAULT 0,investment_strategy TEXT NOT NULL DEFAULT 'none',investment_amount INTEGER,note TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS income_settings(user_id INTEGER PRIMARY KEY,monthly_salary INTEGER NOT NULL DEFAULT 0,monthly_savings INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS retirement_goals(user_id INTEGER PRIMARY KEY,target_cny INTEGER NOT NULL,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS retirement_goal_items(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,name TEXT NOT NULL,category TEXT NOT NULL,amount INTEGER NOT NULL,currency TEXT NOT NULL DEFAULT 'CNY',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS exchange_rates(currency TEXT PRIMARY KEY,cny_rate REAL NOT NULL,rate_date TEXT NOT NULL,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS exchange_rate_history(id INTEGER PRIMARY KEY AUTOINCREMENT,currency TEXT NOT NULL,cny_rate REAL NOT NULL,rate_date TEXT NOT NULL,source TEXT NOT NULL,fetched_at TEXT NOT NULL,UNIQUE(currency,rate_date));
CREATE TABLE IF NOT EXISTS market_returns(id INTEGER PRIMARY KEY AUTOINCREMENT,category TEXT NOT NULL,code TEXT NOT NULL,lookback_days INTEGER NOT NULL,calculation_date TEXT NOT NULL,annual_rate REAL NOT NULL,period_return REAL NOT NULL,requested_days INTEGER NOT NULL,actual_days INTEGER NOT NULL,history_limited INTEGER NOT NULL DEFAULT 0,start_date TEXT NOT NULL,end_date TEXT NOT NULL,source TEXT NOT NULL,calculated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(category,code,lookback_days,calculation_date));
CREATE TABLE IF NOT EXISTS asset_history(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,snapshot_date TEXT NOT NULL,total_cny INTEGER NOT NULL,trigger TEXT NOT NULL,rate_date TEXT,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(user_id,snapshot_date));
CREATE INDEX IF NOT EXISTS assets_user_id_idx ON assets(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS market_returns_lookup_idx ON market_returns(category,code,lookback_days,calculation_date);`

// ImportLegacy copies the persisted application tables from the previous
// Miniflare/D1 SQLite file. It is safe to run repeatedly: existing rows are
// retained and the source database is never modified.
func ImportLegacy(db *sql.DB, legacyPath string) error {
	// ATTACH is scoped to a SQLite connection, rather than a transaction. Open
	// uses one connection deliberately, so attaching before beginning the copy
	// transaction also makes repeated imports deterministic.
	if _, err := db.Exec("ATTACH DATABASE ? AS legacy", legacyPath); err != nil {
		return err
	}
	defer db.Exec("DETACH DATABASE legacy")
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []struct{ table, statement string }{
		{"users", "INSERT OR IGNORE INTO users(id,email,display_name,password_hash,password_salt,password_iterations,created_at) SELECT id,email,display_name,password_hash,password_salt,password_iterations,created_at FROM legacy.users"},
		{"sessions", "INSERT OR IGNORE INTO sessions(token_hash,user_id,expires_at,created_at) SELECT s.token_hash,s.user_id,s.expires_at,s.created_at FROM legacy.sessions s JOIN users u ON u.id=s.user_id"},
		{"assets", "INSERT OR IGNORE INTO assets(id,user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note,created_at) SELECT a.id,a.user_id,a.name,a.category,a.code,a.amount,a.quantity,a.currency,a.annual_rate,a.investment_strategy,a.investment_amount,a.note,a.created_at FROM legacy.assets a JOIN users u ON u.id=a.user_id"},
		{"income_settings", "INSERT OR IGNORE INTO income_settings(user_id,monthly_salary,monthly_savings,updated_at) SELECT user_id,monthly_salary,monthly_savings,updated_at FROM legacy.income_settings"},
		{"retirement_goals", "INSERT OR IGNORE INTO retirement_goals(user_id,target_cny,updated_at) SELECT g.user_id,g.target_cny,g.updated_at FROM legacy.retirement_goals g JOIN users u ON u.id=g.user_id"},
		{"retirement_goal_items", "INSERT OR IGNORE INTO retirement_goal_items(id,user_id,name,category,amount,currency,created_at) SELECT i.id,i.user_id,i.name,i.category,i.amount,i.currency,i.created_at FROM legacy.retirement_goal_items i JOIN users u ON u.id=i.user_id"},
		{"exchange_rates", "INSERT OR IGNORE INTO exchange_rates(currency,cny_rate,rate_date,updated_at) SELECT currency,cny_rate,rate_date,updated_at FROM legacy.exchange_rates"},
		{"exchange_rate_history", "INSERT OR IGNORE INTO exchange_rate_history(id,currency,cny_rate,rate_date,source,fetched_at) SELECT id,currency,cny_rate,rate_date,source,fetched_at FROM legacy.exchange_rate_history"},
		{"asset_history", "INSERT OR IGNORE INTO asset_history(id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at) SELECT h.id,h.user_id,h.snapshot_date,h.total_cny,h.trigger,h.rate_date,h.created_at,h.updated_at FROM legacy.asset_history h JOIN users u ON u.id=h.user_id"},
		{"market_returns", "INSERT OR IGNORE INTO market_returns(id,category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source,calculated_at) SELECT id,category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source,calculated_at FROM legacy.market_returns"},
	}
	for _, entry := range statements {
		var exists int
		if err := tx.QueryRow("SELECT 1 FROM legacy.sqlite_master WHERE type='table' AND name=?", entry.table).Scan(&exists); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return err
		}
		if _, err = tx.Exec(entry.statement); err != nil {
			return fmt.Errorf("import legacy table %s: %w", entry.table, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = db.Exec("DETACH DATABASE legacy"); err != nil {
		return err
	}
	return nil
}
