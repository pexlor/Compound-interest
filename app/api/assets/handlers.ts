import type { AssetRow } from "../../../db/assets";
import type { HistoryRow } from "../../../db/history";

const supportedCategories = new Set(["stock", "fund", "money", "deposit", "housing", "fixed"]);
const supportedCurrencies = new Set(["CNY", "USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"]);
const supportedInvestmentStrategies = new Set(["none", "monthly", "weekly", "yearly", "daily"]);
const MAX_AMOUNT = Number.MAX_SAFE_INTEGER / 100;

function supportsInvestment(category: string, code: string | null) {
  return category === "fund" || category === "stock" && Boolean(code && /^[A-Z][A-Z0-9.-]*$/i.test(code));
}

type User = { id: number };
type Dependencies = {
  getAuthenticatedUser(request: Request): Promise<User | null>;
  getAssetsDb(): Promise<D1Database>;
  recordDailySnapshot?(db: D1Database, userId: number, trigger: "asset_change"): Promise<HistoryRow | null>;
};

function unauthorized() {
  return Response.json({ error: "请先登录" }, { status: 401 });
}

function validRate(value: unknown) {
  const rate = Number(value);
  return Number.isFinite(rate) && rate >= -100 && rate <= 1000 ? rate : null;
}

function validAmount(value: unknown) {
  const amount = Number(value);
  if (!Number.isFinite(amount) || amount <= 0 || amount > MAX_AMOUNT) return null;
  const cents = Math.round(amount * 100);
  return Number.isSafeInteger(cents) ? cents : null;
}

