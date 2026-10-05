package database

// Each observation retains the provider's date; fetch time never masquerades
// as a trading date. Cache metadata extends the legacy returns contract.
const marketCacheSchema = `
CREATE TABLE IF NOT EXISTS market_daily_prices(category TEXT NOT NULL,code TEXT NOT NULL,price_date TEXT NOT NULL,price REAL NOT NULL,return_price REAL NOT NULL,income REAL NOT NULL DEFAULT 0,currency TEXT NOT NULL,source TEXT NOT NULL,fetched_at TEXT NOT NULL,PRIMARY KEY(category,code,price_date));
CREATE TABLE IF NOT EXISTS market_quotes(category TEXT NOT NULL,code TEXT NOT NULL,price REAL NOT NULL,currency TEXT NOT NULL,price_date TEXT NOT NULL,source TEXT NOT NULL,fetched_at TEXT NOT NULL,PRIMARY KEY(category,code));
CREATE TABLE IF NOT EXISTS market_sync_state(category TEXT NOT NULL,code TEXT NOT NULL,covered_from TEXT NOT NULL DEFAULT '',covered_to TEXT NOT NULL DEFAULT '',inception_known INTEGER NOT NULL DEFAULT 0,input_version INTEGER NOT NULL DEFAULT 0,checked_at TEXT NOT NULL DEFAULT '',full_checked_at TEXT NOT NULL DEFAULT '',last_success TEXT NOT NULL DEFAULT '',last_error TEXT NOT NULL DEFAULT '',retry_after TEXT NOT NULL DEFAULT '',PRIMARY KEY(category,code));
CREATE TABLE IF NOT EXISTS market_return_cache(category TEXT NOT NULL,code TEXT NOT NULL,lookback_days INTEGER NOT NULL,calculation_date TEXT NOT NULL,input_version INTEGER NOT NULL,payload TEXT NOT NULL,calculated_at TEXT NOT NULL,PRIMARY KEY(category,code,lookback_days,calculation_date));
CREATE TABLE IF NOT EXISTS asset_daily_snapshots(user_id INTEGER NOT NULL,asset_id INTEGER NOT NULL,snapshot_date TEXT NOT NULL,amount INTEGER NOT NULL,quantity REAL NOT NULL,currency TEXT NOT NULL,annual_rate REAL NOT NULL,price_date TEXT NOT NULL,source TEXT NOT NULL,fetched_at TEXT NOT NULL,PRIMARY KEY(user_id,asset_id,snapshot_date));
CREATE TABLE IF NOT EXISTS market_refresh_runs(run_date TEXT NOT NULL,slot TEXT NOT NULL,job_state TEXT NOT NULL,started_at TEXT NOT NULL,finished_at TEXT NOT NULL DEFAULT '',success_count INTEGER NOT NULL DEFAULT 0,failure_count INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',PRIMARY KEY(run_date,slot));
`
