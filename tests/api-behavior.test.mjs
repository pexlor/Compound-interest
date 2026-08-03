import assert from "node:assert/strict";
import test from "node:test";

const load = async (path) => {
  try {
    return await import(new URL(`../${path}`, import.meta.url));
  } catch {
    assert.fail(`${path} behavior module is missing`);
  }
};

function createAssetDb() {
  const rows = [];
  let nextId = 1;
  return {
    prepare(sql) {
      return {
        bind(...values) {
          return {
            async all() {
              if (sql.startsWith("SELECT")) {
                return { results: rows.filter((row) => row.user_id === values[0]) };
              }
              throw new Error(`Unexpected all query: ${sql}`);
            },
            async first() {
              if (!sql.startsWith("INSERT INTO assets")) throw new Error(`Unexpected first query: ${sql}`);
              const [userId, name, category, code, amount, currency, annualRate, note] = values;
              const row = {
                id: nextId++, user_id: userId, name, category, code, amount, currency,
                annual_rate: annualRate, note, created_at: "2026-08-03 00:00:00",
              };
              rows.push(row);
              return row;
            },
            async run() {
              if (sql.startsWith("UPDATE assets SET annual_rate")) {
                const [annualRate, id, userId] = values;
                const row = rows.find((item) => item.id === id && item.user_id === userId);
                if (!row) return { meta: { changes: 0 } };
                row.annual_rate = annualRate;
                return { meta: { changes: 1 } };
              }
              if (sql.startsWith("UPDATE assets SET amount")) {
                const [amount, currency, id, userId] = values;
                const row = rows.find((item) => item.id === id && item.user_id === userId);
                if (!row) return { meta: { changes: 0 } };
                row.amount = amount;
                row.currency = currency;
                return { meta: { changes: 1 } };
              }
              if (sql.startsWith("DELETE FROM assets")) {
                const [id, userId] = values;
                const index = rows.findIndex((item) => item.id === id && item.user_id === userId);
                if (index < 0) return { meta: { changes: 0 } };
                rows.splice(index, 1);
                return { meta: { changes: 1 } };
              }
              throw new Error(`Unexpected run query: ${sql}`);
            },
          };
        },
      };
    },
  };
}

function createSnapshotDb() {
  const assets = [{ user_id: 1, amount: 10000, currency: "CNY" }];
  const rates = [{ currency: "CNY", cny_rate: 1, rate_date: "2026-08-03" }];
  const history = new Map();
  return {
    assets,
    rates,
    history,
    prepare(sql) {
      return {
        bind(...values) {
          return {
            async all() {
              if (sql.includes("FROM assets")) return { results: assets.filter((row) => row.user_id === values[0]) };
              if (sql.includes("FROM exchange_rates")) return { results: rates };
              throw new Error(`Unexpected snapshot all query: ${sql}`);
            },
            async first() {
              if (!sql.includes("INSERT INTO asset_history")) throw new Error(`Unexpected snapshot first query: ${sql}`);
              const [userId, snapshotDate, totalCny, trigger, rateDate] = values;
              const key = `${userId}:${snapshotDate}`;
              const previous = history.get(key);
              const row = {
                id: previous?.id ?? history.size + 1,
                user_id: userId,
                snapshot_date: snapshotDate,
                total_cny: totalCny,
                trigger,
                rate_date: rateDate,
                created_at: previous?.created_at ?? "2026-08-03 00:00:00",
                updated_at: "2026-08-03 01:00:00",
              };
              history.set(key, row);
              return row;
            },
          };
        },
      };
    },
  };
}

test("daily asset snapshot uses complete rates and overwrites the same date", async () => {
  const { calculateSnapshotTotal, recordDailySnapshot } = await load("db/history.ts");
  assert.equal(calculateSnapshotTotal([
    { amount: 10000, currency: "CNY" },
    { amount: 20000, currency: "USD" },
  ], { CNY: 1, USD: 7 }), 150000);
  assert.equal(calculateSnapshotTotal([{ amount: 20000, currency: "USD" }], { CNY: 1 }), null);

  const db = createSnapshotDb();
  const first = await recordDailySnapshot(db, 1, "asset_change", new Date("2026-08-03T01:00:00Z"));
  assert.equal(first.total_cny, 10000);
  db.assets[0].amount = 25000;
  const second = await recordDailySnapshot(db, 1, "exchange_refresh", new Date("2026-08-03T12:00:00Z"));
  assert.equal(second.total_cny, 25000);
  assert.equal(second.trigger, "exchange_refresh");
  assert.equal(db.history.size, 1);
});

