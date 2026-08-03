type Dependencies = {
  getAssetsDb(): Promise<D1Database>;
  hashNewPassword(password: string): Promise<{ hash: string; salt: string; iterations: number }>;
  createSession(db: D1Database, userId: number): Promise<string>;
  sessionCookie(token: string, request: Request): string;
};

export function createRegisterHandler(dependencies: Dependencies) {
  return async function POST(request: Request) {
    try {
      const body = (await request.json()) as { displayName?: string; email?: string; password?: string };
      const displayName = body.displayName?.trim() ?? "";
      const email = body.email?.trim().toLowerCase() ?? "";
      const password = body.password ?? "";
      if (displayName.length < 2 || displayName.length > 40) {
        return Response.json({ error: "昵称请输入 2—40 个字符" }, { status: 400 });
      }
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || email.length > 254) {
        return Response.json({ error: "请输入有效的邮箱地址" }, { status: 400 });
      }
      if (password.length < 8 || password.length > 128) {
        return Response.json({ error: "密码至少需要 8 个字符" }, { status: 400 });
      }

      const db = await dependencies.getAssetsDb();
      const passwordData = await dependencies.hashNewPassword(password);
      const user = await db.prepare(
        "INSERT INTO users (email, display_name, password_hash, password_salt, password_iterations) VALUES (?, ?, ?, ?, ?) RETURNING id, email, display_name"
      ).bind(email, displayName, passwordData.hash, passwordData.salt, passwordData.iterations)
        .first<{ id: number; email: string; display_name: string }>();
      if (!user) throw new Error("创建用户失败");
      const token = await dependencies.createSession(db, user.id);
      return Response.json(
        { user: { id: user.id, email: user.email, displayName: user.display_name } },
        { status: 201, headers: { "Set-Cookie": dependencies.sessionCookie(token, request) } }
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : "注册失败";
      if (message.toLowerCase().includes("unique")) {
        return Response.json({ error: "该邮箱已经注册，请直接登录" }, { status: 409 });
      }
      return Response.json({ error: message }, { status: 500 });
    }
  };
}
