import { initializeAssetsDb } from "../../../db/assets.ts";
import { createMarketCalculator } from "./calculator.ts";
import { createMarketReturnService } from "./market-return-service.ts";

export async function prewarmMarketReturns(
  db: D1Database,
  fetcher: typeof fetch,
  logger: Pick<Console, "info" | "warn" | "error"> = console,
) {
  await initializeAssetsDb(db);
  const calculator = createMarketCalculator({ fetch: fetcher });
  return createMarketReturnService({
    db,
    calculate: calculator.calculate,
    logger,
  }).prewarmAll();
}
