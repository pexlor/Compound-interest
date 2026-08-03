export const SUPPORTED_CURRENCIES = ["AUD", "CAD", "CHF", "EUR", "GBP", "HKD", "JPY", "SGD", "USD"] as const;

export type SupportedCurrency = typeof SUPPORTED_CURRENCIES[number];

export type RateRow = {
  date: string;
  base: string;
  quote: string;
  rate: number;
};

export type ExchangeRateHistoryFile = {
  version: 1;
  base: "CNY";
  checkedThrough: string;
  updatedAt: string;
  dates: Record<string, {
    source: string;
    rates: Record<SupportedCurrency, number>;
  }>;
};

const DAY_MS = 86_400_000;
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const SOURCE = "Frankfurter 央行参考汇率";

function parseDate(value: string): number {
  if (!DATE_PATTERN.test(value)) throw new Error("汇率历史文件格式无效：日期格式错误");
  const timestamp = Date.parse(`${value}T00:00:00.000Z`);
  if (!Number.isFinite(timestamp) || new Date(timestamp).toISOString().slice(0, 10) !== value) {
    throw new Error("汇率历史文件格式无效：日期不存在");
  }
  return timestamp;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function validateRates(value: unknown): value is Record<SupportedCurrency, number> {
  if (!isRecord(value)) return false;
  const keys = Object.keys(value).sort();
  if (keys.length !== SUPPORTED_CURRENCIES.length
    || keys.some((key, index) => key !== SUPPORTED_CURRENCIES[index])) return false;
  return SUPPORTED_CURRENCIES.every((currency) => Number.isFinite(value[currency]) && (value[currency] as number) > 0);
}

export function parseHistoryFile(json: string): ExchangeRateHistoryFile {
  let value: unknown;
  try {
    value = JSON.parse(json);
  } catch {
    throw new Error("汇率历史文件格式无效：JSON 解析失败");
  }
  if (isRecord(value) && value.version !== 1) throw new Error("不支持的汇率历史文件版本");
  if (!isRecord(value)
    || value.base !== "CNY"
    || typeof value.checkedThrough !== "string"
    || typeof value.updatedAt !== "string"
    || !Number.isFinite(Date.parse(value.updatedAt))
    || !isRecord(value.dates)) {
    throw new Error("汇率历史文件格式无效");
  }
  parseDate(value.checkedThrough);
  for (const [date, entry] of Object.entries(value.dates)) {
    parseDate(date);
    if (!isRecord(entry) || typeof entry.source !== "string" || !entry.source || !validateRates(entry.rates)) {
      throw new Error("汇率历史文件格式无效");
    }
  }
  return value as ExchangeRateHistoryFile;
}

export function createEmptyHistoryFile(checkedThrough: string): ExchangeRateHistoryFile {
  parseDate(checkedThrough);
  return {
    version: 1,
    base: "CNY",
    checkedThrough,
    updatedAt: new Date(0).toISOString(),
    dates: {},
  };
}

export function historyWindows(from: string, to: string): Array<{ from: string; to: string }> {
  const start = parseDate(from);
  const end = parseDate(to);
  if (start > end) return [];
  const windows: Array<{ from: string; to: string }> = [];
  for (let windowStart = start; windowStart <= end;) {
    const windowEnd = Math.min(windowStart + 365 * DAY_MS, end);
    windows.push({
      from: new Date(windowStart).toISOString().slice(0, 10),
      to: new Date(windowEnd).toISOString().slice(0, 10),
    });
    windowStart = windowEnd + DAY_MS;
  }
  return windows;
}

export function mergeRateRows(
  file: ExchangeRateHistoryFile,
  rows: RateRow[],
  checkedThrough: string,
  now: Date,
): ExchangeRateHistoryFile {
  parseDate(checkedThrough);
  if (!Number.isFinite(now.getTime())) throw new Error("无效的汇率同步时间");
  const grouped = new Map<string, Partial<Record<SupportedCurrency, number>>>();
  for (const row of rows) {
    if (row.base !== "CNY" || !SUPPORTED_CURRENCIES.includes(row.quote as SupportedCurrency)
      || !Number.isFinite(row.rate) || row.rate <= 0) continue;
    parseDate(row.date);
    const rates = grouped.get(row.date) ?? {};
    rates[row.quote as SupportedCurrency] = 1 / row.rate;
    grouped.set(row.date, rates);
  }
  const dates = { ...file.dates };
  for (const [date, rates] of grouped) {
    if (!SUPPORTED_CURRENCIES.every((currency) => Number.isFinite(rates[currency]) && (rates[currency] ?? 0) > 0)) continue;
    dates[date] = { source: SOURCE, rates: rates as Record<SupportedCurrency, number> };
  }
  return {
    version: 1,
    base: "CNY",
    checkedThrough,
    updatedAt: now.toISOString(),
    dates,
  };
}

export function pruneHistoryFile(file: ExchangeRateHistoryFile, cutoffDate: string): ExchangeRateHistoryFile {
  parseDate(cutoffDate);
  return {
    ...file,
    dates: Object.fromEntries(Object.entries(file.dates).filter(([date]) => date >= cutoffDate)),
  };
}
