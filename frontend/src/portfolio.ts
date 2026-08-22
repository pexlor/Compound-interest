export type InvestmentStrategy = "none" | "monthly" | "weekly" | "yearly" | "daily";
type PortfolioAsset = {
  category: string; amount: number; currency: string; annual_rate: number;
  investment_strategy?: InvestmentStrategy | null; investment_amount?: number | null; code?: string | null;
};

const dateFormatters = new Map<string, Intl.DateTimeFormat>();
const scheduleCache = new Map<string, Date[]>();

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
  const previous = new Date(first);
  do { previous.setUTCDate(previous.getUTCDate() - 1); } while (!isTradingDay(previous, timeZone));
  let previousParts = localDateParts(previous, timeZone);
  for (let date = first; date <= end; date.setUTCDate(date.getUTCDate() + 1)) {
    const p = localDateParts(date, timeZone);
    if (p.weekday < 1 || p.weekday > 5) continue;
    const firstTradingDay = previousParts.month !== p.month || previousParts.year !== p.year;
    const firstYearTradingDay = previousParts.year !== p.year;
    if ((strategy === "daily") || (strategy === "weekly" && p.weekday === 1) || (strategy === "monthly" && firstTradingDay) || (strategy === "yearly" && firstYearTradingDay)) dates.push(new Date(date));
    previousParts = p;
  }
  return dates;
}

function cachedContributionDates(start: Date, end: Date, strategy: InvestmentStrategy, timeZone: string) {
  const key = `${start.getTime()}:${end.getTime()}:${strategy}:${timeZone}`;
  const cached = scheduleCache.get(key);
  if (cached) return cached;
  const dates = contributionDates(start, end, strategy, timeZone);
  if (scheduleCache.size >= 32) scheduleCache.delete(scheduleCache.keys().next().value!);
  scheduleCache.set(key, dates);
  return dates;
}

type PreparedInvestment = {
  rate: number;
  initial: number;
  contribution: number;
  dates: Date[];
};

function preparePortfolio(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  asOf = new Date(),
) {
  if (assets.some((asset) => !Number.isFinite(rates[asset.currency]) || (rates[asset.currency] ?? 0) <= 0)) return null;
  const total = assets.reduce((sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0), 0);
  const weightedRate = total
    ? assets.reduce((sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0) * (asset.category === "fixed" ? 0 : asset.annual_rate), 0) / total
    : 0;
  return { total, weightedRate, asOf };
}

function prepareInvestments(assets: PortfolioAsset[], rates: Partial<Record<string, number>>, asOf: Date, maxHorizon: number): PreparedInvestment[] {
  const maxEnd = new Date(asOf);
  maxEnd.setUTCFullYear(maxEnd.getUTCFullYear() + maxHorizon);
  return assets.map((asset) => {
    const rate = asset.category === "fixed" ? 0 : asset.annual_rate / 100;
    const contribution = (asset.investment_amount ?? 0) * (rates[asset.currency] ?? 0);
    const dates = asset.category === "fund" && asset.investment_strategy && asset.investment_strategy !== "none" && contribution > 0
      ? cachedContributionDates(asOf, maxEnd, asset.investment_strategy, marketTimeZone(asset.code))
      : [];
    return {
      rate,
      initial: asset.amount * (rates[asset.currency] ?? 0),
      contribution,
      dates,
    };
  });
}

function calculateForecast(prepared: ReturnType<typeof preparePortfolio> & object, investments: PreparedInvestment[], horizon: number, monthlySavings: number) {
  const end = new Date(prepared.asOf); end.setUTCFullYear(end.getUTCFullYear() + horizon);
  const investmentForecast = investments.reduce((sum, investment) => {
    const initial = investment.initial * Math.pow(1 + investment.rate, horizon);
    if (!investment.dates.length) return sum + initial;
    const invested = investment.dates.reduce((total, date) => {
      if (date > end) return total;
      const yearsRemaining = Math.max(0, (end.getTime() - date.getTime()) / (365.25 * 86400000));
      return total + investment.contribution * Math.pow(1 + investment.rate, yearsRemaining);
    }, 0);
    return sum + initial + invested;
  }, 0);
  const savingsContribution = Math.max(0, monthlySavings) * Math.max(0, horizon) * 12;
  const forecast = investmentForecast + savingsContribution;
  return { total: prepared.total, forecast, expectedGain: forecast - prepared.total, weightedRate: prepared.weightedRate, savingsContribution };
}

export function calculatePortfolioSeries(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  maxHorizon: number,
  asOf = new Date(),
  monthlySavings = 0,
) {
  const prepared = preparePortfolio(assets, rates, asOf);
  if (!prepared) return null;
  const horizon = Math.max(0, Math.floor(maxHorizon));
  const investments = prepareInvestments(assets, rates, asOf, horizon);
  return Array.from({ length: horizon + 1 }, (_, year) => calculateForecast(prepared, investments, year, monthlySavings));
}

export function calculatePortfolio(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  horizon: number,
  asOf = new Date(),
  monthlySavings = 0,
) {
  const series = calculatePortfolioSeries(assets, rates, horizon, asOf, monthlySavings);
  if (!series) return null;
  return series[Math.max(0, Math.floor(horizon))] ?? series[0];
}
