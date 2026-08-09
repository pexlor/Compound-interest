import { fetchHistoricalUsdCnyRate } from "./historical-rates.ts";
import { createQuoteCache, readCachedQuote, type QuoteCache } from "./quote-cache.ts";

type FundPoint = { FSRQ: string; DWJZ: string; LJJZ: string };
type FundPage = {
  Data?: { LSJZList?: FundPoint[]; SYType?: string };
  TotalCount?: number;
  PageSize?: number;
};

type BnyLiquidityQuote = {
  annualRate: number;
  priceDate: string;
};

const BNY_US_DOLLAR_LIQUIDITY_FUND = "IE0004514828";
const BNY_US_DOLLAR_LIQUIDITY_URL = "https://www.dreyfus.com/products/nra-offshore/fund/bny-mellon-liquidity-funds-plc-bny-mellon-us-dollar-liquidity-fu.shareclass.Institutional%20Shares.html";

export type MarketCalculation = {
  annualRate: number;
  periodReturn: number;
  requestedDays: number;
  actualDays: number;
  historyLimited: boolean;
  startDate: string;
  endDate: string;
  source: string;
};

export type MarketQuote = {
  currentPrice: number;
  priceCurrency: "CNY" | "USD";
  priceDate: string;
};

type CalculatorDependencies = {
  fetch: typeof fetch;
  historicalRate?: (marketDate: string) => Promise<{ date: string; rate: number }>;
  timeoutMs?: number;
  maxConcurrent?: number;
  quoteCache?: QuoteCache;
};

type YahooChartPoint = { date: string; adjustedClose: number };
type YahooChartResponse = {
  chart?: {
    result?: Array<{
      timestamp?: number[];
      indicators?: { adjclose?: Array<{ adjclose?: Array<number | null> }> };
    }>;
    error?: { description?: string } | null;
  };
};

function domesticStockSymbol(code: string) {
  if (/^(5|6|9)/.test(code)) return `sh${code}`;
  if (/^(0|1|2|3)/.test(code)) return `sz${code}`;
  return code;
}

export function isUsSecurityCode(code: string) {
  return /^[A-Z][A-Z0-9.-]{0,14}$/.test(code);
}

function annualize(start: number, end: number, days: number) {
  if (start <= 0 || end <= 0 || days <= 0) return 0;
  return (Math.pow(end / start, 365 / days) - 1) * 100;
}

function pointAtLookback<T>(points: T[], latestDate: string | object, days: number, getDate: (point: T) => string | object) {
  const targetTime = Date.parse(String(latestDate)) - days * 86400000;
  let oldest = points[0];
  let oldestTime = Number.POSITIVE_INFINITY;
  let selected: T | undefined;
  let selectedTime = Number.NEGATIVE_INFINITY;
  for (const point of points) {
    const time = Date.parse(String(getDate(point)));
    if (!Number.isFinite(time)) continue;
    if (time < oldestTime) { oldest = point; oldestTime = time; }
    if (time <= targetTime && time > selectedTime) { selected = point; selectedTime = time; }
  }
  return selected ?? oldest;
}

