import { getAssetsDb, type AssetRow } from "../../../db/assets";

const samples = [
  ["贵州茅台", "stock", "600519", 28640000, 8.6, "核心持仓"],
  ["沪深300ETF", "fund", "510300", 19860000, 6.8, "宽基配置"],
  ["稳健货币基金", "money", "000198", 12800000, 1.52, "流动资金"],
  ["三年期定期存款", "deposit", null, 30000000, 2.6, "2028 年到期"],
  ["住房公积金", "housing", null, 16000000, 1.5, "每月持续缴存"],
  ["自住房产", "fixed", null, 40800000, 0, "按保守估值记录"],
] as const;

async function seedIfEmpty(db: D1Database) {
  await db.prepare(
    "UPDATE assets SET name = '沪深300ETF', code = '510300' WHERE name = '沪深300指数基金' AND code = '000300'"
  ).run();
  const count = await db.prepare("SELECT COUNT(*) AS count FROM assets").first<{ count: number }>();
  if ((count?.count ?? 0) > 0) return;
  await db.batch(
    samples.map((item) =>
      db.prepare(
        "INSERT INTO assets (name, category, code, amount, annual_rate, note) VALUES (?, ?, ?, ?, ?, ?)"
      ).bind(...item)
    )
  );
}

export async function GET() {
  try {
    const db = await getAssetsDb();
    await seedIfEmpty(db);
    const result = await db.prepare(
      "SELECT id, name, category, code, amount, annual_rate, note, created_at FROM assets ORDER BY id"
    ).all<AssetRow>();
    return Response.json({ assets: result.results });
  } catch (error) {
    return Response.json(
      { error: error instanceof Error ? error.message : "读取资产失败" },
      { status: 500 }
    );
  }
}

export async function POST(request: Request) {
  try {
    const body = (await request.json()) as Partial<{
      name: string;
      category: string;
      code: string;
      amount: number;
      annualRate: number;
      note: string;
    }>;
    const name = body.name?.trim();
    const category = body.category?.trim();
    const amount = Math.round(Number(body.amount) * 100);
    if (!name || !category || !Number.isFinite(amount) || amount <= 0) {
      return Response.json({ error: "请填写有效的资产名称和金额" }, { status: 400 });
    }
    const db = await getAssetsDb();
    const row = await db.prepare(
      "INSERT INTO assets (name, category, code, amount, annual_rate, note) VALUES (?, ?, ?, ?, ?, ?) RETURNING id, name, category, code, amount, annual_rate, note, created_at"
    ).bind(
      name,
      category,
      body.code?.trim() || null,
      amount,
      Number(body.annualRate) || 0,
      body.note?.trim() || ""
    ).first<AssetRow>();
    return Response.json({ asset: row }, { status: 201 });
  } catch (error) {
    return Response.json(
      { error: error instanceof Error ? error.message : "保存资产失败" },
      { status: 500 }
    );
  }
}

export async function DELETE(request: Request) {
  const id = Number(new URL(request.url).searchParams.get("id"));
  if (!Number.isInteger(id)) return Response.json({ error: "无效资产" }, { status: 400 });
  const db = await getAssetsDb();
  await db.prepare("DELETE FROM assets WHERE id = ?").bind(id).run();
  return Response.json({ ok: true });
}
