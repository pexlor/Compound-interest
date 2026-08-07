import { recordDailySnapshot } from "../../../db/history.ts";
import { createMarketCalculator } from "../market/calculator.ts";
import type { MarketQuote } from "../market/calculator.ts";
import { sharedQuoteCache } from "../market/quote-cache.ts";

type SnapshotAsset = {
  id: number;
  user_id: number;
  category: string;
  code: string | null;
  amount: number;
  quantity: number | null;
  currency: string;
};

type Dependencies = {
  db: D1Database;
  quote(category: string, code: string): Promise<MarketQuote>;
  userId?: number;
  snapshot?: typeof recordDailySnapshot;
  now?: () => Date;
  concurrency?: number;
};

async function mapLimited<T, R>(items: T[], limit: number, operation: (item: T) => Promise<R>) {
  const results: R[] = [];
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const index = next++;
      results[index] = await operation(items[index]);
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, () => worker()));
  return results;
}

export function createDailyAssetSnapshotService(dependencies: Dependencies) {
  const saveSnapshot = dependencies.snapshot ?? recordDailySnapshot;
  const now = dependencies.now ?? (() => new Date());
  const concurrency = Math.max(1, dependencies.concurrency ?? 4);

  return async function calculateAndRecordDailyAssets() {
    const query = dependencies.userId
      ? `SELECT id, user_id, category, code, amount, quantity, currency FROM assets WHERE user_id = ? ORDER BY id`
      : `SELECT id, user_id, category, code, amount, quantity, currency FROM assets WHERE user_id IS NOT NULL ORDER BY id`;
    const result = await dependencies.db.prepare(query).bind(...(dependencies.userId ? [dependencies.userId] : [])).all<SnapshotAsset>();
    const assets = result.results;
    const quotePromises = new Map<string, Promise<MarketQuote>>();
    const marketAssets = assets.filter((asset) =>
      (asset.category === "stock" || asset.category === "fund") && asset.code && asset.quantity && asset.quantity > 0
    );

    const settled = await mapLimited(marketAssets, concurrency, async (asset) => {
      const key = `${asset.category}:${asset.code!.trim().toUpperCase()}`;
      let pending = quotePromises.get(key);
      if (!pending) {
        pending = dependencies.quote(asset.category, asset.code!);
        quotePromises.set(key, pending);
      }
      try {
        const quote = await pending;
        return { asset, quote };
      } catch (error) {
        return { asset, error: error instanceof Error ? error.message : "实时价格读取失败" };
      }
    });

    const updates = settled.flatMap((item) => item.quote ? [dependencies.db.prepare(
      "UPDATE assets SET amount = ?, currency = ? WHERE id = ? AND user_id = ?"
    ).bind(
      Math.round(item.asset.quantity! * item.quote.currentPrice * 100),
      item.quote.priceCurrency,
      item.asset.id,
      item.asset.user_id,
    )] : []);
    for (let index = 0; index < updates.length; index += 100) {
      await dependencies.db.batch(updates.slice(index, index + 100));
    }

    const snapshotTime = now();
    const userIds = dependencies.userId ? [dependencies.userId] : [...new Set(assets.map((asset) => asset.user_id))];
    const snapshots = await mapLimited(userIds, concurrency, (userId) =>
      saveSnapshot(dependencies.db, userId, "scheduled_daily", snapshotTime)
    );
    const errors = settled.flatMap((item) => item.error ? [{
      id: item.asset.id,
      category: item.asset.category,
      code: item.asset.code,
      error: item.error,
    }] : []);
    return { users: userIds.length, updatedAssets: updates.length, recordedSnapshots: snapshots.filter(Boolean).length, errors };
  };
}

export function runDailyAssetSnapshot(db: D1Database, fetcher: typeof fetch) {
  const calculator = createMarketCalculator({ fetch: fetcher, quoteCache: sharedQuoteCache });
  return createDailyAssetSnapshotService({ db, quote: calculator.quote })();
}

export function runUserDailyAssetSnapshot(db: D1Database, userId: number, fetcher: typeof fetch) {
  const calculator = createMarketCalculator({ fetch: fetcher, quoteCache: sharedQuoteCache });
  return createDailyAssetSnapshotService({ db, userId, quote: calculator.quote })();
}
