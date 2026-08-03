const foreignCurrencies = ["USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"] as const;

type RateRow = { date: string; base: string; quote: string; rate: number };
type RatesPayload = { rates: Record<string, number>; date: string; source: string; fetchedAt: string };
type Dependencies = {
  authenticate(request: Request): Promise<{ id: number } | null>;
  fetch: typeof fetch;
  getAssetsDb?(): Promise<D1Database>;
  saveExchangeRates?(db: D1Database, rates: Record<string, number>, rateDate: string): Promise<void>;
  recordDailySnapshot?(db: D1Database, userId: number, trigger: "exchange_refresh"): Promise<unknown>;
  cacheTtlMs?: number;
  timeoutMs?: number;
  now?: () => number;
};

export function createExchangeRatesHandler(dependencies: Dependencies) {
  const now = dependencies.now ?? Date.now;
  const cacheTtlMs = dependencies.cacheTtlMs ?? 15 * 60 * 1000;
  const timeoutMs = dependencies.timeoutMs ?? 5000;
  let cache: (RatesPayload & { expiresAt: number }) | null = null;

  async function upstreamFetch(url: URL | string) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(new DOMException("汇率请求超时", "TimeoutError")), timeoutMs);
    try {
      return await dependencies.fetch(url, { headers: { Accept: "application/json" }, signal: controller.signal });
    } finally {
      clearTimeout(timer);
    }
  }

  async function fetchFrankfurterRates(): Promise<RatesPayload> {
    const endpoint = new URL("https://api.frankfurter.dev/v2/rates");
    endpoint.searchParams.set("base", "CNY");
    endpoint.searchParams.set("quotes", foreignCurrencies.join(","));
    const response = await upstreamFetch(endpoint);
    if (!response.ok) throw new Error("最新汇率服务暂不可用");
    const rows = await response.json() as RateRow[];
    const rates: Record<string, number> = { CNY: 1 };
    for (const currency of foreignCurrencies) {
      const row = rows.find((item) => item.base === "CNY" && item.quote === currency);
      if (!row || !Number.isFinite(row.rate) || row.rate <= 0) throw new Error(`未取得 ${currency} 的最新汇率`);
      rates[currency] = 1 / row.rate;
    }
    return {
      rates,
      date: rows.map((row) => row.date).filter(Boolean).sort().at(-1) || "",
      source: "Frankfurter 央行参考汇率",
      fetchedAt: new Date(now()).toISOString(),
    };
  }

  async function fetchBackupRates(): Promise<RatesPayload> {
    const response = await upstreamFetch("https://api.exchangerate-api.com/v4/latest/CNY");
    if (!response.ok) throw new Error("备用汇率服务暂不可用");
    const payload = await response.json() as { base?: string; date?: string; rates?: Record<string, number> };
    if (payload.base !== "CNY" || !payload.rates) throw new Error("备用汇率数据格式异常");
    const rates: Record<string, number> = { CNY: 1 };
    for (const currency of foreignCurrencies) {
      const rate = payload.rates[currency];
      if (!Number.isFinite(rate) || rate <= 0) throw new Error(`未取得 ${currency} 的备用汇率`);
      rates[currency] = 1 / rate;
    }
    return {
      rates,
      date: payload.date || "",
      source: "ExchangeRate-API 备用汇率",
      fetchedAt: new Date(now()).toISOString(),
    };
  }

  async function fetchLatestRates() {
    try {
      return await fetchFrankfurterRates();
    } catch {
      return fetchBackupRates();
    }
  }

  return async function GET(request: Request) {
    const user = await dependencies.authenticate(request);
    if (!user) return Response.json({ error: "请先登录" }, { status: 401 });
    const force = new URL(request.url).searchParams.get("refresh") === "1";
    if (!force && cache && cache.expiresAt > now()) return Response.json({ ...cache, expiresAt: undefined, cached: true });
    try {
      const payload = await fetchLatestRates();
      cache = { ...payload, expiresAt: now() + cacheTtlMs };
      let snapshot: unknown = null;
      if (dependencies.getAssetsDb && dependencies.saveExchangeRates) {
        const db = await dependencies.getAssetsDb();
        await dependencies.saveExchangeRates(db, payload.rates, payload.date);
        if (force && dependencies.recordDailySnapshot) {
          snapshot = await dependencies.recordDailySnapshot(db, user.id, "exchange_refresh");
        }
      }
      return Response.json({ ...payload, cached: false, snapshot });
    } catch (error) {
      if (cache) return Response.json({ ...cache, expiresAt: undefined, cached: true, stale: true });
      return Response.json({ error: error instanceof Error ? error.message : "读取最新汇率失败" }, { status: 502 });
    }
  };
}
