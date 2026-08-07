type Dependencies = {
  getAuthenticatedUser(request: Request): Promise<{ id: number } | null>;
  getAssetsDb(): Promise<D1Database>;
  refreshUser(db: D1Database, userId: number): Promise<{ recordedSnapshots: number; errors?: unknown[]; [key: string]: unknown }>;
};

export function createDailyRefreshHandler(dependencies: Dependencies) {
  return async function POST(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return Response.json({ error: "请先登录" }, { status: 401 });
      const db = await dependencies.getAssetsDb();
      const result = await dependencies.refreshUser(db, user.id);
      if (result.recordedSnapshots < 1) throw new Error("当天资产快照写入失败");
      if (result.errors?.length) throw new Error("部分市场价格刷新失败，重新打开将自动重试");
      return Response.json({ refreshed: true, result });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "每日刷新失败" }, { status: 500 });
    }
  };
}
