import { getAssetsDb, type AssetRow } from "../../../db/assets";
import { getAuthenticatedUser } from "../../../db/auth";
import { readLatestExchangeRates } from "../../../db/exchange-rate-history";
import type { HistoryRow } from "../../../db/history";

const HISTORY_LIMIT = 3650;

export async function GET(request: Request) {
  try {
    const user = await getAuthenticatedUser(request);
    if (!user) return Response.json({ error: "请先登录" }, { status: 401 });

    const db = await getAssetsDb();
    const [assets, history, income, latestRates] = await Promise.all([
      db.prepare("SELECT id, user_id, name, category, code, amount, quantity, currency, annual_rate, investment_strategy, investment_amount, note, created_at FROM assets WHERE user_id = ? ORDER BY id")
        .bind(user.id).all<AssetRow>(),
      db.prepare(`
        SELECT id, user_id, snapshot_date, total_cny, trigger, rate_date, created_at, updated_at
        FROM (
          SELECT id, user_id, snapshot_date, total_cny, trigger, rate_date, created_at, updated_at
          FROM asset_history WHERE user_id = ? ORDER BY snapshot_date DESC LIMIT ?
        ) ORDER BY snapshot_date ASC
      `).bind(user.id, HISTORY_LIMIT).all<HistoryRow>(),
      db.prepare("SELECT monthly_salary, monthly_savings, updated_at FROM income_settings WHERE user_id = ?")
        .bind(user.id).first<{ monthly_salary: number; monthly_savings: number; updated_at: string | null }>(),
      readLatestExchangeRates(db),
    ]);

    return Response.json({
      user,
      assets: assets.results,
      history: history.results,
      income: income ?? { monthly_salary: 0, monthly_savings: 0, updated_at: null },
      rates: { CNY: 1, ...(latestRates?.rates ?? {}) },
      date: latestRates?.date ?? "",
      stale: !latestRates,
    });
  } catch (error) {
    return Response.json({ error: error instanceof Error ? error.message : "读取本地账本失败" }, { status: 500 });
  }
}
