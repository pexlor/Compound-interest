import { initializeAssetsDb } from "../../../db/assets.ts";
import { createMarketCalculator } from "./calculator.ts";
import { fetchHistoricalUsdCnyRate } from "./historical-rates.ts";
import { createMarketReturnService } from "./market-return-service.ts";

export async function prewarmMarketReturns(
  db: D1Database,
  fetcher: typeof fetch,
  logger: Pick<Console, "info" | "warn" | "error"> = console,
) {
  await initializeAssetsDb(db);
  const calculator = createMarketCalculator({
    fetch: fetcher,
    historicalRate: (marketDate) => fetchHistoricalUsdCnyRate({
      db,
      fetcher: (url) => fetcher(url, { signal: AbortSignal.timeout(4000) }),
    }, marketDate),
  });
  return createMarketReturnService({
    db,
    calculate: calculator.calculate,
    logger,
  }).prewarmAll();
}
