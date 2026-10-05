// 数据库连接与当前表结构：初始化 SQLite 数据库并校验现有数据库字段。
// Package database owns SQLite connections and the current schema.
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// Open creates the local database with the current schema.
// Open 打开 SQLite 数据库，并初始化应用所需的表结构。
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "fulibu.db")+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var tables int
	if err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		db.Close()
		return nil, err
	}
	// Validate existing databases before executing any schema writes.
	if tables > 0 {
		for _, query := range []string{
			"SELECT id,email,display_name,password_hash,password_salt,password_iterations,created_at FROM users LIMIT 0",
			"SELECT token_hash,user_id,expires_at,created_at FROM sessions LIMIT 0",
			"SELECT id,user_id,name,token_hash,scope,expires_at,created_at FROM api_tokens LIMIT 0",
			"SELECT user_id,operation,request_key,fingerprint,status,response,created_at FROM mutation_requests LIMIT 0",
			"SELECT id,user_id,operation,request_key,response,created_at FROM operation_logs LIMIT 0",
			"SELECT id,user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note,created_at,version,archived_at FROM assets LIMIT 0",
			"SELECT user_id,monthly_salary,monthly_savings,annual_bonus,updated_at,compensation,version FROM income_settings LIMIT 0",
			"SELECT id,user_id,name,category,amount,currency,created_at,version FROM retirement_goal_items LIMIT 0",
			"SELECT currency,cny_rate,rate_date,updated_at FROM exchange_rates LIMIT 0",
			"SELECT id,currency,cny_rate,rate_date,source,fetched_at FROM exchange_rate_history LIMIT 0",
			"SELECT id,category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source,calculated_at FROM market_returns LIMIT 0",
			"SELECT id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at FROM asset_history LIMIT 0",
		} {
			rows, err := db.Query(query)
			if err != nil {
				db.Close()
				return nil, fmt.Errorf("database requires the current schema: %w", err)
			}
			rows.Close()
		}
	}
	if _, err = db.Exec(schema + marketCacheSchema); err != nil {
		db.Close()
		return nil, err
	}
	for _, table := range []string{"assets", "income_settings", "retirement_goal_items"} {
		key := "id"
		if table == "income_settings" {
			key = "user_id"
		}
		_, err = db.Exec(fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s_version AFTER UPDATE ON %s WHEN NEW.version=OLD.version BEGIN UPDATE %s SET version=OLD.version+1 WHERE %s=NEW.%s; END`, table, table, table, key, key))
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

const schema = `PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS api_tokens(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,name TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,scope TEXT NOT NULL CHECK(scope IN ('read','write')),expires_at INTEGER NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS mutation_requests(user_id INTEGER NOT NULL,operation TEXT NOT NULL,request_key TEXT NOT NULL,fingerprint TEXT NOT NULL,status INTEGER NOT NULL,response TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,PRIMARY KEY(user_id,operation,request_key),FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS operation_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,operation TEXT NOT NULL,request_key TEXT NOT NULL,response TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT NOT NULL UNIQUE,display_name TEXT NOT NULL,password_hash TEXT NOT NULL,password_salt TEXT NOT NULL,password_iterations INTEGER NOT NULL DEFAULT 210000,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL,expires_at INTEGER NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS assets(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER,name TEXT NOT NULL,category TEXT NOT NULL,code TEXT,amount INTEGER NOT NULL,quantity REAL,currency TEXT NOT NULL DEFAULT 'CNY',annual_rate REAL NOT NULL DEFAULT 0,investment_strategy TEXT NOT NULL DEFAULT 'none',investment_amount INTEGER,note TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,archived_at TEXT,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS income_settings(user_id INTEGER PRIMARY KEY,monthly_salary INTEGER NOT NULL DEFAULT 0,monthly_savings INTEGER NOT NULL DEFAULT 0,annual_bonus INTEGER NOT NULL DEFAULT 0,compensation TEXT NOT NULL DEFAULT '{}',version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS retirement_goal_items(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,name TEXT NOT NULL,category TEXT NOT NULL,amount INTEGER NOT NULL,currency TEXT NOT NULL DEFAULT 'CNY',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS exchange_rates(currency TEXT PRIMARY KEY,cny_rate REAL NOT NULL,rate_date TEXT NOT NULL,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS exchange_rate_history(id INTEGER PRIMARY KEY AUTOINCREMENT,currency TEXT NOT NULL,cny_rate REAL NOT NULL,rate_date TEXT NOT NULL,source TEXT NOT NULL,fetched_at TEXT NOT NULL,UNIQUE(currency,rate_date));
CREATE TABLE IF NOT EXISTS market_returns(id INTEGER PRIMARY KEY AUTOINCREMENT,category TEXT NOT NULL,code TEXT NOT NULL,lookback_days INTEGER NOT NULL,calculation_date TEXT NOT NULL,annual_rate REAL NOT NULL,period_return REAL NOT NULL,requested_days INTEGER NOT NULL,actual_days INTEGER NOT NULL,history_limited INTEGER NOT NULL DEFAULT 0,start_date TEXT NOT NULL,end_date TEXT NOT NULL,source TEXT NOT NULL,calculated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(category,code,lookback_days,calculation_date));
CREATE TABLE IF NOT EXISTS asset_history(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,snapshot_date TEXT NOT NULL,total_cny INTEGER NOT NULL,trigger TEXT NOT NULL,rate_date TEXT,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,UNIQUE(user_id,snapshot_date));
CREATE INDEX IF NOT EXISTS assets_user_id_idx ON assets(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS market_returns_lookup_idx ON market_returns(category,code,lookback_days,calculation_date);`
