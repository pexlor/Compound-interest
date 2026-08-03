import { getAuthenticatedUser } from "../../../db/auth";
import { getAssetsDb } from "../../../db/assets";
import { createMarketCalculator } from "./calculator";
import { createMarketHandler } from "./handler";
import { createMarketReturnService } from "./market-return-service";

const calculator = createMarketCalculator({ fetch: (input, init) => fetch(input, init) });
let servicePromise: Promise<ReturnType<typeof createMarketReturnService>> | null = null;

async function getService() {
  servicePromise ??= getAssetsDb().then((db) => createMarketReturnService({
    db,
    calculate: calculator.calculate,
  }));
  return servicePromise;
}

export const GET = createMarketHandler({ authenticate: getAuthenticatedUser, getService });