test("history API authenticates, isolates users, clamps limits, and returns ascending dates", async () => {
  const { createHistoryHandler } = await load("app/api/history/handlers.ts");
  const rows = [
    { id: 1, user_id: 1, snapshot_date: "2026-08-03", total_cny: 120000, trigger: "asset_change" },
    { id: 2, user_id: 2, snapshot_date: "2026-08-02", total_cny: 999999, trigger: "asset_change" },
    { id: 3, user_id: 1, snapshot_date: "2026-08-01", total_cny: 100000, trigger: "exchange_refresh" },
  ];
  let boundLimit = 0;
  const db = {
    prepare: () => ({ bind: (userId, limit) => ({ all: async () => {
      boundLimit = limit;
      return { results: rows.filter((row) => row.user_id === userId).sort((a, b) => a.snapshot_date.localeCompare(b.snapshot_date)) };
    } }) }),
  };
  const authenticated = createHistoryHandler({ getAuthenticatedUser: async () => ({ id: 1 }), getAssetsDb: async () => db });
  const response = await authenticated(new Request("http://local/api/history?limit=99999"));
  assert.equal(response.status, 200);
  assert.equal(boundLimit, 3650);
  assert.deepEqual((await response.json()).history.map((row) => row.snapshot_date), ["2026-08-01", "2026-08-03"]);

  const unauthorized = createHistoryHandler({ getAuthenticatedUser: async () => null, getAssetsDb: async () => db });
  assert.equal((await unauthorized(new Request("http://local/api/history"))).status, 401);
});

test("asset API validates input and isolates CRUD by signed-in user", async () => {
  const { createAssetsHandlers } = await load("app/api/assets/handlers.ts");
  const db = createAssetDb();
  let currentUser = { id: 1 };
  const handlers = createAssetsHandlers({
    getAuthenticatedUser: async () => currentUser,
    getAssetsDb: async () => db,
  });

  const invalid = await handlers.POST(new Request("http://local/api/assets", {
    method: "POST",
    body: JSON.stringify({ name: "bad", category: "crypto", amount: 100, currency: "CNY", annualRate: 2 }),
  }));
  assert.equal(invalid.status, 400);

  const created = await handlers.POST(new Request("http://local/api/assets", {
    method: "POST",
    body: JSON.stringify({ name: "存款", category: "deposit", amount: 1000.25, currency: "CNY", annualRate: 2.5 }),
  }));
  assert.equal(created.status, 201);
  const asset = (await created.json()).asset;
  assert.equal(asset.amount, 100025);

  currentUser = { id: 2 };
  assert.deepEqual((await (await handlers.GET(new Request("http://local/api/assets"))).json()).assets, []);
  assert.equal((await handlers.PATCH(new Request("http://local/api/assets", {
    method: "PATCH", body: JSON.stringify({ id: asset.id, annualRate: 4 }),
  }))).status, 404);
  assert.equal((await handlers.DELETE(new Request(`http://local/api/assets?id=${asset.id}`, { method: "DELETE" }))).status, 404);

  currentUser = { id: 1 };
  const patched = await handlers.PATCH(new Request("http://local/api/assets", {
    method: "PATCH", body: JSON.stringify({ id: asset.id, annualRate: 4 }),
  }));
  assert.equal(patched.status, 200);
  assert.equal((await handlers.DELETE(new Request(`http://local/api/assets?id=${asset.id}`, { method: "DELETE" }))).status, 200);
});

test("asset API rejects unsafe numeric values", async () => {
  const { createAssetsHandlers } = await load("app/api/assets/handlers.ts");
  const handlers = createAssetsHandlers({
    getAuthenticatedUser: async () => ({ id: 1 }),
    getAssetsDb: async () => createAssetDb(),
  });
  for (const body of [
    { name: "x", category: "deposit", amount: 1e20, currency: "CNY", annualRate: 1 },
    { name: "x", category: "deposit", amount: 10, currency: "CNY", annualRate: 1001 },
    { name: "x".repeat(121), category: "deposit", amount: 10, currency: "CNY", annualRate: 1 },
  ]) {
    const response = await handlers.POST(new Request("http://local/api/assets", { method: "POST", body: JSON.stringify(body) }));
    assert.equal(response.status, 400);
  }
});

