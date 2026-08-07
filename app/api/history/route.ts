import { getAssetsDb } from "../../../db/assets";
import { getAuthenticatedUser } from "../../../db/auth";
import { createHistoryHandler } from "./handlers";
import { runUserDailyAssetSnapshot } from "./daily-snapshot";
import { createDailyRefreshHandler } from "./daily-refresh";

export const GET = createHistoryHandler({ getAssetsDb, getAuthenticatedUser });
export const POST = createDailyRefreshHandler({
  getAssetsDb,
  getAuthenticatedUser,
  refreshUser: (db, userId) => runUserDailyAssetSnapshot(db, userId, (input, init) => fetch(input, init)),
});
