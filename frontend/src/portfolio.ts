// 投资组合预测工具：结合汇率、定投日期、储蓄和未来现金流计算资产增长。

import type { Cashflow } from "./income";
// InvestmentStrategy 列出不定投、月投、周投、年投和日投策略。
export type InvestmentStrategy = "none" | "monthly" | "weekly" | "yearly" | "daily";
// PortfolioAsset 定义收益预测需要的资产、币种、收益率和定投字段。
type PortfolioAsset = {
  category: string; amount: number; currency: string; annual_rate: number;
  investment_strategy?: InvestmentStrategy | null; investment_amount?: number | null; code?: string | null;
};

const dateFormatters = new Map<string, Intl.DateTimeFormat>();
const scheduleCache = new Map<string, Date[]>();

// getDateFormatter 缓存指定时区的日期格式化器。
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

// marketTimeZone 根据证券代码推断对应市场时区。
function marketTimeZone(code: string | null | undefined) {
  return code && /^[A-Z][A-Z0-9.-]*$/i.test(code) ? "America/New_York" : "Asia/Shanghai";
}

// localDateParts 将日期拆分为目标时区的本地年月日和星期。
function localDateParts(date: Date, timeZone: string) {
  const parts = getDateFormatter(timeZone).formatToParts(date);
  const values = Object.fromEntries(parts.map(/* 把日期格式化片段转换为便于按名称读取的键值对。 */ (part) => [part.type, part.value]));
  return { year: Number(values.year), month: Number(values.month), day: Number(values.day), weekday: ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(values.weekday) };
}

// isTradingDay 判断目标时区中的日期是否为工作日交易日。
function isTradingDay(date: Date, timeZone: string) { const weekday = localDateParts(date, timeZone).weekday; return weekday >= 1 && weekday <= 5; }

// contributionDates 生成指定定投策略在预测区间内的扣款日期。
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

// cachedContributionDates 缓存定投日期计算结果，减少重复预测开销。
function cachedContributionDates(start: Date, end: Date, strategy: InvestmentStrategy, timeZone: string) {
  const key = `${start.getTime()}:${end.getTime()}:${strategy}:${timeZone}`;
  const cached = scheduleCache.get(key);
  if (cached) return cached;
  const dates = contributionDates(start, end, strategy, timeZone);
  if (scheduleCache.size >= 32) scheduleCache.delete(scheduleCache.keys().next().value!);
  scheduleCache.set(key, dates);
  return dates;
}

// PreparedInvestment 保存已换算的初始市值、定投金额、收益率与未来扣款日期。
type PreparedInvestment = {
  rate: number;
  initial: number;
  contribution: number;
  dates: Date[];
};

// preparePortfolio 汇总当前资产并计算预测所需的加权收益率。
function preparePortfolio(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  asOf = new Date(),
) {
  if (assets.some(/* 检查相关资产或预测现金流是否缺少有效汇率。 */ (asset) => !Number.isFinite(rates[asset.currency]) || (rates[asset.currency] ?? 0) <= 0)) return null;
  const total = assets.reduce(/* 按汇率换算并累计资产的人民币总额。 */ (sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0), 0);
  const weightedRate = total
    ? assets.reduce(/* 按人民币资产市值加权累计收益率，固定资产按零收益处理。 */ (sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0) * (asset.category === "fixed" ? 0 : asset.annual_rate), 0) / total
    : 0;
  return { total, weightedRate, asOf };
}

// prepareInvestments 整理基金定投计划及其未来扣款日期。
function prepareInvestments(assets: PortfolioAsset[], rates: Partial<Record<string, number>>, asOf: Date, maxHorizon: number): PreparedInvestment[] {
  const maxEnd = new Date(asOf);
  maxEnd.setUTCFullYear(maxEnd.getUTCFullYear() + maxHorizon);
  return assets.map(/* 整理单个资产的人民币市值、收益率和未来定投扣款日期。 */ (asset) => {
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

// calculateForecast 计算给定年限后的资产预测值和收益构成。
function calculateForecast(prepared: ReturnType<typeof preparePortfolio> & object, investments: PreparedInvestment[], horizon: number, monthlySavings: number, cashflows: Cashflow[], rates: Partial<Record<string, number>> = {}) {
  const end = new Date(prepared.asOf); end.setUTCFullYear(end.getUTCFullYear() + horizon);
  const investmentForecast = investments.reduce(/* 合计各资产初始市值与未来定投的预测价值。 */ (sum, investment) => {
    const initial = investment.initial * Math.pow(1 + investment.rate, horizon);
    if (!investment.dates.length) return sum + initial;
    const invested = investment.dates.reduce(/* 累计预测终点前各笔定投的本金。 */ (total, date) => {
      if (date > end) return total;
      return total + investment.contribution;
    }, 0);
    return sum + initial + invested;
  }, 0);
  let savingsContribution = 0;
  let proceedsForecast = 0;
  // add 将预测区间内的到账金额计入本金，未来新增资金不计算收益。
  const add = (date: Date, amount: number) => {
    if (date <= prepared.asOf || date > end) return;
    savingsContribution += amount;
    proceedsForecast += amount;
  };
  // Monthly savings arrive at calendar month end, matching retirement planning.
  for (let date = new Date(Date.UTC(prepared.asOf.getUTCFullYear(), prepared.asOf.getUTCMonth() + 1, 0)); date <= end;) {
    add(date, Math.max(0, monthlySavings));
    date = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 2, 0));
  }
  for (const event of cashflows) add(new Date(`${event.date}T00:00:00Z`), event.amount * (rates[event.currency] ?? 0));
  const forecast = investmentForecast + proceedsForecast;
  return { total: prepared.total, forecast, expectedGain: forecast - prepared.total, weightedRate: prepared.weightedRate, savingsContribution };
}

// calculatePortfolioSeries 生成用于资产增长图的逐年预测数据。
export function calculatePortfolioSeries(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  maxHorizon: number,
  asOf = new Date(),
  monthlySavings = 0,
  cashflows: Cashflow[] = [],
) {
  const prepared = preparePortfolio(assets, rates, asOf);
  if (!prepared) return null;
  const horizon = Math.max(0, Math.floor(maxHorizon));
  const end = new Date(asOf); end.setUTCFullYear(end.getUTCFullYear() + horizon);
  if (cashflows.some(/* 检查相关资产或预测现金流是否缺少有效汇率。 */ event => { const date = new Date(`${event.date}T00:00:00Z`); return date > asOf && date <= end && (!Number.isFinite(rates[event.currency]) || (rates[event.currency] ?? 0) <= 0); })) return null;
  const investments = prepareInvestments(assets, rates, asOf, horizon);
  return Array.from({ length: horizon + 1 }, /* 逐年计算预测结果，生成从当前年度到终点的增长序列。 */ (_, year) => calculateForecast(prepared, investments, year, monthlySavings, cashflows, rates));
}

// calculatePortfolio 计算单一预测年限下的资产汇总结果。
export function calculatePortfolio(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  horizon: number,
  asOf = new Date(),
  monthlySavings = 0,
  cashflows: Cashflow[] = [],
) {
  const series = calculatePortfolioSeries(assets, rates, horizon, asOf, monthlySavings, cashflows);
  if (!series) return null;
  return series[Math.max(0, Math.floor(horizon))] ?? series[0];
}
