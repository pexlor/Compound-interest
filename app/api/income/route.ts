import { getAuthenticatedUser } from "../../../db/auth";
import { getAssetsDb } from "../../../db/assets";
import { createIncomeHandlers } from "./handlers";

export const runtime = "edge";

export const { GET, PUT } = createIncomeHandlers({ getAuthenticatedUser, getAssetsDb });
