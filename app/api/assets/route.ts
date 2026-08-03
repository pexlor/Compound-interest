import { getAssetsDb } from "../../../db/assets";
import { getAuthenticatedUser } from "../../../db/auth";
import { recordDailySnapshot } from "../../../db/history";
import { createAssetsHandlers } from "./handlers";

export const { GET, POST, DELETE, PATCH } = createAssetsHandlers({ getAssetsDb, getAuthenticatedUser, recordDailySnapshot });
