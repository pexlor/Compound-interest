import { clearSessionCookie, deleteCurrentSession } from "../../../../db/auth";

export async function POST(request: Request) {
  await deleteCurrentSession(request);
  return Response.json({ ok: true }, { headers: { "Set-Cookie": clearSessionCookie(request) } });
}
