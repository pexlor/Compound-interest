import { createSession, hashNewPassword, sessionCookie } from "../../../../db/auth";
import { getAssetsDb } from "../../../../db/assets";
import { createRegisterHandler } from "./handler";

export const POST = createRegisterHandler({ createSession, hashNewPassword, sessionCookie, getAssetsDb });