test("asset writes record a snapshot except for annual-rate-only updates", async () => {
  const { createAssetsHandlers } = await load("app/api/assets/handlers.ts");
  const db = createAssetDb();
  const calls = [];
  const handlers = createAssetsHandlers({
    getAuthenticatedUser: async () => ({ id: 1 }),
    getAssetsDb: async () => db,
    recordDailySnapshot: async (...args) => {
      calls.push(args);
      return { snapshot_date: "2026-08-03", total_cny: 10000 };
    },
  });
  const created = await handlers.POST(new Request("http://local/api/assets", {
    method: "POST",
    body: JSON.stringify({ name: "存款", category: "deposit", amount: 100, currency: "CNY", annualRate: 2 }),
  }));
  const asset = (await created.json()).asset;
  await handlers.PATCH(new Request("http://local/api/assets", {
    method: "PATCH", body: JSON.stringify({ id: asset.id, annualRate: 3 }),
  }));
  await handlers.PATCH(new Request("http://local/api/assets", {
    method: "PATCH", body: JSON.stringify({ id: asset.id, amount: 120, currency: "CNY" }),
  }));
  await handlers.DELETE(new Request(`http://local/api/assets?id=${asset.id}`, { method: "DELETE" }));
  assert.equal(calls.length, 3);
  assert.deepEqual(calls.map((call) => call.slice(1)), [[1, "asset_change"], [1, "asset_change"], [1, "asset_change"]]);
});

test("registration creates a hashed local account and session", async () => {
  const { createRegisterHandler } = await load("app/api/auth/register/handler.ts");
  const calls = [];
  const handler = createRegisterHandler({
    getAssetsDb: async () => ({
      prepare: () => ({ bind: (...values) => ({ first: async () => {
        calls.push(values);
        return { id: 7, email: values[0], display_name: values[1] };
      } }) }),
    }),
    hashNewPassword: async () => ({ hash: "hash", salt: "salt", iterations: 210000 }),
    createSession: async (_db, userId) => `token-${userId}`,
    sessionCookie: (token) => `session=${token}; HttpOnly`,
  });
  const response = await handler(new Request("http://local/api/auth/register", {
    method: "POST",
    body: JSON.stringify({ displayName: "测试用户", email: "USER@example.com", password: "password123" }),
  }));
  assert.equal(response.status, 201);
  assert.equal(response.headers.get("set-cookie"), "session=token-7; HttpOnly");
  assert.deepEqual(calls[0], ["user@example.com", "测试用户", "hash", "salt", 210000]);
});

function fundPage(pageIndex) {
  const latest = new Date(Date.UTC(2026, 7, 3));
  const points = Array.from({ length: 100 }, (_, offset) => {
    const day = (pageIndex - 1) * 100 + offset;
    const date = new Date(latest.getTime() - day * 86400000).toISOString().slice(0, 10);
    return { FSRQ: date, DWJZ: String(2 - day / 5000), LJJZ: String(2 - day / 5000) };
  });
  return { Data: { LSJZList: points, TotalCount: 2000 } };
}

test("market API authenticates, covers ten years, caches, and limits upstream concurrency", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  let active = 0;
  let maximumActive = 0;
  let calls = 0;
  const fetch = async (url) => {
    calls += 1;
    active += 1;
    maximumActive = Math.max(maximumActive, active);
    await new Promise((resolve) => setTimeout(resolve, 1));
    active -= 1;
    const pageIndex = Number(new URL(url).searchParams.get("pageIndex"));
    return Response.json(fundPage(pageIndex));
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch, maxConcurrent: 3 });
  const request = new Request("http://local/api/market?code=000001&category=fund&days=3650");
  const first = await handler(request);
  assert.equal(first.status, 200);
  const payload = await first.json();
  assert.ok(Date.parse(payload.endDate) - Date.parse(payload.startDate) >= 3400 * 86400000);
  assert.ok(calls >= 35);
  assert.ok(maximumActive <= 3);

  const beforeCache = calls;
  assert.equal((await handler(request)).status, 200);
  assert.equal(calls, beforeCache);

  const unauthorized = createMarketHandler({ authenticate: async () => null, fetch });
  assert.equal((await unauthorized(request)).status, 401);
});

