import {
  findLatestMarketReturn,
  findMarketReturn,
  saveMarketReturn,
  shanghaiDate,
} from "../../../db/market-returns.ts";
import type { MarketReturnRecord } from "../../../db/market-returns.ts";
import type { MarketCalculation, MarketQuote } from "./calculator.ts";

export const LOOKBACK_DAYS = [365, 1095, 1825, 3650] as const;

export type MarketReturnError = {
  category: string;
  code: string;
  lookbackDays: number;
  error: string;
};

type Logger = Pick<Console, "info" | "warn" | "error">;
type MarketKey = { category: string; code: string };
type Dependencies = {
  db: D1Database;
  calculate(category: string, code: string, lookbackDays: number): Promise<MarketCalculation>;
  quote?(category: string, code: string): Promise<MarketQuote>;
  now?: () => Date;
  concurrency?: number;
  logger?: Logger;
};

const marketCategories = new Set(["stock", "fund", "money"]);
const legacyUnsafeStockSources = new Set([
  "腾讯证券美股历史行情（人民币汇率调整）",
  "腾讯证券历史复权行情",
]);

function isLegacyUnsafeStockReturn(record: MarketReturnRecord | null) {
  return Boolean(record && legacyUnsafeStockSources.has(record.source));
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "读取行情失败";
}

function normalizeKey(category: string, code: string): MarketKey {
  const trimmedCode = code.trim();
  return { category: category.trim(), code: /^[a-z]/i.test(trimmedCode) ? trimmedCode.toUpperCase() : trimmedCode };
}

function uniqueKeys(rows: MarketKey[]) {
  const unique = new Map<string, MarketKey>();
  for (const row of rows) {
    const key = normalizeKey(row.category, row.code ?? "");
    if (!marketCategories.has(key.category) || !key.code) continue;
    unique.set(`${key.category}:${key.code}`, key);
  }
  return [...unique.values()];
}

async function mapLimited<T, R>(items: T[], limit: number, operation: (item: T) => Promise<R>) {
  const results: R[] = [];
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const index = next;
      next += 1;
      results[index] = await operation(items[index]);
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, () => worker()));
  return results;
}

export function createMarketReturnService(dependencies: Dependencies) {
  const now = dependencies.now ?? (() => new Date());
  const logger = dependencies.logger ?? console;
  const concurrency = Math.max(1, dependencies.concurrency ?? 4);
  const inFlight = new Map<string, Promise<MarketReturnRecord>>();
  const quoteCache = new Map<string, { value: MarketQuote; expiresAt: number }>();
  const quoteInFlight = new Map<string, Promise<MarketQuote>>();
  const quoteTtlMs = 60_000;

  function log(level: keyof Logger, event: string, fields: Record<string, unknown>) {
    logger[level]("[market-return]", { event, ...fields });
  }

  async function resolve(category: string, code: string, lookbackDays: number) {
    const currentDate = shanghaiDate(now());
    const fields = { category, code, lookbackDays, calculationDate: currentDate };
    const cached = await findMarketReturn(dependencies.db, category, code, lookbackDays, currentDate);
    if (cached && !isLegacyUnsafeStockReturn(cached)) {
      log("info", "cache_hit", fields);
      return cached;
    }

    log("info", "cache_miss", fields);
    const startedAt = Date.now();
    try {
      const calculated = await dependencies.calculate(category, code, lookbackDays);
      const saved = await saveMarketReturn(dependencies.db, {
        category,
        code,
        lookbackDays,
        calculationDate: currentDate,
        ...calculated,
        calculatedAt: now().toISOString(),
      });
      log("info", "calculate_success", {
        ...fields,
        actualDays: saved.actualDays,
        durationMs: Date.now() - startedAt,
      });
      return saved;
    } catch (error) {
      const message = errorMessage(error);
      const stale = await findLatestMarketReturn(dependencies.db, category, code, lookbackDays);
      if (stale && !isLegacyUnsafeStockReturn(stale)) {
        log("warn", "stale_fallback", { ...fields, staleDate: stale.calculationDate, error: message });
        return { ...stale, stale: true };
      }
      log("error", "calculate_failed", { ...fields, error: message, durationMs: Date.now() - startedAt });
      throw error;
    }
  }

  async function get(category: string, rawCode: string, lookbackDays: number) {
    const { category: normalizedCategory, code } = normalizeKey(category, rawCode);
    if (!marketCategories.has(normalizedCategory) || !code) throw new Error("无效的市场资产");
    if (!LOOKBACK_DAYS.includes(lookbackDays as (typeof LOOKBACK_DAYS)[number])) throw new Error("不支持的历史区间");
    const key = `${normalizedCategory}:${code}:${lookbackDays}:${shanghaiDate(now())}`;
    let pending = inFlight.get(key);
    if (!pending) {
      pending = resolve(normalizedCategory, code, lookbackDays);
      inFlight.set(key, pending);
    }
    try {
      const marketReturn = await pending;
      if (!dependencies.quote) return marketReturn;
      const quoteKey = `${normalizedCategory}:${code}`;
      const cachedQuote = quoteCache.get(quoteKey);
      if (cachedQuote && cachedQuote.expiresAt > now().getTime()) {
        return { ...marketReturn, ...cachedQuote.value };
      }
      let quotePending = quoteInFlight.get(quoteKey);
      if (!quotePending) {
        quotePending = dependencies.quote(normalizedCategory, code);
        quoteInFlight.set(quoteKey, quotePending);
      }
      try {
        const quote = await quotePending;
        quoteCache.set(quoteKey, { value: quote, expiresAt: now().getTime() + quoteTtlMs });
        return { ...marketReturn, ...quote };
      } finally {
        if (quoteInFlight.get(quoteKey) === quotePending) quoteInFlight.delete(quoteKey);
      }
    } finally {
      inFlight.delete(key);
    }
  }

  async function getForUser(userId: number, lookbackDays: number) {
    const rows = await dependencies.db.prepare(`SELECT category, code FROM assets
      WHERE user_id = ? AND category IN ('stock', 'fund', 'money') AND code IS NOT NULL AND code != ''`)
      .bind(userId).all<MarketKey>();
    const keys = uniqueKeys(rows.results);
    const settled = await mapLimited(keys, concurrency, async (key) => {
      try {
        return { result: await get(key.category, key.code, lookbackDays) };
      } catch (error) {
        return { error: { ...key, lookbackDays, error: errorMessage(error) } };
      }
    });
    return {
      results: settled.flatMap((item) => item.result ? [item.result] : []),
      errors: settled.flatMap((item) => item.error ? [item.error] : []),
    };
  }

  async function prewarmAll() {
    const rows = await dependencies.db.prepare(`SELECT category, code FROM assets
      WHERE category IN ('stock', 'fund', 'money') AND code IS NOT NULL AND code != ''`).bind().all<MarketKey>();
    const keys = uniqueKeys(rows.results);
    const work = keys.flatMap((key) => LOOKBACK_DAYS.map((lookbackDays) => ({ ...key, lookbackDays })));
    log("info", "prewarm_start", { assets: keys.length, items: work.length, calculationDate: shanghaiDate(now()) });
    const settled = await mapLimited(work, concurrency, async (item) => {
      try {
        await resolve(item.category, item.code, item.lookbackDays);
        return null;
      } catch (error) {
        return { ...item, error: errorMessage(error) };
      }
    });
    const errors = settled.filter((item): item is MarketReturnError => item !== null);
    const summary = { succeeded: work.length - errors.length, failed: errors.length, errors };
    log("info", "prewarm_complete", summary);
    return summary;
  }

  return { get, getForUser, prewarmAll };
}
