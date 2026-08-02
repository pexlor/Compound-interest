import { env } from "cloudflare:workers";

export type AssetRow = {
  id: number;
  name: string;
  category: string;
  code: string | null;
  amount: number;
  annual_rate: number;
  note: string;
  created_at: string;
};

export async function getAssetsDb() {
  const db = env.DB;
  if (!db) throw new Error("资产数据库暂不可用");

  await db.prepare(`
    CREATE TABLE IF NOT EXISTS assets (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      name TEXT NOT NULL,
      category TEXT NOT NULL,
      code TEXT,
      amount INTEGER NOT NULL,
      annual_rate REAL NOT NULL DEFAULT 0,
      note TEXT NOT NULL DEFAULT '',
      created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
    )
  `).run();
  await db.prepare(
    "CREATE INDEX IF NOT EXISTS assets_category_idx ON assets (category)"
  ).run();
  return db;
}