export function createAssetsHandlers(dependencies: Dependencies) {
  async function snapshot(db: D1Database, userId: number) {
    try {
      return await dependencies.recordDailySnapshot?.(db, userId, "asset_change") ?? null;
    } catch {
      return null;
    }
  }

  async function GET(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return unauthorized();
      const db = await dependencies.getAssetsDb();
      const result = await db.prepare(
        "SELECT id, user_id, name, category, code, amount, currency, annual_rate, investment_strategy, investment_amount, note, created_at FROM assets WHERE user_id = ? ORDER BY id"
      ).bind(user.id).all<AssetRow>();
      return Response.json({ assets: result.results });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "读取资产失败" }, { status: 500 });
    }
  }

  async function POST(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return unauthorized();
      const body = (await request.json()) as Partial<{
        name: string; category: string; code: string; amount: number; currency: string; annualRate: number; note: string; investmentStrategy: string; investmentAmount: number;
      }>;
      const name = body.name?.trim() ?? "";
      const category = body.category?.trim() ?? "";
      const currency = body.currency?.trim().toUpperCase() || "CNY";
      const code = body.code?.trim().toUpperCase() || null;
      const note = body.note?.trim() || "";
      const amount = validAmount(body.amount);
      const annualRate = validRate(body.annualRate ?? 0);
      const investmentStrategy = body.investmentStrategy?.trim() || "none";
      const investmentAmount = investmentStrategy === "none" ? null : validAmount(body.investmentAmount);

      if (!name || name.length > 120 || amount === null) {
        return Response.json({ error: "请填写有效的资产名称和金额" }, { status: 400 });
      }
      if (!supportedCategories.has(category)) {
        return Response.json({ error: "暂不支持这个资产类别" }, { status: 400 });
      }
      if (!supportedCurrencies.has(currency)) {
        return Response.json({ error: "暂不支持这个计价币种" }, { status: 400 });
      }
      if (annualRate === null) {
        return Response.json({ error: "无效的收益率数据" }, { status: 400 });
      }
      if (!supportedInvestmentStrategies.has(investmentStrategy) || investmentStrategy !== "none" && !supportsInvestment(category, code) || investmentStrategy !== "none" && investmentAmount === null) {
        return Response.json({ error: "基金定投策略或金额无效" }, { status: 400 });
      }
      if ((code?.length ?? 0) > 32 || note.length > 500) {
        return Response.json({ error: "代码或备注内容过长" }, { status: 400 });
      }

      const db = await dependencies.getAssetsDb();
      const row = await db.prepare(
        "INSERT INTO assets (user_id, name, category, code, amount, currency, annual_rate, investment_strategy, investment_amount, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id, user_id, name, category, code, amount, currency, annual_rate, investment_strategy, investment_amount, note, created_at"
      ).bind(user.id, name, category, code, amount, currency, annualRate, investmentStrategy, investmentAmount, note).first<AssetRow>();
      return Response.json({ asset: row, snapshot: await snapshot(db, user.id) }, { status: 201 });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "保存资产失败" }, { status: 500 });
    }
  }

  async function DELETE(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return unauthorized();
      const id = Number(new URL(request.url).searchParams.get("id"));
      if (!Number.isInteger(id) || id <= 0) return Response.json({ error: "无效资产" }, { status: 400 });
      const db = await dependencies.getAssetsDb();
      const result = await db.prepare("DELETE FROM assets WHERE id = ? AND user_id = ?").bind(id, user.id).run();
      if (!result.meta.changes) return Response.json({ error: "资产不存在" }, { status: 404 });
      return Response.json({ ok: true, snapshot: await snapshot(db, user.id) });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "删除资产失败" }, { status: 500 });
    }
  }

  async function PATCH(request: Request) {
    try {
      const user = await dependencies.getAuthenticatedUser(request);
      if (!user) return unauthorized();
      const body = (await request.json()) as { id?: number; annualRate?: number; amount?: number; currency?: string; investmentStrategy?: string; investmentAmount?: number };
      const id = Number(body.id);
      if (!Number.isInteger(id) || id <= 0) return Response.json({ error: "无效资产" }, { status: 400 });
      const db = await dependencies.getAssetsDb();

      if ((body.investmentStrategy !== undefined || body.investmentAmount !== undefined) && body.amount === undefined && body.currency === undefined) {
        const strategy = body.investmentStrategy?.trim() || "none";
        const investmentAmount = strategy === "none" ? null : validAmount(body.investmentAmount);
        if (!supportedInvestmentStrategies.has(strategy) || strategy !== "none" && investmentAmount === null) return Response.json({ error: "基金定投策略或金额无效" }, { status: 400 });
        const result = await db.prepare(`UPDATE assets SET investment_strategy = ?, investment_amount = ? WHERE id = ? AND user_id = ?${strategy === "none" ? "" : " AND (category = 'fund' OR (category = 'stock' AND code IS NOT NULL AND code NOT GLOB '[0-9]*'))"}`)
          .bind(strategy, investmentAmount, id, user.id).run();
        if (!result.meta.changes) return Response.json({ error: "资产不存在或不是基金" }, { status: 404 });
        return Response.json({ ok: true, investmentStrategy: strategy, investmentAmount, snapshot: await snapshot(db, user.id) });
      }

      if (body.amount !== undefined || body.currency !== undefined || body.investmentStrategy !== undefined || body.investmentAmount !== undefined) {
        const amount = validAmount(body.amount);
        const currency = body.currency?.trim().toUpperCase() || "";
        if (amount === null) return Response.json({ error: "请填写有效的当前市值" }, { status: 400 });
        if (!supportedCurrencies.has(currency)) return Response.json({ error: "暂不支持这个计价币种" }, { status: 400 });
        const changesInvestment = body.investmentStrategy !== undefined || body.investmentAmount !== undefined;
        const strategy = body.investmentStrategy?.trim() || "none";
        const investmentAmount = strategy === "none" ? null : validAmount(body.investmentAmount);
        if (changesInvestment && (!supportedInvestmentStrategies.has(strategy) || strategy !== "none" && investmentAmount === null)) return Response.json({ error: "基金定投策略或金额无效" }, { status: 400 });
        const result = changesInvestment
          ? await db.prepare(`UPDATE assets SET amount = ?, currency = ?, investment_strategy = ?, investment_amount = ? WHERE id = ? AND user_id = ?${strategy === "none" ? "" : " AND (category = 'fund' OR (category = 'stock' AND code IS NOT NULL AND code NOT GLOB '[0-9]*'))"}`)
            .bind(amount, currency, strategy, investmentAmount, id, user.id).run()
          : await db.prepare("UPDATE assets SET amount = ?, currency = ? WHERE id = ? AND user_id = ?")
            .bind(amount, currency, id, user.id).run();
        if (!result.meta.changes) return Response.json({ error: "资产不存在" }, { status: 404 });
        return Response.json({ ok: true, amount, currency, investmentStrategy: changesInvestment ? strategy : undefined, investmentAmount: changesInvestment ? investmentAmount : undefined, snapshot: await snapshot(db, user.id) });
      }

      const annualRate = validRate(body.annualRate);
      if (annualRate === null) return Response.json({ error: "无效的收益率数据" }, { status: 400 });
      const result = await db.prepare("UPDATE assets SET annual_rate = ? WHERE id = ? AND user_id = ?")
        .bind(annualRate, id, user.id).run();
      if (!result.meta.changes) return Response.json({ error: "资产不存在" }, { status: 404 });
      return Response.json({ ok: true, annualRate });
    } catch (error) {
      return Response.json({ error: error instanceof Error ? error.message : "修改资产失败" }, { status: 500 });
    }
  }

  return { GET, POST, DELETE, PATCH };
}
