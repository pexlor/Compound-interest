import { getAssetsDb, type AssetRow } from "../../../db/assets";
import { getAuthenticatedUser } from "../../../db/auth";

const supportedCurrencies = new Set(["CNY", "USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"]);

function unauthorized() {
  return Response.json({ error: "请先登录" }, { status: 401 });
}

export async function GET(request: Request) {
  try {
    const user = await getAuthenticatedUser(request);
    if (!user) return unauthorized();
    const db = await getAssetsDb();
    const result = await db.prepare(
      "SELECT id, user_id, name, category, code, amount, currency, annual_rate, note, created_at FROM assets WHERE user_id = ? ORDER BY id"
    ).bind(user.id).all<AssetRow>();
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
    const user = await getAuthenticatedUser(request);
    if (!user) return unauthorized();
    const body = (await request.json()) as Partial<{
      name: string;
      category: string;
      code: string;
      amount: number;
      currency: string;
      annualRate: number;
      note: string;
    }>;
    const name = body.name?.trim();
    const category = body.category?.trim();
    const currency = body.currency?.trim().toUpperCase() || "CNY";
    const amount = Math.round(Number(body.amount) * 100);
    if (!name || !category || !Number.isFinite(amount) || amount <= 0) {
      return Response.json({ error: "请填写有效的资产名称和金额" }, { status: 400 });
    }
    if (!supportedCurrencies.has(currency)) {
      return Response.json({ error: "暂不支持这个计价币种" }, { status: 400 });
    }
    const db = await getAssetsDb();
    const row = await db.prepare(
      "INSERT INTO assets (user_id, name, category, code, amount, currency, annual_rate, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id, user_id, name, category, code, amount, currency, annual_rate, note, created_at"
    ).bind(
      user.id,
      name,
      category,
      body.code?.trim() || null,
      amount,
      currency,
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
  const user = await getAuthenticatedUser(request);
  if (!user) return unauthorized();
  const id = Number(new URL(request.url).searchParams.get("id"));
  if (!Number.isInteger(id)) return Response.json({ error: "无效资产" }, { status: 400 });
  const db = await getAssetsDb();
  const result = await db.prepare("DELETE FROM assets WHERE id = ? AND user_id = ?").bind(id, user.id).run();
  if (!result.meta.changes) return Response.json({ error: "资产不存在" }, { status: 404 });
  return Response.json({ ok: true });
}

export async function PATCH(request: Request) {
  const user = await getAuthenticatedUser(request);
  if (!user) return unauthorized();
  const body = (await request.json()) as { id?: number; annualRate?: number; amount?: number; currency?: string };
  const id = Number(body.id);
  if (!Number.isInteger(id)) {
    return Response.json({ error: "无效资产" }, { status: 400 });
  }
  const db = await getAssetsDb();

  if (body.amount !== undefined || body.currency !== undefined) {
    const amount = Math.round(Number(body.amount) * 100);
    const currency = body.currency?.trim().toUpperCase() || "";
    if (!Number.isFinite(amount) || amount <= 0) {
      return Response.json({ error: "请填写有效的当前市值" }, { status: 400 });
    }
    if (!supportedCurrencies.has(currency)) {
      return Response.json({ error: "暂不支持这个计价币种" }, { status: 400 });
    }
    const result = await db.prepare(
      "UPDATE assets SET amount = ?, currency = ? WHERE id = ? AND user_id = ?"
    ).bind(amount, currency, id, user.id).run();
    if (!result.meta.changes) return Response.json({ error: "资产不存在" }, { status: 404 });
    return Response.json({ ok: true, amount, currency });
  }

  const annualRate = Number(body.annualRate);
  if (!Number.isFinite(annualRate) || Math.abs(annualRate) > 1000) {
    return Response.json({ error: "无效的收益率数据" }, { status: 400 });
  }
  const result = await db.prepare(
    "UPDATE assets SET annual_rate = ? WHERE id = ? AND user_id = ?"
  ).bind(annualRate, id, user.id).run();
  if (!result.meta.changes) return Response.json({ error: "资产不存在" }, { status: 404 });
  return Response.json({ ok: true, annualRate });
}
