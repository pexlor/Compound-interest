export type MarketReturnRecord = {
  category: string;
  code: string;
  lookbackDays: number;
  calculationDate: string;
  annualRate: number;
  periodReturn: number;
  requestedDays: number;
  actualDays: number;
  historyLimited: boolean;
  startDate: string;
  endDate: string;
  source: string;
  calculatedAt?: string;
  stale?: boolean;
};

type MarketReturnRow = {
  category: string;
  code: string;
  lookback_days: number;
  calculation_date: string;
  annual_rate: number;
  period_return: number;
  requested_days: number;
  actual_days: number;
  history_limited: number;
  start_date: string;
  end_date: string;
  source: string;
  calculated_at: string;
};

const selectColumns = `category, code, lookback_days, calculation_date, annual_rate, period_return,
  requested_days, actual_days, history_limited, start_date, end_date, source, calculated_at`;

function fromRow(row: MarketReturnRow): MarketReturnRecord {
  return {
    category: row.category,
    code: row.code,
    lookbackDays: row.lookback_days,
    calculationDate: row.calculation_date,
    annualRate: row.annual_rate,
    periodReturn: row.period_return,
    requestedDays: row.requested_days,
    actualDays: row.actual_days,
    historyLimited: Boolean(row.history_limited),
    startDate: row.start_date,
    endDate: row.end_date,
    source: row.source,
    calculatedAt: row.calculated_at,
  };
}

export function shanghaiDate(now = new Date()) {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(now);
}

export async function findMarketReturn(
  db: D1Database,
  category: string,
  code: string,
  lookbackDays: number,
  calculationDate: string,
) {
  const row = await db.prepare(`SELECT ${selectColumns} FROM market_returns
    WHERE category = ? AND code = ? AND lookback_days = ? AND calculation_date = ?`)
    .bind(category, code, lookbackDays, calculationDate).first<MarketReturnRow>();
  return row ? fromRow(row) : null;
}

export async function findLatestMarketReturn(
  db: D1Database,
  category: string,
  code: string,
  lookbackDays: number,
) {
  const row = await db.prepare(`SELECT ${selectColumns} FROM market_returns
    WHERE category = ? AND code = ? AND lookback_days = ?
    ORDER BY calculation_date DESC LIMIT 1`)
    .bind(category, code, lookbackDays).first<MarketReturnRow>();
  return row ? fromRow(row) : null;
}

export async function saveMarketReturn(db: D1Database, record: MarketReturnRecord) {
  const calculatedAt = record.calculatedAt ?? new Date().toISOString();
  const row = await db.prepare(`INSERT INTO market_returns (
    category, code, lookback_days, calculation_date, annual_rate, period_return,
    requested_days, actual_days, history_limited, start_date, end_date, source, calculated_at
  ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(category, code, lookback_days, calculation_date) DO UPDATE SET
    annual_rate = excluded.annual_rate,
    period_return = excluded.period_return,
    requested_days = excluded.requested_days,
    actual_days = excluded.actual_days,
    history_limited = excluded.history_limited,
    start_date = excluded.start_date,
    end_date = excluded.end_date,
    source = excluded.source,
    calculated_at = excluded.calculated_at
  RETURNING ${selectColumns}`)
    .bind(
      record.category,
      record.code,
      record.lookbackDays,
      record.calculationDate,
      record.annualRate,
      record.periodReturn,
      record.requestedDays,
      record.actualDays,
      record.historyLimited ? 1 : 0,
      record.startDate,
      record.endDate,
      record.source,
      calculatedAt,
    ).first<MarketReturnRow>();
  if (!row) throw new Error("市场收益缓存写入失败");
  return fromRow(row);
}
