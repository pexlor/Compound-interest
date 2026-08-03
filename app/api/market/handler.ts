import { createMarketCalculator } from "./calculator.ts";
import type { MarketCalculation } from "./calculator.ts";
import { LOOKBACK_DAYS } from "./market-return-service.ts";
import type { createMarketReturnService } from "./market-return-service.ts";

type User = { id: number };
type Dependencies = {
  authenticate(request: Request): Promise<User | null>;
  fetch?: typeof fetch;
  getService?: () => Promise<ReturnType<typeof createMarketReturnService>>;
  timeoutMs?: number;
  maxConcurrent?: number;
  cacheTtlMs?: number;
  now?: () => number;
};

const supportedCategories = new Set(["stock", "fund", "money"]);

export function createMarketHandler(dependencies: Dependencies) {
  const now = dependencies.now ?? Date.now;
  const cacheTtlMs = dependencies.cacheTtlMs ?? 10 * 60 * 1000;
  const calculator = dependencies.fetch ? createMarketCalculator({
    fetch: dependencies.fetch,
    timeoutMs: dependencies.timeoutMs,
    maxConcurrent: dependencies.maxConcurrent,
  }) : null;
  const cache = new Map<string, { expiresAt: number; value: MarketCalculation }>();
  const inFlight = new Map<string, Promise<MarketCalculation>>();

  return async function GET(request: Request) {
    const user = await dependencies.authenticate(request);
    if (!user) return Response.json({ error: "请先登录" }, { status: 401 });
    const params = new URL(request.url).searchParams;
    const rawCode = params.get("code")?.trim() ?? "";
    const code = /^[a-z]/i.test(rawCode) ? rawCode.toUpperCase() : rawCode;
    const category = params.get("category") ?? "stock";
    const rawDays = params.get("days");
    const days = rawDays === null ? 365 : Number(rawDays);
    if (!Number.isInteger(days) || !LOOKBACK_DAYS.includes(days as (typeof LOOKBACK_DAYS)[number])) {
      return Response.json({ error: "不支持的历史区间" }, { status: 400 });
    }
    if (!supportedCategories.has(category)) return Response.json({ error: "不支持这个资产类别" }, { status: 400 });

    if (dependencies.getService) {
      try {
        const service = await dependencies.getService();
        if (!code) return Response.json(await service.getForUser(user.id, days));
        if (code.length > 32) return Response.json({ error: "请输入有效代码" }, { status: 400 });
        return Response.json(await service.get(category, code, days));
      } catch (error) {
        return Response.json({ error: error instanceof Error ? error.message : "读取行情失败" }, { status: 502 });
      }
    }

    if (!code || code.length > 32) return Response.json({ error: "请输入有效代码" }, { status: 400 });
    if (!calculator) return Response.json({ error: "行情服务暂不可用" }, { status: 503 });

    const key = `${category}:${code}:${days}`;
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now()) return Response.json({ ...cached.value, cached: true });
    try {
      let pending = inFlight.get(key);
      if (!pending) {
        pending = calculator.calculate(category, code, days);
        inFlight.set(key, pending);
      }
      const result = await pending;
      cache.set(key, { value: result, expiresAt: now() + cacheTtlMs });
      return Response.json({ ...result, cached: false });
    } catch (error) {
      const timedOut = error instanceof Error && (error.name === "TimeoutError" || error.name === "AbortError");
      return Response.json(
        { error: timedOut ? "行情请求超时，请稍后重试" : error instanceof Error ? error.message : "读取行情失败" },
        { status: timedOut ? 504 : 502 },
      );
    } finally {
      inFlight.delete(key);
    }
  };
}