test("market API aborts slow upstream requests", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const fetch = async (_url, init) => new Promise((_resolve, reject) => {
    init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true });
  });
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch, timeoutMs: 5 });
  const response = await handler(new Request("http://local/api/market?code=600000&category=stock&days=365"));
  assert.equal(response.status, 504);
});

test("QQQ ten-year history stays within Tencent's request limit", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const urls = [];
  const fetch = async (url) => {
    urls.push(String(url));
    if (String(url).includes("day,,,2,qfq")) {
      return Response.json({ data: { usQQQ: { qt: { usQQQ: ["delay", "QQQ", "QQQ.OQ"] } } } });
    }
    if (String(url).includes("usQQQ.OQ")) {
      return Response.json({ data: { "usQQQ.OQ": { day: [["2018-08-15", "180", "180"], ["2026-07-31", "687.99", "687.99"]] } } });
    }
    return Response.json({ data: { usQQQ: { day: [["2011-06-02", "57.18", "57.18"], ["2026-07-31", "687.99", "687.99"]] } } });
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=QQQ&category=stock&days=3650"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.ok(Date.parse(payload.startDate) <= Date.parse("2016-08-03"));
  assert.ok(urls.some((url) => url.includes("day,,,2000,qfq") && url.includes("usQQQ")));
});

const validRates = ["USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"].map((quote) => ({
  date: "2026-08-03", base: "CNY", quote, rate: quote === "USD" ? 0.14 : 1,
}));

test("portfolio totals are unavailable instead of partial when an exchange rate is missing", async () => {
  const { calculatePortfolio } = await load("app/portfolio.ts");
  const assets = [
    { category: "deposit", amount: 10000, currency: "CNY", annual_rate: 2 },
    { category: "stock", amount: 10000, currency: "USD", annual_rate: 8 },
  ];
  assert.equal(calculatePortfolio(assets, { CNY: 1 }, 3), null);
  const result = calculatePortfolio(assets, { CNY: 1, USD: 7 }, 3);
  assert.equal(result.total, 80000);
  assert.ok(result.forecast > result.total);
});

test("exchange-rate API falls back to stale cache and rejects incomplete data", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  let mode = "valid";
  const fetch = async () => mode === "valid" ? Response.json(validRates) : mode === "incomplete"
    ? Response.json(validRates.slice(0, -1))
    : new Response("down", { status: 503 });
  const handler = createExchangeRatesHandler({ authenticate: async () => ({ id: 1 }), fetch });

  const first = await handler(new Request("http://local/api/exchange-rates"));
  assert.equal(first.status, 200);
  assert.ok(Math.abs((await first.json()).rates.USD - 1 / 0.14) < 1e-10);

  mode = "down";
  const stale = await handler(new Request("http://local/api/exchange-rates?refresh=1"));
  assert.equal(stale.status, 200);
  assert.equal((await stale.json()).stale, true);

  mode = "incomplete";
  const emptyHandler = createExchangeRatesHandler({ authenticate: async () => ({ id: 1 }), fetch });
  assert.equal((await emptyHandler(new Request("http://local/api/exchange-rates"))).status, 502);
});

test("exchange-rate API uses the backup provider when Frankfurter is unavailable", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  const fetch = async (url) => {
    if (String(url).includes("frankfurter")) return new Response("down", { status: 503 });
    return Response.json({ base: "CNY", date: "2026-08-03", rates: Object.fromEntries(validRates.map((row) => [row.quote, 1 / row.rate])) });
  };
  const handler = createExchangeRatesHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/exchange-rates"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.source, "ExchangeRate-API 备用汇率");
  assert.equal(payload.rates.CNY, 1);
});

test("manual exchange-rate refresh saves rates and records a snapshot", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  const calls = [];
  const db = { name: "db" };
  const handler = createExchangeRatesHandler({
    authenticate: async () => ({ id: 7 }),
    fetch: async () => Response.json(validRates),
    getAssetsDb: async () => db,
    saveExchangeRates: async (...args) => calls.push(["rates", ...args]),
    recordDailySnapshot: async (...args) => {
      calls.push(["snapshot", ...args]);
      return { snapshot_date: "2026-08-03", total_cny: 70000 };
    },
  });
  const response = await handler(new Request("http://local/api/exchange-rates?refresh=1"));
  assert.equal(response.status, 200);
  assert.equal(calls[0][0], "rates");
  assert.deepEqual(calls[1], ["snapshot", db, 7, "exchange_refresh"]);
  assert.equal((await response.json()).snapshot.total_cny, 70000);
});
