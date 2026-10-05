// 投资组合预测工具：结合汇率、储蓄和未来现金流计算资产增长。

import type { Cashflow } from "./income";
// PortfolioAsset 定义收益预测需要的资产、币种和收益率。
type PortfolioAsset = {
  category: string; amount: number; currency: string; annual_rate: number;
};

// PreparedInvestment 保存已换算的初始市值与收益率。
type PreparedInvestment = { rate: number; initial: number };

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

// prepareInvestments 将现有资产市值换算为人民币并整理其收益率。
function prepareInvestments(assets: PortfolioAsset[], rates: Partial<Record<string, number>>): PreparedInvestment[] {
  return assets.map(/* 换算资产市值，固定资产按零收益处理。 */ (asset) => ({
    rate: asset.category === "fixed" ? 0 : asset.annual_rate / 100,
    initial: asset.amount * (rates[asset.currency] ?? 0),
  }));
}

// calculateMonthEndGrowth 按预测基准日到本月底的剩余天数估算现有资产收益，只计基准日之后本月到账的本金。
export function calculateMonthEndGrowth(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  asOf: Date,
  monthlySavings = 0,
  cashflows: Cashflow[] = [],
) {
  if (!Number.isFinite(asOf.getTime()) || !preparePortfolio(assets, rates, asOf)) return null;
  const end = new Date(Date.UTC(asOf.getUTCFullYear(), asOf.getUTCMonth() + 1, 0));
  const daysRemaining = Math.max(0, (end.getTime() - asOf.getTime()) / 86400000);
  const daysInYear = (Date.UTC(asOf.getUTCFullYear() + 1, 0, 1) - Date.UTC(asOf.getUTCFullYear(), 0, 1)) / 86400000;
  const incoming = cashflows.filter(/* 仅本月基准日之后到账的奖金、期权等现金流参与剩余增长。 */ event => {
    const date = new Date(`${event.date}T00:00:00Z`);
    return date > asOf && date <= end;
  });
  if (incoming.some(/* 本月到账外币缺失有效汇率时不返回不完整金额。 */ event => !Number.isFinite(rates[event.currency]) || (rates[event.currency] ?? 0) <= 0)) return null;
  const investmentGain = prepareInvestments(assets, rates).reduce(/* 将各资产有效年化收益率按剩余天数折算，未来新增资金不计收益。 */ (sum, investment) =>
    sum + investment.initial * (Math.pow(1 + investment.rate, daysRemaining / daysInYear) - 1), 0);
  if (!Number.isFinite(investmentGain)) return null;
  const savingsContribution = (end > asOf ? Math.max(0, monthlySavings) : 0)
    + incoming.reduce(/* 按实际本月到账日期筛选后的现金流换算人民币本金。 */ (sum, event) => sum + event.amount * (rates[event.currency] ?? 0), 0);
  return { investmentGain, savingsContribution, expectedGain: investmentGain + savingsContribution, endDate: end.toISOString().slice(0, 10) };
}

// calculateForecast 计算给定年限后的资产预测值和收益构成。
function calculateForecast(prepared: ReturnType<typeof preparePortfolio> & object, investments: PreparedInvestment[], horizon: number, monthlySavings: number, cashflows: Cashflow[], rates: Partial<Record<string, number>> = {}) {
  const end = new Date(prepared.asOf); end.setUTCFullYear(end.getUTCFullYear() + horizon);
  const investmentForecast = investments.reduce(/* 合计现有资产按各自收益率计算的预测价值。 */ (sum, investment) =>
    sum + investment.initial * Math.pow(1 + investment.rate, horizon), 0);
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
  const investments = prepareInvestments(assets, rates);
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
