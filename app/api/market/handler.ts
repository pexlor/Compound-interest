type FundPoint = { FSRQ: string; DWJZ: string; LJJZ: string };
type User = { id: number };
type MarketResult = {
  annualRate: number;
  periodReturn: number;
  startDate: string | object;
  endDate: string | object;
  source: string;
};
type Dependencies = {
  authenticate(request: Request): Promise<User | null>;
  fetch: typeof fetch;
  timeoutMs?: number;
  maxConcurrent?: number;
  cacheTtlMs?: number;
  now?: () => number;
};

const supportedCategories = new Set(["stock", "fund", "money"]);

function domesticStockSymbol(code: string) {
  if (/^(5|6|9)/.test(code)) return `sh${code}`;
  if (/^(0|1|2|3)/.test(code)) return `sz${code}`;
  return code;
}

function isUsSecurityCode(code: string) {
  return /^[A-Z][A-Z0-9.-]{0,14}$/.test(code);
}

function annualize(start: number, end: number, days: number) {
  if (start <= 0 || end <= 0 || days <= 0) return 0;
  return (Math.pow(end / start, 365 / days) - 1) * 100;
}

function createLimiter(limit: number) {
  let active = 0;
  const waiting: Array<() => void> = [];
  return async function run<T>(operation: () => Promise<T>): Promise<T> {
    if (active >= limit) await new Promise<void>((resolve) => waiting.push(resolve));
    active += 1;
    try {
      return await operation();
    } finally {
      active -= 1;
      waiting.shift()?.();
    }
  };
}

