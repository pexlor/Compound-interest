type User = { id: number };
type Dependencies = {
  getAuthenticatedUser(request: Request): Promise<User | null>;
  getAssetsDb(): Promise<D1Database>;
};

const MAX_AMOUNT = Number.MAX_SAFE_INTEGER / 100;

function unauthorized() {
  return Response.json({ error: "请先登录" }, { status: 401 });
}

function validMoney(value: unknown) {
  const amount = Number(value);
  if (!Number.isFinite(amount) || amount < 0 || amount > MAX_AMOUNT) return null;
  const cents = Math.round(amount * 100);
  return Number.isSafeInteger(cents) ? cents : null;
}

export function createIncomeHandlers(dependencies: Dependencies) {
  async function GET(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return unauthorized();
      const db = await dependencies.getAssetsDb();
      const settings = await db.prepare(
        "SELECT monthly_salary, monthly_savings, updated_at FROM income_settings WHERE user_id = ?"
      ).bind(user.id).first<{ monthly_salary: number; monthly_savings: number; updated_at: string }>();
      return Response.json({ income: settings ?? { monthly_salary: 0, monthly_savings: 0, updated_at: null } });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "读取工资设置失败" }, { status: 500 });
    }
  }

  async function PUT(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return unauthorized();
      const body = await request.json() as { monthlySalary?: number; monthlySavings?: number };
      const monthlySalary = validMoney(body.monthlySalary);
      const monthlySavings = validMoney(body.monthlySavings);
      if (monthlySalary === null || monthlySavings === null) {
        return Response.json({ error: "请输入有效的工资和预计储蓄额" }, { status: 400 });
      }
      const db = await dependencies.getAssetsDb();
      const income = await db.prepare(`
        INSERT INTO income_settings (user_id, monthly_salary, monthly_savings, updated_at)
        VALUES (?, ?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(user_id) DO UPDATE SET
          monthly_salary = excluded.monthly_salary,
          monthly_savings = excluded.monthly_savings,
          updated_at = CURRENT_TIMESTAMP
        RETURNING monthly_salary, monthly_savings, updated_at
      `).bind(user.id, monthlySalary, monthlySavings).first();
      return Response.json({ income });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "保存工资设置失败" }, { status: 500 });
    }
  }

  return { GET, PUT };
}
