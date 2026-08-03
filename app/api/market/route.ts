import { getAuthenticatedUser } from "../../../db/auth";
import { getAssetsDb } from "../../../db/assets";
import { createMarketCalculator } from "./calculator";
import { createMarketHandler } from "./handler";
import { fetchHistoricalUsdCnyRate } from "./historical-rates";
import { createMarketReturnService } from "./market-return-service";

let servicePromise: Promise<ReturnType<typeof createMarketReturnService>> | null = null;

async function getService() {
  servicePromise ??= getAssetsDb().then((db) => {
    const fetcher = (input: URL | RequestInfo, init?: RequestInit) => fetch(input, init);
    const calculator = createMarketCalculator({
      fetch: fetcher,
      historicalRate: (marketDate) => fetchHistoricalUsdCnyRate({
        db,
        fetcher: (url) => fetcher(url, { signal: AbortSignal.timeout(4000) }),
      }, marketDate),
    });
    return createMarketReturnService({ db, calculate: calculator.calculate });
  });
  return servicePromise;
}

export const GET = createMarketHandler({ authenticate: getAuthenticatedUser, getService });
