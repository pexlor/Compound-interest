import type { HistoryRow } from "../../../db/history";

type Dependencies = {
  getAuthenticatedUser(request: Request): Promise<{ id: number } | null>;
  getAssetsDb(): Promise<D1Database>;
};

export function createHistoryHandler(dependencies: Dependencies) {
  return async function GET(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return Response.json({ error: "请先登录" }, { status: 401 });
      const parsedLimit = Number(new URL(request.url).searchParams.get("limit"));
      const limit = Math.round(Math.min(3650, Math.max(1, Number.isFinite(parsedLimit) && parsedLimit > 0 ? parsedLimit : 365)));
      const db = await dependencies.getAssetsDb();
      const result = await db.prepare(`
        SELECT id, user_id, snapshot_date, total_cny, trigger, rate_date, created_at, updated_at
        FROM (
          SELECT id, user_id, snapshot_date, total_cny, trigger, rate_date, created_at, updated_at
          FROM asset_history
          WHERE user_id = ?
          ORDER BY snapshot_date DESC
          LIMIT ?
        )
        ORDER BY snapshot_date ASC
      `).bind(user.id, limit).all<HistoryRow>();
      return Response.json({ history: result.results });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "读取资产历史失败" }, { status: 500 });
    }
  };
}
