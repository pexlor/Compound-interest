export type InvestmentStrategy = "none" | "monthly" | "weekly" | "yearly" | "daily";
type PortfolioAsset = {
  category: string; amount: number; currency: string; annual_rate: number;
  investment_strategy?: InvestmentStrategy | null; investment_amount?: number | null; code?: string | null;
};

const dateFormatters = new Map<string, Intl.DateTimeFormat>();

function getDateFormatter(timeZone: string) {
  let formatter = dateFormatters.get(timeZone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat("en-US", {
      timeZone,
      year: "numeric",
      month: "numeric",
      day: "numeric",
      weekday: "short",
    });
    dateFormatters.set(timeZone, formatter);
  }
  return formatter;
}

function marketTimeZone(code: string | null | undefined) {
  return code && /^[A-Z][A-Z0-9.-]*$/i.test(code) ? "America/New_York" : "Asia/Shanghai";
}

function localDateParts(date: Date, timeZone: string) {
  const parts = getDateFormatter(timeZone).formatToParts(date);
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return { year: Number(values.year), month: Number(values.month), day: Number(values.day), weekday: ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(values.weekday) };
}

function isTradingDay(date: Date, timeZone: string) { const weekday = localDateParts(date, timeZone).weekday; return weekday >= 1 && weekday <= 5; }

function contributionDates(start: Date, end: Date, strategy: InvestmentStrategy, timeZone: string) {
  if (strategy === "none") return [];
  const dates: Date[] = [];
  const first = new Date(start); first.setUTCDate(first.getUTCDate() + 1);
  for (let date = first; date <= end; date.setUTCDate(date.getUTCDate() + 1)) {
    if (!isTradingDay(date, timeZone)) continue;
    const p = localDateParts(date, timeZone);
    const previous = new Date(date);
    do { previous.setUTCDate(previous.getUTCDate() - 1); } while (!isTradingDay(previous, timeZone));
    const previousParts = localDateParts(previous, timeZone);
    const firstTradingDay = previousParts.month !== p.month || previousParts.year !== p.year;
    const firstYearTradingDay = previousParts.year !== p.year;
    if ((strategy === "daily") || (strategy === "weekly" && p.weekday === 1) || (strategy === "monthly" && firstTradingDay) || (strategy === "yearly" && firstYearTradingDay)) dates.push(new Date(date));
  }
  return dates;
}

export function calculatePortfolio(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  horizon: number,
  asOf = new Date(),
) {
  if (assets.some((asset) => !Number.isFinite(rates[asset.currency]) || (rates[asset.currency] ?? 0) <= 0)) return null;
  const total = assets.reduce((sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0), 0);
  const forecast = assets.reduce((sum, asset) => {
    const rate = asset.category === "fixed" ? 0 : asset.annual_rate / 100;
    const initial = asset.amount * (rates[asset.currency] ?? 0) * Math.pow(1 + rate, horizon);
    if (!asset.investment_strategy || asset.investment_strategy === "none" || !asset.investment_amount || asset.category !== "fund") return sum + initial;
    const end = new Date(asOf); end.setUTCFullYear(end.getUTCFullYear() + horizon);
    const start = new Date(asOf);
    const contribution = asset.investment_amount * (rates[asset.currency] ?? 0);
    const invested = contributionDates(start, end, asset.investment_strategy, marketTimeZone(asset.code)).reduce((total, date) => {
      const yearsRemaining = Math.max(0, (end.getTime() - date.getTime()) / (365.25 * 86400000));
      return total + contribution * Math.pow(1 + rate, yearsRemaining);
    }, 0);
    return sum + initial + invested;
  }, 0);
  const weightedRate = total
    ? assets.reduce((sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0) * (asset.category === "fixed" ? 0 : asset.annual_rate), 0) / total
    : 0;
  return { total, forecast, expectedGain: forecast - total, weightedRate };
}
