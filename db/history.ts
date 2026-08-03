export type SnapshotTrigger = "asset_change" | "exchange_refresh";

export type HistoryRow = {
  id: number;
  user_id: number;
  snapshot_date: string;
  total_cny: number;
  trigger: SnapshotTrigger;
  rate_date: string | null;
  created_at: string;
  updated_at: string;
};

type SnapshotAsset = { amount: number; currency: string };
type StoredRate = { currency: string; cny_rate: number; rate_date: string };

export function calculateSnapshotTotal(assets: SnapshotAsset[], rates: Record<string, number>) {
  let total = 0;
  for (const asset of assets) {
    const rate = rates[asset.currency];
    if (!Number.isFinite(rate) || rate <= 0) return null;
    total += asset.amount * rate;
  }
  return Math.round(total);
}

function shanghaiDate(now: Date) {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(now);
  const value = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return `${value.year}-${value.month}-${value.day}`;
}

export async function saveExchangeRates(
  db: D1Database,
  rates: Record<string, number>,
  rateDate: string,
) {
  const statements = Object.entries(rates).map(([currency, cnyRate]) => db.prepare(`
    INSERT INTO exchange_rates (currency, cny_rate, rate_date, updated_at)
    VALUES (?, ?, ?, CURRENT_TIMESTAMP)
    ON CONFLICT(currency) DO UPDATE SET
      cny_rate = excluded.cny_rate,
      rate_date = excluded.rate_date,
      updated_at = CURRENT_TIMESTAMP
  `).bind(currency, cnyRate, rateDate));
  if (statements.length) await db.batch(statements);
}

export async function recordDailySnapshot(
  db: D1Database,
  userId: number,
  trigger: SnapshotTrigger,
  now = new Date(),
): Promise<HistoryRow | null> {
  const [assetResult, rateResult] = await Promise.all([
    db.prepare("SELECT amount, currency FROM assets WHERE user_id = ?").bind(userId).all<SnapshotAsset>(),
    db.prepare("SELECT currency, cny_rate, rate_date FROM exchange_rates").bind().all<StoredRate>(),
  ]);
  const rates = Object.fromEntries(rateResult.results.map((row) => [row.currency, row.cny_rate]));
  rates.CNY = 1;
  const totalCny = calculateSnapshotTotal(assetResult.results, rates);
  if (totalCny === null) return null;
  const rateDate = rateResult.results.map((row) => row.rate_date).filter(Boolean).sort().at(-1) ?? null;
  return db.prepare(`
    INSERT INTO asset_history (user_id, snapshot_date, total_cny, trigger, rate_date)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(user_id, snapshot_date) DO UPDATE SET
      total_cny = excluded.total_cny,
      trigger = excluded.trigger,
      rate_date = excluded.rate_date,
      updated_at = CURRENT_TIMESTAMP
    RETURNING id, user_id, snapshot_date, total_cny, trigger, rate_date, created_at, updated_at
  `).bind(userId, shanghaiDate(now), totalCny, trigger, rateDate).first<HistoryRow>();
}