export function createMarketHandler(dependencies: Dependencies) {
  const timeoutMs = dependencies.timeoutMs ?? 8000;
  const now = dependencies.now ?? Date.now;
  const cacheTtlMs = dependencies.cacheTtlMs ?? 10 * 60 * 1000;
  const runLimited = createLimiter(Math.max(1, dependencies.maxConcurrent ?? 4));
  const cache = new Map<string, { expiresAt: number; value: MarketResult }>();
  const inFlight = new Map<string, Promise<MarketResult>>();

  async function upstreamFetch(url: string) {
    return runLimited(async () => {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(new DOMException("行情请求超时", "TimeoutError")), timeoutMs);
      try {
        return await dependencies.fetch(url, {
          headers: { "User-Agent": "Mozilla/5.0", Referer: "https://fundf10.eastmoney.com/" },
          signal: controller.signal,
        });
      } finally {
        clearTimeout(timer);
      }
    });
  }

  async function resolveStockSymbol(code: string) {
    if (!isUsSecurityCode(code)) return domesticStockSymbol(code);
    const lookupSymbol = `us${code}`;
    const lookupUrl = `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${encodeURIComponent(lookupSymbol)},day,,,2,qfq`;
    const response = await upstreamFetch(lookupUrl);
    if (!response.ok) throw new Error("行情服务暂时不可用");
    const json = (await response.json()) as { data?: Record<string, { qt?: Record<string, (string | number)[]> }> };
    const resolvedCode = String(json.data?.[lookupSymbol]?.qt?.[lookupSymbol]?.[2] ?? "").trim();
    return resolvedCode ? `us${resolvedCode}` : lookupSymbol;
  }

  async function stockReturn(code: string, days: number): Promise<MarketResult> {
    const count = Math.min(2000, Math.max(30, Math.ceil(days * 0.75)));
    const isUsSecurity = isUsSecurityCode(code);
    const symbol = await resolveStockSymbol(code);
    const rawSymbol = isUsSecurity ? `us${code}` : symbol;
    const candidates = symbol === rawSymbol ? [symbol] : [symbol, rawSymbol];
    let selected: { rows: (string | object)[][]; actualDays: number } | null = null;
    let lastError: unknown = null;
    for (const candidate of candidates) {
      try {
        const url = `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${encodeURIComponent(candidate)},day,,,${count},qfq`;
        const response = await upstreamFetch(url);
        if (!response.ok) throw new Error("行情服务暂时不可用");
        const json = (await response.json()) as { data?: Record<string, { qfqday?: (string | object)[][]; day?: (string | object)[][] }> };
        const rows = json.data?.[candidate]?.qfqday ?? json.data?.[candidate]?.day ?? [];
        if (rows.length < 2) throw new Error("没有找到这个股票代码的历史行情");
        const actualDays = Math.max(1, (Date.parse(String(rows.at(-1)?.[0])) - Date.parse(String(rows[0][0]))) / 86400000);
        selected = { rows, actualDays };
        if (!isUsSecurity || actualDays >= days * 0.9) break;
      } catch (error) {
        lastError = error;
      }
    }
    if (!selected) throw (lastError instanceof Error ? lastError : new Error("没有找到这个股票代码的历史行情"));
    const first = selected.rows[0];
    const last = selected.rows.at(-1)!;
    const start = Number(first[2]);
    const end = Number(last[2]);
    return {
      annualRate: annualize(start, end, selected.actualDays),
      periodReturn: (end / start - 1) * 100,
      startDate: first[0], endDate: last[0],
      source: isUsSecurity ? "腾讯证券美股历史行情" : "腾讯证券历史复权行情",
    };
  }

  async function fundReturn(code: string, days: number, isMoney: boolean): Promise<MarketResult> {
    const pageSize = 100;
    const desiredPoints = isMoney ? 20 : Math.min(4000, Math.max(30, days + 30));
    const pageCount = isMoney ? 1 : Math.ceil(desiredPoints / pageSize);
    const pages = await Promise.all(Array.from({ length: pageCount }, async (_, index) => {
      const url = `https://api.fund.eastmoney.com/f10/lsjz?fundCode=${encodeURIComponent(code)}&pageIndex=${index + 1}&pageSize=${pageSize}`;
      const response = await upstreamFetch(url);
      if (!response.ok) throw new Error("基金数据服务暂时不可用");
      return (await response.json()) as { Data?: { LSJZList?: FundPoint[]; SYType?: string } };
    }));
    const points = pages.flatMap((page) => page.Data?.LSJZList ?? []);
    const yieldType = pages[0]?.Data?.SYType;
    if (points.length < 2) throw new Error("没有找到这个基金代码的数据");

    if (isMoney || yieldType?.includes("每万份收益")) {
      const sample = points.slice(0, 7);
      const averageDaily = sample.reduce((sum, point) => sum + (Number(point.DWJZ) || 0), 0) / sample.length;
      const annualRate = averageDaily * 3.65;
      return {
        annualRate, periodReturn: annualRate,
        startDate: sample[sample.length - 1].FSRQ, endDate: sample[0].FSRQ,
        source: "东方财富每万份收益折算",
      };
    }

    const latest = points[0];
    const oldest = points[points.length - 1];
    const start = Number(oldest.LJJZ) || Number(oldest.DWJZ);
    const end = Number(latest.LJJZ) || Number(latest.DWJZ);
    const actualDays = Math.max(1, (Date.parse(latest.FSRQ) - Date.parse(oldest.FSRQ)) / 86400000);
    return {
      annualRate: annualize(start, end, actualDays), periodReturn: (end / start - 1) * 100,
      startDate: oldest.FSRQ, endDate: latest.FSRQ, source: "东方财富历史净值",
    };
  }

  return async function GET(request: Request) {
    const user = await dependencies.authenticate(request);
    if (!user) return Response.json({ error: "请先登录" }, { status: 401 });
    const params = new URL(request.url).searchParams;
    const rawCode = params.get("code")?.trim() ?? "";
    const code = /^[a-z]/i.test(rawCode) ? rawCode.toUpperCase() : rawCode;
    const category = params.get("category") ?? "stock";
    const parsedDays = Number(params.get("days"));
    const days = Math.round(Math.min(3650, Math.max(30, Number.isFinite(parsedDays) ? parsedDays : 365)));
    if (!code || code.length > 32) return Response.json({ error: "请输入有效代码" }, { status: 400 });
    if (!supportedCategories.has(category)) return Response.json({ error: "不支持这个资产类别" }, { status: 400 });

    const key = `${category}:${code}:${days}`;
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now()) return Response.json({ ...cached.value, cached: true });
    try {
      let pending = inFlight.get(key);
      if (!pending) {
        pending = category === "stock" || (category === "fund" && (/^[15]/.test(code) || isUsSecurityCode(code)))
          ? stockReturn(code, days)
          : fundReturn(code, days, category === "money");
        inFlight.set(key, pending);
      }
      const result = await pending;
      cache.set(key, { value: result, expiresAt: now() + cacheTtlMs });
      return Response.json({ ...result, cached: false });
    } catch (error) {
      const timedOut = error instanceof Error && (error.name === "TimeoutError" || error.name === "AbortError");
      return Response.json(
        { error: timedOut ? "行情请求超时，请稍后重试" : error instanceof Error ? error.message : "读取行情失败" },
        { status: timedOut ? 504 : 502 }
      );
    } finally {
      inFlight.delete(key);
    }
  };
}