function dateBefore(date: string | object, days: number) {
  return new Date(Date.parse(String(date)) - days * 86400000).toISOString().slice(0, 10);
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

export function createMarketCalculator(dependencies: CalculatorDependencies) {
  const timeoutMs = dependencies.timeoutMs ?? 8000;
  const historicalTimeoutMs = Math.min(timeoutMs, 4000);
  const runLimited = createLimiter(Math.max(1, dependencies.maxConcurrent ?? 4));
  const quoteCache = dependencies.quoteCache ?? createQuoteCache();

  async function upstreamFetch(url: string, requestTimeoutMs = timeoutMs) {
    return runLimited(async () => {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(new DOMException("行情请求超时", "TimeoutError")), requestTimeoutMs);
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

  async function usStockReturn(code: string, days: number): Promise<MarketCalculation> {
    const secondsPerDay = 86400;
    const period2 = Math.floor(Date.now() / 1000) + secondsPerDay;
    const period1 = period2 - (days + 45) * secondsPerDay;
    const yahooSymbol = code.replaceAll(".", "-");
    const params = new URLSearchParams({
      period1: String(period1),
      period2: String(period2),
      interval: "1d",
      events: "div,splits",
      includeAdjustedClose: "true",
    });
    const response = await upstreamFetch(`https://query1.finance.yahoo.com/v8/finance/chart/${encodeURIComponent(yahooSymbol)}?${params}`);
    if (!response.ok) throw new Error("美股复权行情服务暂时不可用");
    const json = (await response.json()) as YahooChartResponse;
    const result = json.chart?.result?.[0];
    const timestamps = result?.timestamp ?? [];
    const adjustedCloses = result?.indicators?.adjclose?.[0]?.adjclose ?? [];
    const points: YahooChartPoint[] = timestamps.flatMap((timestamp, index) => {
      const adjustedClose = adjustedCloses[index];
      if (!Number.isFinite(adjustedClose) || Number(adjustedClose) <= 0) return [];
      return [{
        date: new Date(timestamp * 1000).toISOString().slice(0, 10),
        adjustedClose: Number(adjustedClose),
      }];
    });
    if (points.length < 1) {
      throw new Error(json.chart?.error?.description || "没有找到这个股票代码的复权历史行情");
    }
    points.sort((left, right) => left.date.localeCompare(right.date));
    const last = points.at(-1)!;
    const targetTime = Date.parse(last.date) - days * 86400000;
    const eligible = points.filter((point) => Date.parse(point.date) <= targetTime);
    const first = eligible.at(-1) ?? points[0];
    const historyLimited = eligible.length === 0;
    const readHistoricalRate = dependencies.historicalRate
      ?? ((marketDate: string) => fetchHistoricalUsdCnyRate((url) => upstreamFetch(url, historicalTimeoutMs), marketDate));
    const startRate = await readHistoricalRate(first.date);
    const endRate = await readHistoricalRate(last.date);
    const start = first.adjustedClose * startRate.rate;
    const end = last.adjustedClose * endRate.rate;
    const actualDays = Math.max(1, (Date.parse(last.date) - Date.parse(first.date)) / 86400000);
    return {
      annualRate: annualize(start, end, actualDays),
      periodReturn: (end / start - 1) * 100,
      requestedDays: days,
      actualDays,
      historyLimited,
      startDate: first.date,
      endDate: last.date,
      source: "Yahoo Finance 复权收盘价（人民币汇率调整）",
    };
  }

  async function stockReturn(code: string, days: number): Promise<MarketCalculation> {
    const isUsSecurity = isUsSecurityCode(code);
    if (isUsSecurity) return usStockReturn(code, days);
    const symbol = await resolveStockSymbol(code);
    const rawSymbol = isUsSecurity ? `us${code}` : symbol;
    const candidates = symbol === rawSymbol ? [symbol] : [symbol, rawSymbol];
    const fetchRows = async (candidate: string, startDate = "", endDate = "", count = 2) => {
      const url = `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${encodeURIComponent(candidate)},day,${startDate},${endDate},${count},qfq`;
      const response = await upstreamFetch(url);
      if (!response.ok) throw new Error("行情服务暂时不可用");
      const json = (await response.json()) as { data?: Record<string, { qfqday?: (string | object)[][] }> };
      return json.data?.[candidate]?.qfqday ?? [];
    };
    let selected: { first: (string | object)[]; last: (string | object)[] } | null = null;
    let earliestFallback: { first: (string | object)[]; last: (string | object)[] } | null = null;
    let lastError: unknown = null;
    for (const candidate of candidates) {
      try {
        const latestRows = await fetchRows(candidate);
        const last = latestRows.at(-1);
        if (!last) throw new Error("没有找到这个股票代码的历史行情");
        const targetDate = dateBefore(last[0], days);
        const targetRows = await fetchRows(candidate, dateBefore(targetDate, 30), targetDate, 30);
        if (targetRows.length > 0) {
          selected = { first: pointAtLookback(targetRows, last[0], days, (row) => row[0]), last };
          break;
        }

        let rangeEnd = String(last[0]);
        let earliest: (string | object)[] | undefined;
        for (let chunk = 0; chunk < 10; chunk += 1) {
          const rows = await fetchRows(candidate, targetDate, rangeEnd, 2000);
          if (rows.length === 0) break;
          earliest = rows[0];
          if (rows.length < 2000) break;
          rangeEnd = dateBefore(earliest[0], 1);
        }
        if (earliest && (!earliestFallback || Date.parse(String(earliest[0])) < Date.parse(String(earliestFallback.first[0])))) {
          earliestFallback = { first: earliest, last };
          break;
        }
      } catch (error) {
        lastError = error;
      }
    }
    const historyLimited = !selected && Boolean(earliestFallback);
    selected ??= earliestFallback;
    if (!selected) throw (lastError instanceof Error ? lastError : new Error("没有找到这个股票代码的历史行情"));
    const { first, last } = selected;
    const start = Number(first[2]);
    const end = Number(last[2]);
    const actualDays = Math.max(1, (Date.parse(String(last[0])) - Date.parse(String(first[0]))) / 86400000);
    return {
      annualRate: annualize(start, end, actualDays),
      periodReturn: (end / start - 1) * 100,
      requestedDays: days,
      actualDays,
      historyLimited,
      startDate: String(first[0]),
      endDate: String(last[0]),
      source: "腾讯证券前复权行情（严格复权）",
    };
  }

  async function stockQuote(code: string): Promise<MarketQuote> {
    const isUsSecurity = isUsSecurityCode(code);
    const symbol = await resolveStockSymbol(code);
    const rawSymbol = isUsSecurity ? `us${code}` : symbol;
    const candidates = symbol === rawSymbol ? [symbol] : [symbol, rawSymbol];
    let fields: string[] = [];
    let currentPrice = 0;
    for (const candidate of candidates) {
      const response = await upstreamFetch(`https://qt.gtimg.cn/q=${encodeURIComponent(candidate)}`);
      if (!response.ok) continue;
      const payload = await response.text();
      fields = payload.match(/="([^"]*)"/)?.[1]?.split("~") ?? [];
      currentPrice = Number(fields[3]);
      if (Number.isFinite(currentPrice) && currentPrice > 0) break;
    }
    if (!Number.isFinite(currentPrice) || currentPrice <= 0) throw new Error("没有找到这个股票代码的实时价格");
    const rawDate = fields[30] || "";
    const priceDate = /^\d{14}$/.test(rawDate)
      ? `${rawDate.slice(0, 4)}-${rawDate.slice(4, 6)}-${rawDate.slice(6, 8)} ${rawDate.slice(8, 10)}:${rawDate.slice(10, 12)}`
      : rawDate || new Date().toISOString();
    return { currentPrice, priceCurrency: isUsSecurity ? "USD" : "CNY", priceDate };
  }

  async function fundReturn(code: string, days: number, isMoney: boolean): Promise<MarketCalculation> {
    if (code === BNY_US_DOLLAR_LIQUIDITY_FUND) {
      const quote = await bnyUsdLiquidityQuote();
      return {
        annualRate: quote.annualRate,
        periodReturn: quote.annualRate,
        requestedDays: days,
        actualDays: 7,
        historyLimited: false,
        startDate: dateBefore(quote.priceDate, 7),
        endDate: quote.priceDate,
        source: "BNY Mellon 官方 7 日年化收益率（美元）",
      };
    }
    const requestedPageSize = 100;
    const fetchPage = async (pageIndex: number, startDate?: string, endDate?: string) => {
      const params = new URLSearchParams({ fundCode: code, pageIndex: String(pageIndex), pageSize: String(requestedPageSize) });
      if (startDate) params.set("startDate", startDate);
      if (endDate) params.set("endDate", endDate);
      const response = await upstreamFetch(`https://api.fund.eastmoney.com/f10/lsjz?${params}`);
      if (!response.ok) throw new Error("基金数据服务暂时不可用");
      return (await response.json()) as FundPage;
    };
    const firstPage = await fetchPage(1);
    const points = firstPage.Data?.LSJZList ?? [];
    const yieldType = firstPage.Data?.SYType;
    if (points.length < 1) throw new Error("没有找到这个基金代码的数据");

    if (isMoney || yieldType?.includes("每万份收益")) {
      const sample = points.slice(0, 7);
      const averageDaily = sample.reduce((sum, point) => sum + (Number(point.DWJZ) || 0), 0) / sample.length;
      const annualRate = averageDaily * 3.65;
      const actualDays = Math.max(1, (Date.parse(sample[0].FSRQ) - Date.parse(sample[sample.length - 1].FSRQ)) / 86400000);
      return {
        annualRate, periodReturn: annualRate,
        requestedDays: days, actualDays, historyLimited: false,
        startDate: sample[sample.length - 1].FSRQ, endDate: sample[0].FSRQ,
        source: "东方财富每万份收益折算",
      };
    }

    const latest = points[0];
    const targetDate = dateBefore(latest.FSRQ, days);
    const targetPage = await fetchPage(1, dateBefore(targetDate, 30), targetDate);
    const targetPoints = targetPage.Data?.LSJZList ?? [];
    let oldest: FundPoint;
    let historyLimited = false;
    if (targetPoints.length > 0) {
      oldest = pointAtLookback(targetPoints, latest.FSRQ, days, (point) => point.FSRQ);
    } else {
      historyLimited = true;
      const actualPageSize = Math.max(1, Number(firstPage.PageSize) || points.length || requestedPageSize);
      const lastPageIndex = Math.max(1, Math.ceil((Number(firstPage.TotalCount) || points.length) / actualPageSize));
      const lastPage = lastPageIndex === 1 ? firstPage : await fetchPage(lastPageIndex);
      oldest = lastPage.Data?.LSJZList?.at(-1) ?? points.at(-1)!;
    }
    const start = Number(oldest.LJJZ) || Number(oldest.DWJZ);
    const end = Number(latest.LJJZ) || Number(latest.DWJZ);
    const actualDays = Math.max(1, (Date.parse(latest.FSRQ) - Date.parse(oldest.FSRQ)) / 86400000);
    return {
      annualRate: annualize(start, end, actualDays), periodReturn: (end / start - 1) * 100,
      requestedDays: days, actualDays, historyLimited,
      startDate: oldest.FSRQ, endDate: latest.FSRQ, source: "东方财富历史净值",
    };
  }

  async function fundQuote(code: string): Promise<MarketQuote> {
    if (code === BNY_US_DOLLAR_LIQUIDITY_FUND) {
      const quote = await bnyUsdLiquidityQuote();
      return { currentPrice: 1, priceCurrency: "USD", priceDate: quote.priceDate };
    }
    const params = new URLSearchParams({ fundCode: code, pageIndex: "1", pageSize: "1" });
    const response = await upstreamFetch(`https://api.fund.eastmoney.com/f10/lsjz?${params}`);
    if (!response.ok) throw new Error("基金数据服务暂时不可用");
    const point = ((await response.json()) as FundPage).Data?.LSJZList?.[0];
    const currentPrice = Number(point?.DWJZ);
    if (!point || !Number.isFinite(currentPrice) || currentPrice <= 0) throw new Error("没有找到这个基金代码的最新单位净值");
    return { currentPrice, priceCurrency: "CNY", priceDate: point.FSRQ };
  }

  async function bnyUsdLiquidityQuote(): Promise<BnyLiquidityQuote> {
    const response = await upstreamFetch(BNY_US_DOLLAR_LIQUIDITY_URL);
    if (!response.ok) throw new Error("BNY Mellon 基金数据服务暂时不可用");
    const page = await response.text();
    const yieldMatch = page.match(/7-Day Yield With Waiver[\s\S]{0,500}?title="([0-9.]+)"/i);
    const dateMatch = page.match(/7-Day Yield With Waiver[\s\S]{0,700}?As of\s*&nbsp;\s*(\d{2}\/\d{2}\/\d{2})/i);
    const annualRate = Number(yieldMatch?.[1]);
    if (!Number.isFinite(annualRate) || annualRate < 0 || !dateMatch) {
      throw new Error("没有找到 BNY Mellon 美元流动性基金的 7 日年化收益率");
    }
    const [month, day, year] = dateMatch[1].split("/").map(Number);
    return { annualRate, priceDate: `20${String(year).padStart(2, "0")}-${String(month).padStart(2, "0")}-${String(day).padStart(2, "0")}` };
  }

  async function quote(category: string, code: string) {
    return readCachedQuote(quoteCache, `${category}:${code}`, () =>
      code === BNY_US_DOLLAR_LIQUIDITY_FUND
        ? fundQuote(code)
        : category === "stock" || (category === "fund" && (/^[15]/.test(code) || isUsSecurityCode(code)))
        ? stockQuote(code)
        : fundQuote(code));
  }

  return {
    calculate(category: string, code: string, days: number) {
      return code === BNY_US_DOLLAR_LIQUIDITY_FUND
        ? fundReturn(code, days, true)
        : category === "stock" || (category === "fund" && (/^[15]/.test(code) || isUsSecurityCode(code)))
        ? stockReturn(code, days)
        : fundReturn(code, days, category === "money");
    },
    quote,
  };
}
