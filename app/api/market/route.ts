import { getAuthenticatedUser } from "../../../db/auth";
import { createMarketHandler } from "./handler";

export const GET = createMarketHandler({ authenticate: getAuthenticatedUser, fetch: (input, init) => fetch(input, init) });
