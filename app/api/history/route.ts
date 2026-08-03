import { getAssetsDb } from "../../../db/assets";
import { getAuthenticatedUser } from "../../../db/auth";
import { createHistoryHandler } from "./handlers";

export const GET = createHistoryHandler({ getAssetsDb, getAuthenticatedUser });
