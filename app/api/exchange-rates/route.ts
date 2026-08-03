import { env } from "cloudflare:workers";
import { getAuthenticatedUser } from "../../../db/auth";
import { getAssetsDb } from "../../../db/assets";
import { readLatestExchangeRates } from "../../../db/exchange-rate-history";
import { recordDailySnapshot, saveExchangeRates } from "../../../db/history";
import { createExchangeRatesHandler } from "./handler";
import { createExchangeRateHistorySync } from "./history-sync";

export const GET = createExchangeRatesHandler({
  authenticate: getAuthenticatedUser,
  fetch: (input, init) => fetch(input, init),
  getAssetsDb,
  readLatestRates: readLatestExchangeRates,
  syncHistory: (db, options) => createExchangeRateHistorySync({
    db,
    bucket: (env as unknown as { RATES: R2Bucket }).RATES,
    fetch: (input, init) => fetch(input, init),
  }).sync(options),
  saveExchangeRates,
  recordDailySnapshot,
});
