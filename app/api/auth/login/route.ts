import { createSession, findUserByEmail, sessionCookie, verifyPassword } from "../../../../db/auth";

export async function POST(request: Request) {
  try {
    const body = (await request.json()) as { email?: string; password?: string };
    const email = body.email?.trim().toLowerCase() ?? "";
    const password = body.password ?? "";
    const { db, user } = await findUserByEmail(email);
    if (!user || !(await verifyPassword(password, user))) {
      return Response.json({ error: "邮箱或密码不正确" }, { status: 401 });
    }
    const token = await createSession(db, user.id);
    return Response.json(
      { user: { id: user.id, email: user.email, displayName: user.display_name } },
      { headers: { "Set-Cookie": sessionCookie(token, request) } }
    );
  } catch (error) {
    return Response.json(
      { error: error instanceof Error ? error.message : "登录失败" },
      { status: 500 }
    );
  }
}
