import {
  importExchangeRateHistory,
  pruneExchangeRateHistory,
  refreshLatestExchangeRates,
  type ExchangeRateHistoryEntry,
} from "../../../db/exchange-rate-history.ts";
import {
  SUPPORTED_CURRENCIES,
  createEmptyHistoryFile,
  historyWindows,
  mergeRateRows,
  parseHistoryFile,
  pruneHistoryFile,
  type ExchangeRateHistoryFile,
  type RateRow,
} from "./history-file.ts";

export const EXCHANGE_RATE_HISTORY_KEY = "exchange-rates/history.json";

export type ExchangeRateSyncSummary = {
  checkedThrough: string;
  dates: number;
  currencies: number;
  inserted: number;
  durationMs: number;
};

type Logger = Pick<Console, "info" | "warn" | "error">;
type Dependencies = {
  db: D1Database;
  bucket: R2Bucket;
  fetch: typeof fetch;
  now?: () => Date;
  logger?: Logger;
  timeoutMs?: number;
};

const DAY_MS = 86_400_000;
const MAX_WRITE_ATTEMPTS = 3;

function dateString(date: Date): string {
  return date.toISOString().slice(0, 10);
}

function addDays(value: string, days: number): string {
  return dateString(new Date(Date.parse(`${value}T00:00:00.000Z`) + days * DAY_MS));
}

function rollingCutoff(through: string): string {
  const date = new Date(`${through}T00:00:00.000Z`);
  date.setUTCFullYear(date.getUTCFullYear() - 10);
  return dateString(date);
}

function historyEntries(file: ExchangeRateHistoryFile, dates?: ReadonlySet<string>): ExchangeRateHistoryEntry[] {
  return Object.entries(file.dates)
    .filter(([rateDate]) => !dates || dates.has(rateDate))
    .flatMap(([rateDate, value]) => SUPPORTED_CURRENCIES.map((currency) => ({
      currency,
      cnyRate: value.rates[currency],
      rateDate,
      source: value.source,
      fetchedAt: file.updatedAt,
    })));
}

export function createExchangeRateHistorySync(dependencies: Dependencies) {
  const now = dependencies.now ?? (() => new Date());
  const logger = dependencies.logger ?? console;
  // The history endpoint can be noticeably slower than the latest-rate API,
  // especially on residential networks. Leave enough time for a full window
  // response before falling back to the latest-rate provider.
  const timeoutMs = dependencies.timeoutMs ?? 20_000;

  async function fetchRows(from?: string, to?: string): Promise<RateRow[]> {
    const endpoint = new URL("https://api.frankfurter.dev/v2/rates");
    // USD/CNY is the rate used for US-security returns.  Fetch it directly
    // instead of deriving it by inverting a CNY-based quote.  The remaining
    // USD quotes let us retain CNY conversion for the other supported assets.
    endpoint.searchParams.set("base", "USD");
    endpoint.searchParams.set("quotes", ["CNY", ...SUPPORTED_CURRENCIES.filter((currency) => currency !== "USD")].join(","));
    if (from) endpoint.searchParams.set("from", from);
    if (to) endpoint.searchParams.set("to", to);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(new DOMException("汇率历史请求超时", "TimeoutError")), timeoutMs);
    let response: Response;
    try {
      response = await dependencies.fetch(endpoint, {
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
    } catch (error) {
      throw new Error("汇率历史请求失败", { cause: error });
    } finally {
      clearTimeout(timer);
    }
    if (!response.ok) throw new Error(`汇率历史请求失败（HTTP ${response.status}）`);
    const payload = await response.json() as unknown;
    if (!Array.isArray(payload)) throw new Error("汇率历史响应格式无效");
    return payload as RateRow[];
  }

  async function readFile(): Promise<{ file: ExchangeRateHistoryFile | null; etag: string | null }> {
    const object = await dependencies.bucket.get(EXCHANGE_RATE_HISTORY_KEY);
    if (!object) return { file: null, etag: null };
    return { file: parseHistoryFile(await object.text()), etag: object.etag };
  }

  async function importFile(file: ExchangeRateHistoryFile, cutoffDate: string, dates?: ReadonlySet<string>) {
    const result = await importExchangeRateHistory(dependencies.db, historyEntries(file, dates));
    await pruneExchangeRateHistory(dependencies.db, cutoffDate);
    await refreshLatestExchangeRates(dependencies.db);
    return result.inserted;
  }

  return {
    async sync(options: { forceLatest?: boolean } = {}): Promise<ExchangeRateSyncSummary> {
      const startedAt = performance.now();
      const synchronizationTime = now();
      const checkedThrough = dateString(synchronizationTime);
      const cutoffDate = rollingCutoff(checkedThrough);
      logger.info("[exchange-rate-history]", {
        event: "sync_start",
        checkedThrough,
        forceLatest: options.forceLatest === true,
      });
      try {
        for (let attempt = 1; attempt <= MAX_WRITE_ATTEMPTS; attempt += 1) {
          const stored = await readFile();
          const original = stored.file ?? createEmptyHistoryFile(addDays(cutoffDate, -1));
          const nextUncheckedDate = addDays(original.checkedThrough, 1);
          const from = nextUncheckedDate < cutoffDate ? cutoffDate : nextUncheckedDate;
          const windows = historyWindows(from, checkedThrough);
          const rows: RateRow[] = [];
          for (const window of windows) {
            logger.info("[exchange-rate-history]", { event: "fetch_window", ...window, attempt });
            rows.push(...await fetchRows(window.from, window.to));
          }
          if (options.forceLatest && windows.length === 0) {
            logger.info("[exchange-rate-history]", { event: "fetch_latest", attempt });
            rows.push(...await fetchRows());
          }

          const needsWrite = stored.file === null || windows.length > 0 || options.forceLatest === true;
          const merged = mergeRateRows(
            original,
            rows,
            windows.length > 0 ? checkedThrough : original.checkedThrough,
            synchronizationTime,
          );
          const next = pruneHistoryFile(merged, cutoffDate);
          if (needsWrite) {
            const onlyIf = stored.etag
              ? { etagMatches: stored.etag }
              : { etagDoesNotMatch: "*" };
            const written = await dependencies.bucket.put(
              EXCHANGE_RATE_HISTORY_KEY,
              JSON.stringify(next),
              { httpMetadata: { contentType: "application/json" }, onlyIf },
            );
            if (!written) {
              logger.warn("[exchange-rate-history]", { event: "write_conflict", attempt });
              if (attempt < MAX_WRITE_ATTEMPTS) continue;
              throw new Error("汇率历史文件并发写入冲突");
            }
          }

          // The R2 file holds the full ten-year history. After its initial
          // import, write only the dates returned by this synchronization.
          const importedDates = stored.file === null
            ? undefined
            : new Set(rows.map((row) => row.date));
          const inserted = await importFile(next, cutoffDate, importedDates);
          const summary = {
            checkedThrough: next.checkedThrough,
            dates: Object.keys(next.dates).length,
            currencies: SUPPORTED_CURRENCIES.length,
            inserted,
            durationMs: Math.round(performance.now() - startedAt),
          };
          logger.info("[exchange-rate-history]", { event: "sync_complete", ...summary });
          return summary;
        }
        throw new Error("汇率历史文件同步失败");
      } catch (error) {
        logger.error("[exchange-rate-history]", {
          event: "sync_failed",
          message: error instanceof Error ? error.message : String(error),
          durationMs: Math.round(performance.now() - startedAt),
        });
        throw error;
      }
    },
  };
}
