import { getAuthenticatedUser } from "../../../../db/auth";

export async function GET(request: Request) {
  const user = await getAuthenticatedUser(request);
  if (!user) return Response.json({ error: "未登录" }, { status: 401 });
  return Response.json({ user });
}
