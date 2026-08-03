import { env } from "cloudflare:workers";

export type AssetRow = {
  id: number;
  user_id: number | null;
  name: string;
  category: string;
  code: string | null;
  amount: number;
  currency: string;
  annual_rate: number;
  note: string;
  created_at: string;
};

let initialization: Promise<void> | null = null;

async function initialize(db: D1Database) {
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
        currency TEXT NOT NULL DEFAULT 'CNY',
        annual_rate REAL NOT NULL DEFAULT 0,
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
  ]);

  const columns = await db.prepare("PRAGMA table_info(assets)").all<{ name: string }>();
  if (!columns.results.some((column) => column.name === "user_id")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE").run();
  }
  if (!columns.results.some((column) => column.name === "currency")) {
    await db.prepare("ALTER TABLE assets ADD COLUMN currency TEXT NOT NULL DEFAULT 'CNY'").run();
  }

  await db.batch([
    db.prepare("CREATE INDEX IF NOT EXISTS assets_category_idx ON assets (category)"),
    db.prepare("CREATE INDEX IF NOT EXISTS assets_user_id_idx ON assets (user_id)"),
    db.prepare("CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id)"),
    db.prepare("CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at)"),
  ]);
}

export async function getAssetsDb() {
  const db = env.DB;
  if (!db) throw new Error("本地 SQLite 数据库暂不可用");
  initialization ??= initialize(db);
  await initialization;
  return db;
}
