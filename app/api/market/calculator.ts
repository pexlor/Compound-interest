import { fetchHistoricalUsdCnyRate } from "./historical-rates.ts";

type FundPoint = { FSRQ: string; DWJZ: string; LJJZ: string };
type FundPage = {
  Data?: { LSJZList?: FundPoint[]; SYType?: string };
  TotalCount?: number;
  PageSize?: number;
};

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

type CalculatorDependencies = {
  fetch: typeof fetch;
  timeoutMs?: number;
  maxConcurrent?: number;
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

  async function stockReturn(code: string, days: number): Promise<MarketCalculation> {
    const isUsSecurity = isUsSecurityCode(code);
    const symbol = await resolveStockSymbol(code);
    const rawSymbol = isUsSecurity ? `us${code}` : symbol;
    const candidates = symbol === rawSymbol ? [symbol] : [symbol, rawSymbol];
    const fetchRows = async (candidate: string, startDate = "", endDate = "", count = 2) => {
      const url = `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${encodeURIComponent(candidate)},day,${startDate},${endDate},${count},qfq`;
      const response = await upstreamFetch(url);
      if (!response.ok) throw new Error("行情服务暂时不可用");
      const json = (await response.json()) as { data?: Record<string, { qfqday?: (string | object)[][]; day?: (string | object)[][] }> };
      return json.data?.[candidate]?.qfqday ?? json.data?.[candidate]?.day ?? [];
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
    let start = Number(first[2]);
    let end = Number(last[2]);
    if (isUsSecurity) {
      const historicalFetch = (url: string) => upstreamFetch(url, historicalTimeoutMs);
      const startRate = await fetchHistoricalUsdCnyRate(historicalFetch, String(first[0]));
      const endRate = await fetchHistoricalUsdCnyRate(historicalFetch, String(last[0]));
      start *= startRate.rate;
      end *= endRate.rate;
    }
    const actualDays = Math.max(1, (Date.parse(String(last[0])) - Date.parse(String(first[0]))) / 86400000);
    return {
      annualRate: annualize(start, end, actualDays),
      periodReturn: (end / start - 1) * 100,
      requestedDays: days,
      actualDays,
      historyLimited,
      startDate: String(first[0]),
      endDate: String(last[0]),
      source: isUsSecurity ? "腾讯证券美股历史行情（人民币汇率调整）" : "腾讯证券历史复权行情",
    };
  }

  async function fundReturn(code: string, days: number, isMoney: boolean): Promise<MarketCalculation> {
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

  return {
    calculate(category: string, code: string, days: number) {
      return category === "stock" || (category === "fund" && (/^[15]/.test(code) || isUsSecurityCode(code)))
        ? stockReturn(code, days)
        : fundReturn(code, days, category === "money");
    },
  };
}
