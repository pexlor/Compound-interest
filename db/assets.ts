import { env } from "cloudflare:workers";

export type AssetRow = {
  id: number;
  user_id: number | null;
  name: string;
  category: string;
  code: string | null;
  amount: number;
  quantity: number | null;
  currency: string;
  annual_rate: number;
  investment_strategy: string;
  investment_amount: number | null;
  note: string;
  created_at: string;
};

let initialization: Promise<void> | null = null;

export async function initializeAssetsDb(db: D1Database) {
  await db.batch([
    db.prepare(`
      CREATE TABLE IF NOT EXISTS users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        email TEXT NOT NULL UNIQUE,
        display_name TEXT NOT NULL,
        password_hash TEXT NOT NULL,
        password_salt TEXT NOT NULL,
        password_iterations INTEGER NOT NULL DEFAULT 210000,
        created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS assets (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER,
        name TEXT NOT NULL,
        category TEXT NOT NULL,
        code TEXT,
        amount INTEGER NOT NULL,
        quantity REAL,
        currency TEXT NOT NULL DEFAULT 'CNY',
        annual_rate REAL NOT NULL DEFAULT 0,
        investment_strategy TEXT NOT NULL DEFAULT 'none',
        investment_amount INTEGER,
        note TEXT NOT NULL DEFAULT '',
        created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS sessions (
        token_hash TEXT PRIMARY KEY,
        user_id INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS income_settings (
        user_id INTEGER PRIMARY KEY,
        monthly_salary INTEGER NOT NULL DEFAULT 0,
        monthly_savings INTEGER NOT NULL DEFAULT 0,
        updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS exchange_rates (
        currency TEXT PRIMARY KEY,
        cny_rate REAL NOT NULL,
        rate_date TEXT NOT NULL,
        updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS exchange_rate_history (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        currency TEXT NOT NULL,
        cny_rate REAL NOT NULL,
        rate_date TEXT NOT NULL,
        source TEXT NOT NULL,
        fetched_at TEXT NOT NULL,
        UNIQUE (currency, rate_date)
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS asset_history (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL,
        snapshot_date TEXT NOT NULL,
        total_cny INTEGER NOT NULL,
        trigger TEXT NOT NULL,
        rate_date TEXT,
        created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
        UNIQUE (user_id, snapshot_date)
      )
    `),
    db.prepare(`
      CREATE TABLE IF NOT EXISTS market_returns (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        category TEXT NOT NULL,
        code TEXT NOT NULL,
        lookback_days INTEGER NOT NULL,
        calculation_date TEXT NOT NULL,
        annual_rate REAL NOT NULL,
        period_return REAL NOT NULL,
        requested_days INTEGER NOT NULL,
        actual_days INTEGER NOT NULL,
        history_limited INTEGER NOT NULL DEFAULT 0,
        start_date TEXT NOT NULL,
        end_date TEXT NOT NULL,
        source TEXT NOT NULL,
        calculated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        UNIQUE (category, code, lookback_days, calculation_date)
      )
    `),
  ]);

  const columns = await db.prepare("PRAGMA table_info(assets)").all<{ name: string }>();
  if (!columns.results.some((column) => column.name === "user_id")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE").run();
  }
  if (!columns.results.some((column) => column.name === "currency")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN currency TEXT NOT NULL DEFAULT 'CNY'").run();
  }
  if (!columns.results.some((column) => column.name === "quantity")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN quantity REAL").run();
  }
  if (!columns.results.some((column) => column.name === "investment_strategy")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN investment_strategy TEXT NOT NULL DEFAULT 'none'").run();
  }
  if (!columns.results.some((column) => column.name === "investment_amount")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN investment_amount INTEGER").run();
  }

  await db.batch([
    db.prepare("CREATE INDEX IF NOT EXISTS assets_category_idx ON assets (category)"),
    db.prepare("CREATE INDEX IF NOT EXISTS assets_user_id_idx ON assets (user_id)"),
    db.prepare("CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id)"),
    db.prepare("CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at)"),
    db.prepare("CREATE INDEX IF NOT EXISTS asset_history_user_id_idx ON asset_history (user_id)"),
    db.prepare("CREATE INDEX IF NOT EXISTS exchange_rate_history_lookup_idx ON exchange_rate_history (currency, rate_date)"),
    db.prepare("CREATE INDEX IF NOT EXISTS market_returns_lookup_idx ON market_returns (category, code, lookback_days, calculation_date)"),
  ]);
}

export async function getAssetsDb() {
  const db = env.DB;
  if (!db) throw new Error("本地 SQLite 数据库暂不可用");
  initialization ??= initializeAssetsDb(db);
  await initialization;
  return db;
}
