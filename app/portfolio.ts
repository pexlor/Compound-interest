type PortfolioAsset = { category: string; amount: number; currency: string; annual_rate: number };

export function calculatePortfolio(
  assets: PortfolioAsset[],
  rates: Partial<Record<string, number>>,
  horizon: number,
) {
  if (assets.some((asset) => !Number.isFinite(rates[asset.currency]) || (rates[asset.currency] ?? 0) <= 0)) return null;
  const total = assets.reduce((sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0), 0);
  const forecast = assets.reduce((sum, asset) => {
    const rate = asset.category === "fixed" ? 0 : asset.annual_rate / 100;
    return sum + asset.amount * (rates[asset.currency] ?? 0) * Math.pow(1 + rate, horizon);
  }, 0);
  const weightedRate = total
    ? assets.reduce((sum, asset) => sum + asset.amount * (rates[asset.currency] ?? 0) * (asset.category === "fixed" ? 0 : asset.annual_rate), 0) / total
    : 0;
  return { total, forecast, expectedGain: forecast - total, weightedRate };
}
