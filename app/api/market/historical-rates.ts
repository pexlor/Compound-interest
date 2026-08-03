import { findHistoricalCnyRate, importExchangeRateHistory } from "../../../db/exchange-rate-history.ts";

type HistoricalRateRow = { date?: string; base?: string; quote?: string; rate?: number };
type HistoricalRateDependencies = {
  db: D1Database;
  fetcher: (url: string) => Promise<Response>;
};

export async function fetchHistoricalUsdCnyRate(
  dependencies: HistoricalRateDependencies | ((url: string) => Promise<Response>),
  marketDate: string,
) {
  const marketTime = Date.parse(marketDate);
  if (!Number.isFinite(marketTime)) throw new Error("无效的市场行情日期");
  const db = typeof dependencies === "function" ? null : dependencies.db;
  const fetcher = typeof dependencies === "function" ? dependencies : dependencies.fetcher;
  if (db) {
    const stored = await findHistoricalCnyRate(db, "USD", marketDate);
    if (stored) return { date: stored.rateDate, rate: stored.cnyRate };
  }

  const endpoint = new URL("https://api.frankfurter.dev/v2/rates");
  endpoint.searchParams.set("base", "USD");
  endpoint.searchParams.set("quotes", "CNY");
  endpoint.searchParams.set("from", new Date(marketTime - 7 * 86400000).toISOString().slice(0, 10));
  endpoint.searchParams.set("to", marketDate);
  let response: Response | undefined;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      response = await fetcher(endpoint.toString());
      break;
    } catch (error) {
      const errorName = error instanceof Error ? error.name : "";
      if (errorName === "TimeoutError" || errorName === "AbortError" || attempt === 1) {
        throw new Error("美元人民币历史汇率服务暂不可用");
      }
    }
  }
  if (!response?.ok) throw new Error("美元人民币历史汇率服务暂不可用");

  const rows = await response.json() as HistoricalRateRow[];
  const selected = (Array.isArray(rows) ? rows : [])
    .filter((row) => row.base === "USD" && row.quote === "CNY"
      && Number.isFinite(row.rate) && (row.rate ?? 0) > 0
      && Number.isFinite(Date.parse(row.date ?? "")) && Date.parse(row.date ?? "") <= marketTime)
    .sort((a, b) => Date.parse(a.date ?? "") - Date.parse(b.date ?? ""))
    .at(-1);
  if (!selected?.date || !selected.rate) throw new Error("没有找到对应日期的美元人民币历史汇率");
  if (db) {
    await importExchangeRateHistory(db, [{
      currency: "USD",
      cnyRate: selected.rate,
      rateDate: selected.date,
      source: "Frankfurter 央行参考汇率",
      fetchedAt: new Date().toISOString(),
    }]);
  }
  return { date: selected.date, rate: selected.rate };
}
