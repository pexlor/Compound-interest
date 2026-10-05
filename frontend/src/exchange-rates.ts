// ExchangeCurrency 限定总资产卡片展示的三种货币。
type ExchangeCurrency = "CNY" | "USD" | "HKD";

// exchangePairs 将外币兑人民币数据换算为三组双向汇率，无效数据返回空值。
export function exchangePairs(rates: Partial<Record<ExchangeCurrency, number>>) {
  const pairs: [ExchangeCurrency, ExchangeCurrency][] = [["CNY", "USD"], ["CNY", "HKD"], ["HKD", "USD"]];
  return pairs.map(/* 使用相同人民币基准推导双向汇率，避免除零或显示无效数字。 */ ([base, quote]) => {
    const baseRate = rates[base];
    const quoteRate = rates[quote];
    const valid = typeof baseRate === "number" && Number.isFinite(baseRate) && baseRate > 0
      && typeof quoteRate === "number" && Number.isFinite(quoteRate) && quoteRate > 0;
    return { base, quote, forward: valid ? baseRate / quoteRate : null, reverse: valid ? quoteRate / baseRate : null };
  });
}
