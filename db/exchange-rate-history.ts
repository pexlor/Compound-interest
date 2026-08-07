export type ExchangeRateHistoryEntry = {
  currency: string;
  cnyRate: number;
  rateDate: string;
  source: string;
  fetchedAt: string;
};

export type StoredHistoricalRate = ExchangeRateHistoryEntry;

type ExchangeRateHistoryRow = {
  currency: string;
  cny_rate: number;
  rate_date: string;
  source: string;
  fetched_at: string;
};

type LatestExchangeRateRow = {
  currency: string;
  cny_rate: number;
  rate_date: string;
};

const IMPORT_BATCH_SIZE = 100;
const supportedCurrencies = ["USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"] as const;
const historyColumns = "currency, cny_rate, rate_date, source, fetched_at";

function fromRow(row: ExchangeRateHistoryRow): StoredHistoricalRate {
  return {
    currency: row.currency,
    cnyRate: row.cny_rate,
    rateDate: row.rate_date,
    source: row.source,
    fetchedAt: row.fetched_at,
  };
}

export async function importExchangeRateHistory(
  db: D1Database,
  entries: ExchangeRateHistoryEntry[],
): Promise<{ inserted: number }> {
  let inserted = 0;
  for (let offset = 0; offset < entries.length; offset += IMPORT_BATCH_SIZE) {
    const statements = entries.slice(offset, offset + IMPORT_BATCH_SIZE).map((entry) => db.prepare(`INSERT OR IGNORE INTO exchange_rate_history (
      currency, cny_rate, rate_date, source, fetched_at
    ) VALUES (?, ?, ?, ?, ?)`)
      .bind(entry.currency, entry.cnyRate, entry.rateDate, entry.source, entry.fetchedAt));
    const results = await db.batch(statements);
    inserted += results.reduce((sum, result) => sum + (result.meta.changes ?? 0), 0);
  }
  return { inserted };
}

export async function findHistoricalCnyRate(
  db: D1Database,
  currency: string,
  onOrBefore: string,
): Promise<StoredHistoricalRate | null> {
  const row = await db.prepare(`SELECT ${historyColumns} FROM exchange_rate_history
    WHERE currency = ? AND rate_date <= ?
    ORDER BY rate_date DESC LIMIT 1`)
    .bind(currency, onOrBefore).first<ExchangeRateHistoryRow>();
  return row ? fromRow(row) : null;
}

export async function readLatestExchangeRates(
  db: D1Database,
): Promise<{ rates: Record<string, number>; date: string } | null> {
  const result = await db.prepare("SELECT currency, cny_rate, rate_date FROM exchange_rates").all<LatestExchangeRateRow>();
  const supported = result.results.filter((row) => supportedCurrencies.includes(row.currency as typeof supportedCurrencies[number]));
  const dates = new Set(supported.map((row) => row.rate_date));
  if (supported.length !== supportedCurrencies.length || dates.size !== 1) return null;
  return {
    rates: Object.fromEntries(supported.map((row) => [row.currency, row.cny_rate])),
    date: supported[0].rate_date,
  };
}

export async function pruneExchangeRateHistory(db: D1Database, cutoffDate: string): Promise<void> {
  await db.prepare("DELETE FROM exchange_rate_history WHERE rate_date < ?").bind(cutoffDate).run();
}

export async function refreshLatestExchangeRates(db: D1Database): Promise<void> {
  const placeholders = supportedCurrencies.map(() => "?").join(", ");
  const result = await db.prepare(`SELECT ${historyColumns} FROM exchange_rate_history
    WHERE rate_date = (
      SELECT rate_date FROM exchange_rate_history
      WHERE currency IN (${placeholders})
      GROUP BY rate_date
      HAVING COUNT(DISTINCT currency) = ?
      ORDER BY rate_date DESC
      LIMIT 1
    ) AND currency IN (${placeholders})`).bind(
      ...supportedCurrencies,
      supportedCurrencies.length,
      ...supportedCurrencies,
    ).all<ExchangeRateHistoryRow>();
  if (result.results.length !== supportedCurrencies.length) return;
  const rows = new Map(result.results.map((row) => [row.currency, row]));
  const rateDate = result.results[0].rate_date;
  await db.batch(supportedCurrencies.map((currency) => db.prepare(`INSERT INTO exchange_rates (
    currency, cny_rate, rate_date, updated_at
  ) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
  ON CONFLICT(currency) DO UPDATE SET
    cny_rate = excluded.cny_rate,
    rate_date = excluded.rate_date,
    updated_at = CURRENT_TIMESTAMP`).bind(currency, rows.get(currency)!.cny_rate, rateDate)));
}
