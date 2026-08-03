import { getAuthenticatedUser } from "../../../db/auth";
import { getAssetsDb } from "../../../db/assets";
import { recordDailySnapshot, saveExchangeRates } from "../../../db/history";
import { createExchangeRatesHandler } from "./handler";

export const GET = createExchangeRatesHandler({
  authenticate: getAuthenticatedUser,
  fetch: (input, init) => fetch(input, init),
  getAssetsDb,
  saveExchangeRates,
  recordDailySnapshot,
});
