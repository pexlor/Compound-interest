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
              const [userId, name, category, code, amount, quantity, currency, annualRate, investmentStrategy, investmentAmount, note] = values;
              const row = {
                id: nextId++, user_id: userId, name, category, code, amount, quantity, currency,
                annual_rate: annualRate, investment_strategy: investmentStrategy ?? "none", investment_amount: investmentAmount ?? null, note: note ?? "", created_at: "2026-08-03 00:00:00",
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
              if (sql.startsWith("UPDATE assets SET investment_strategy")) {
                const [strategy, investmentAmount, id, userId] = values;
                const row = rows.find((item) => item.id === id && item.user_id === userId);
                if (!row) return { meta: { changes: 0 } };
                row.investment_strategy = strategy;
                row.investment_amount = investmentAmount;
                return { meta: { changes: 1 } };
              }
              if (sql.startsWith("UPDATE assets SET amount")) {
                const [amount, quantity, currency, id, userId] = values;
                const row = rows.find((item) => item.id === id && item.user_id === userId);
                if (!row) return { meta: { changes: 0 } };
                row.amount = amount;
                row.quantity = quantity ?? row.quantity;
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

function createMarketReturnDb() {
  const rows = new Map();
  const db = {
    rows,
    assets: [],
    prepare(sql) {
      return {
        bind(...values) {
          return {
            async all() {
              if (!sql.includes("FROM assets")) throw new Error(`Unexpected market return all query: ${sql}`);
              const userId = sql.includes("user_id = ?") ? values[0] : null;
              return {
                results: db.assets.filter((row) => userId === null || row.user_id === userId),
              };
            },
            async first() {
              if (sql.startsWith("INSERT INTO market_returns")) {
                const [category, code, lookbackDays, calculationDate, annualRate, periodReturn, requestedDays, actualDays, historyLimited, startDate, endDate, source, calculatedAt] = values;
                const key = `${category}:${code}:${lookbackDays}:${calculationDate}`;
                const row = {
                  id: rows.get(key)?.id ?? rows.size + 1,
                  category, code, lookback_days: lookbackDays, calculation_date: calculationDate,
                  annual_rate: annualRate, period_return: periodReturn, requested_days: requestedDays,
                  actual_days: actualDays, history_limited: historyLimited, start_date: startDate,
                  end_date: endDate, source, calculated_at: calculatedAt,
                };
                rows.set(key, row);
                return row;
              }
              if (sql.includes("FROM market_returns") && sql.includes("calculation_date = ?")) {
                return rows.get(`${values[0]}:${values[1]}:${values[2]}:${values[3]}`) ?? null;
              }
              if (sql.includes("FROM market_returns") && sql.includes("ORDER BY calculation_date DESC")) {
                return [...rows.values()]
                  .filter((row) => row.category === values[0] && row.code === values[1] && row.lookback_days === values[2])
                  .sort((a, b) => b.calculation_date.localeCompare(a.calculation_date))[0] ?? null;
              }
              throw new Error(`Unexpected market return query: ${sql}`);
            },
          };
        },
      };
    },
  };
  return db;
}

function createExchangeRateHistoryDb() {
  const rows = new Map();
  const exchangeRates = new Map();
  const batchSizes = [];
  const statement = (sql, values = []) => ({
    sql,
    values,
    bind(...nextValues) {
      return statement(sql, nextValues);
    },
    async first() {
      if (sql.includes("FROM exchange_rate_history")) {
        const [currency, onOrBefore] = values;
        return [...rows.values()]
          .filter((row) => row.currency === currency && row.rate_date <= onOrBefore)
          .sort((a, b) => b.rate_date.localeCompare(a.rate_date))[0] ?? null;
      }
      throw new Error(`Unexpected exchange rate history first query: ${sql}`);
    },
    async all() {
      if (sql.includes("FROM exchange_rates")) return { results: [...exchangeRates.values()] };
      if (sql.includes("FROM exchange_rate_history")) {
        if (sql.includes("WHERE rate_date =")) {
          const currencies = values.slice(0, supportedExchangeRateCurrencies.length);
          const completeDate = [...new Set([...rows.values()]
            .filter((row) => currencies.includes(row.currency))
            .filter((row, _index, allRows) => allRows.filter((item) => item.rate_date === row.rate_date).length === currencies.length)
            .map((row) => row.rate_date))]
            .sort()
            .at(-1);
          return {
            results: completeDate
              ? [...rows.values()].filter((row) => row.rate_date === completeDate && currencies.includes(row.currency))
              : [],
          };
        }
        return { results: [...rows.values()] };
      }
      throw new Error(`Unexpected exchange rate history all query: ${sql}`);
    },
    async run() {
      if (sql.startsWith("DELETE FROM exchange_rate_history")) {
        let changes = 0;
        for (const [key, row] of rows) {
          if (row.rate_date < values[0]) {
            rows.delete(key);
            changes += 1;
          }
        }
        return { meta: { changes } };
      }
      throw new Error(`Unexpected exchange rate history run query: ${sql}`);
    },
  });
  return {
    rows,
    exchangeRates,
    batchSizes,
    prepare(sql) {
      return statement(sql);
    },
    async batch(statements) {
      batchSizes.push(statements.length);
      return statements.map(({ sql, values }) => {
        if (sql.startsWith("INSERT OR IGNORE INTO exchange_rate_history")) {
          const [currency, cnyRate, rateDate, source, fetchedAt] = values;
          const key = `${currency}:${rateDate}`;
          if (rows.has(key)) return { meta: { changes: 0 } };
          rows.set(key, {
            currency, cny_rate: cnyRate, rate_date: rateDate, source, fetched_at: fetchedAt,
          });
          return { meta: { changes: 1 } };
        }
        if (sql.startsWith("INSERT INTO exchange_rates")) {
          const [currency, cnyRate, rateDate] = values;
          exchangeRates.set(currency, { currency, cny_rate: cnyRate, rate_date: rateDate });
          return { meta: { changes: 1 } };
        }
        throw new Error(`Unexpected exchange rate history batch query: ${sql}`);
      });
    },
  };
}

function sampleExchangeRateHistoryEntry(index, overrides = {}) {
  return {
    currency: "USD",
    cnyRate: 7 + index / 1000,
    rateDate: new Date(Date.UTC(2026, 0, index + 1)).toISOString().slice(0, 10),
    source: "test",
    fetchedAt: "2026-08-03T00:00:00.000Z",
    ...overrides,
  };
}

test("exchange rate history inserts idempotently in batches of at most 100", async () => {
  const { importExchangeRateHistory } = await load("db/exchange-rate-history.ts");
  const db = createExchangeRateHistoryDb();
  const entries = Array.from({ length: 202 }, (_, index) => sampleExchangeRateHistoryEntry(index));

  assert.deepEqual(await importExchangeRateHistory(db, entries), { inserted: 202 });
  assert.deepEqual(db.batchSizes, [100, 100, 2]);

  const changed = entries.map((entry) => ({ ...entry, cnyRate: entry.cnyRate + 1 }));
  assert.deepEqual(await importExchangeRateHistory(db, changed), { inserted: 0 });
  assert.equal(db.rows.get(`${entries[0].currency}:${entries[0].rateDate}`).cny_rate, entries[0].cnyRate);
});

test("exchange rate history inserts are found by the latest date on or before the target", async () => {
  const { findHistoricalCnyRate, importExchangeRateHistory } = await load("db/exchange-rate-history.ts");
  const db = createExchangeRateHistoryDb();
  await importExchangeRateHistory(db, [
    sampleExchangeRateHistoryEntry(0, { rateDate: "2026-07-30", cnyRate: 7.1 }),
    sampleExchangeRateHistoryEntry(1, { rateDate: "2026-07-31", cnyRate: 7.2 }),
    sampleExchangeRateHistoryEntry(2, { rateDate: "2026-08-01", cnyRate: 7.3 }),
  ]);

  assert.deepEqual(await findHistoricalCnyRate(db, "USD", "2026-07-31"), {
    currency: "USD",
    cnyRate: 7.2,
    rateDate: "2026-07-31",
    source: "test",
    fetchedAt: "2026-08-03T00:00:00.000Z",
  });
  assert.equal(await findHistoricalCnyRate(db, "HKD", "2026-07-31"), null);
});

const supportedExchangeRateCurrencies = ["USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"];

function sampleFrankfurterRows(date = "2026-07-31") {
  return [
    { date, base: "USD", quote: "CNY", rate: 7.2 },
    ...supportedExchangeRateCurrencies.filter((currency) => currency !== "USD").map((quote, index) => ({
      date, base: "CNY", quote, rate: 0.1 + index / 100,
    })),
  ];
}

function createMemoryRatesBucket(initialText = null, options = {}) {
  let text = initialText;
  let etag = initialText === null ? null : "etag-1";
  let version = initialText === null ? 0 : 1;
  let conflictOnce = options.conflictOnce ?? false;
  const gets = [];
  const puts = [];
  return {
    gets,
    puts,
    currentText: () => text,
    async get(key) {
      gets.push(key);
      if (text === null) return null;
      return { etag, text: async () => text };
    },
    async put(key, value, putOptions = {}) {
      puts.push({ key, value, options: putOptions });
      if (conflictOnce) {
        conflictOnce = false;
        return null;
      }
      const condition = putOptions.onlyIf ?? {};
      if (condition.etagMatches && condition.etagMatches !== etag) return null;
      if (condition.etagDoesNotMatch === "*" && text !== null) return null;
      version += 1;
      text = value;
      etag = `etag-${version}`;
      return { key, etag };
    },
  };
}

function createHistoryFetcher(resolver) {
  const urls = [];
  const fetcher = async (input) => {
    const url = new URL(String(input));
    urls.push(url);
    const result = await resolver(url, urls.length);
    if (result instanceof Response) return result;
    return Response.json(result);
  };
  fetcher.urls = urls;
  return fetcher;
}

const silentHistoryLogger = { info() {}, warn() {}, error() {} };

test("exchange history file validates, converts all currencies, and keeps a rolling ten-year window", async () => {
  const {
    createEmptyHistoryFile,
    mergeRateRows,
    parseHistoryFile,
    pruneHistoryFile,
  } = await load("app/api/exchange-rates/history-file.ts");
  const empty = createEmptyHistoryFile("2016-08-03");
  const merged = mergeRateRows(
    empty,
    sampleFrankfurterRows(),
    "2026-08-02",
    new Date("2026-08-03T00:00:00+08:00"),
  );

  assert.equal(merged.dates["2026-07-31"].rates.USD, 7.2);
  assert.deepEqual(Object.keys(merged.dates["2026-07-31"].rates).sort(), [...supportedExchangeRateCurrencies].sort());
  assert.equal(merged.checkedThrough, "2026-08-02");
  assert.deepEqual(parseHistoryFile(JSON.stringify(merged)), merged);
  assert.throws(() => parseHistoryFile('{"version":2}'), /不支持的汇率历史文件版本/);
  assert.throws(() => parseHistoryFile('{"version":1,"base":"CNY"}'), /汇率历史文件格式无效/);

  const withOldDate = {
    ...merged,
    dates: { "2016-08-02": merged.dates["2026-07-31"], ...merged.dates },
  };
  assert.deepEqual(Object.keys(pruneHistoryFile(withOldDate, "2016-08-03")), ["version", "base", "checkedThrough", "updatedAt", "dates"]);
  assert.equal(pruneHistoryFile(withOldDate, "2016-08-03").dates["2016-08-02"], undefined);
});

test("exchange history file windows are continuous, non-overlapping, and at most one year", async () => {
  const { historyWindows } = await load("app/api/exchange-rates/history-file.ts");
  const windows = historyWindows("2016-08-03", "2026-08-02");

  assert.equal(windows[0].from, "2016-08-03");
  assert.equal(windows.at(-1).to, "2026-08-02");
  for (let index = 0; index < windows.length; index += 1) {
    const window = windows[index];
    const durationDays = (Date.parse(window.to) - Date.parse(window.from)) / 86400000;
    assert.ok(durationDays <= 365, `${window.from}..${window.to} exceeds one year`);
    if (index > 0) {
      const previous = windows[index - 1];
      assert.equal(Date.parse(window.from) - Date.parse(previous.to), 86400000);
    }
  }
});

test("exchange history sync backfills ten years, writes once, imports D1, and logs completion", async () => {
  const { createExchangeRateHistorySync } = await load("app/api/exchange-rates/history-sync.ts");
  const db = createExchangeRateHistoryDb();
  const bucket = createMemoryRatesBucket();
  const fetcher = createHistoryFetcher((url) => url.searchParams.get("to") === "2026-08-03"
    ? [...sampleFrankfurterRows("2026-07-31"), ...sampleFrankfurterRows("2026-08-01")]
    : []);
  const logs = [];
  const logger = {
    info(message, details) { logs.push({ level: "info", message, ...details }); },
    warn(message, details) { logs.push({ level: "warn", message, ...details }); },
    error(message, details) { logs.push({ level: "error", message, ...details }); },
  };

  const summary = await createExchangeRateHistorySync({
    db,
    bucket,
    fetch: fetcher,
    now: () => new Date("2026-08-03T12:00:00.000Z"),
    logger,
  }).sync();

  assert.equal(fetcher.urls.length, 20);
  assert.equal(fetcher.urls.filter((url) => url.searchParams.get("base") === "USD").length, 10);
  assert.equal(fetcher.urls.filter((url) => url.searchParams.get("base") === "CNY").length, 10);
  assert.equal(bucket.puts.length, 1);
  assert.equal(summary.currencies, 9);
  assert.equal(summary.inserted, 18);
  assert.equal(summary.checkedThrough, "2026-08-03");
  assert.ok(logs.some((entry) => entry.message === "[exchange-rate-history]" && entry.event === "sync_complete"));
});

test("exchange history sync advances an existing file after an empty incremental response", async () => {
  const { createExchangeRateHistorySync } = await load("app/api/exchange-rates/history-sync.ts");
  const existing = {
    version: 1,
    base: "CNY",
    checkedThrough: "2026-08-01",
    updatedAt: "2026-08-01T16:00:00.000Z",
    dates: {},
  };
  const bucket = createMemoryRatesBucket(JSON.stringify(existing));
  const fetcher = createHistoryFetcher(() => []);

  await createExchangeRateHistorySync({
    db: createExchangeRateHistoryDb(),
    bucket,
    fetch: fetcher,
    now: () => new Date("2026-08-02T12:00:00.000Z"),
    logger: silentHistoryLogger,
  }).sync();

  assert.equal(fetcher.urls.length, 2);
  assert.equal(fetcher.urls[0].searchParams.get("from"), "2026-08-02");
  assert.equal(JSON.parse(bucket.currentText()).checkedThrough, "2026-08-02");
});

test("exchange history sync never fetches earlier than the rolling ten-year cutoff", async () => {
  const { createExchangeRateHistorySync } = await load("app/api/exchange-rates/history-sync.ts");
  const existing = {
    version: 1,
    base: "CNY",
    checkedThrough: "2010-01-01",
    updatedAt: "2010-01-01T00:00:00.000Z",
    dates: {},
  };
  const fetcher = createHistoryFetcher(() => []);

  await createExchangeRateHistorySync({
    db: createExchangeRateHistoryDb(),
    bucket: createMemoryRatesBucket(JSON.stringify(existing)),
    fetch: fetcher,
    now: () => new Date("2026-08-03T12:00:00.000Z"),
    logger: silentHistoryLogger,
  }).sync();

  assert.equal(fetcher.urls[0].searchParams.get("from"), "2016-08-03");
  assert.equal(fetcher.urls.length, 20);
});

test("exchange history sync never overwrites R2 after a failed window or damaged file", async () => {
  const { createExchangeRateHistorySync } = await load("app/api/exchange-rates/history-sync.ts");
  const failedBucket = createMemoryRatesBucket();
  const failedFetcher = createHistoryFetcher((url, call) => call === 2
    ? new Response("upstream failed", { status: 503 })
    : []);
  await assert.rejects(
    createExchangeRateHistorySync({
      db: createExchangeRateHistoryDb(),
      bucket: failedBucket,
      fetch: failedFetcher,
      now: () => new Date("2026-08-03T12:00:00.000Z"),
      logger: silentHistoryLogger,
    }).sync(),
    /汇率历史请求失败/,
  );
  assert.equal(failedBucket.puts.length, 0);

  const damagedBucket = createMemoryRatesBucket("{damaged");
  await assert.rejects(
    createExchangeRateHistorySync({
      db: createExchangeRateHistoryDb(),
      bucket: damagedBucket,
      fetch: createHistoryFetcher(() => []),
      now: () => new Date("2026-08-03T12:00:00.000Z"),
      logger: silentHistoryLogger,
    }).sync(),
    /JSON 解析失败/,
  );
  assert.equal(damagedBucket.puts.length, 0);
});

test("exchange history sync rereads and succeeds after one R2 ETag conflict", async () => {
  const { createExchangeRateHistorySync } = await load("app/api/exchange-rates/history-sync.ts");
  const existing = {
    version: 1,
    base: "CNY",
    checkedThrough: "2026-08-01",
    updatedAt: "2026-08-01T16:00:00.000Z",
    dates: {},
  };
  const bucket = createMemoryRatesBucket(JSON.stringify(existing), { conflictOnce: true });

  const summary = await createExchangeRateHistorySync({
    db: createExchangeRateHistoryDb(),
    bucket,
    fetch: createHistoryFetcher(() => sampleFrankfurterRows("2026-08-02")),
    now: () => new Date("2026-08-02T12:00:00.000Z"),
    logger: silentHistoryLogger,
  }).sync();

  assert.equal(bucket.gets.length, 2);
  assert.equal(bucket.puts.length, 2);
  assert.equal(summary.inserted, 9);
  assert.ok(JSON.parse(bucket.currentText()).dates["2026-08-02"]);
});

test("latest exchange rates require all supported currencies from one date", async () => {
  const { readLatestExchangeRates } = await load("db/exchange-rate-history.ts");
  const db = createExchangeRateHistoryDb();
  supportedExchangeRateCurrencies.slice(0, -1).forEach((currency, index) => {
    db.exchangeRates.set(currency, { currency, cny_rate: index + 1, rate_date: "2026-07-31" });
  });
  assert.equal(await readLatestExchangeRates(db), null);

  db.exchangeRates.set("CHF", { currency: "CHF", cny_rate: 9, rate_date: "2026-07-30" });
  assert.equal(await readLatestExchangeRates(db), null);

  db.exchangeRates.get("CHF").rate_date = "2026-07-31";
  assert.deepEqual(await readLatestExchangeRates(db), {
    rates: Object.fromEntries(supportedExchangeRateCurrencies.map((currency, index) => [currency, index + 1])),
    date: "2026-07-31",
  });
});

test("latest exchange rates refresh from the newest complete historical date", async () => {
  const { importExchangeRateHistory, refreshLatestExchangeRates } = await load("db/exchange-rate-history.ts");
  const db = createExchangeRateHistoryDb();
  const complete = supportedExchangeRateCurrencies.map((currency, index) => sampleExchangeRateHistoryEntry(index, {
    currency,
    cnyRate: index + 1,
    rateDate: "2026-07-31",
  }));
  const incompleteNewer = complete.slice(0, -1).map((entry) => ({ ...entry, rateDate: "2026-08-01", cnyRate: entry.cnyRate + 10 }));
  await importExchangeRateHistory(db, [...complete, ...incompleteNewer]);

  await refreshLatestExchangeRates(db);

  assert.equal(db.exchangeRates.size, supportedExchangeRateCurrencies.length);
  assert.ok([...db.exchangeRates.values()].every((row) => row.rate_date === "2026-07-31"));
  assert.equal(db.exchangeRates.get("USD").cny_rate, 1);
});

function sampleMarketReturn(overrides = {}) {
  return {
    category: "stock", code: "QQQ", lookbackDays: 1095, calculationDate: "2026-08-03",
    annualRate: 8, periodReturn: 25.97, requestedDays: 1095, actualDays: 1096,
    historyLimited: false, startDate: "2023-08-03", endDate: "2026-08-03",
    source: "test", calculatedAt: "2026-08-03T04:10:00+08:00", ...overrides,
  };
}

test("market return cache uses the Shanghai date and upserts one daily row", async () => {
  const { findLatestMarketReturn, findMarketReturn, saveMarketReturn, shanghaiDate } = await load("db/market-returns.ts");
  assert.equal(shanghaiDate(new Date("2026-08-02T20:10:00Z")), "2026-08-03");
  const db = createMarketReturnDb();
  await saveMarketReturn(db, sampleMarketReturn({ annualRate: 8 }));
  await saveMarketReturn(db, sampleMarketReturn({ annualRate: 9 }));
  assert.equal(db.rows.size, 1);
  assert.equal([...db.rows.values()][0].annual_rate, 9);
  assert.equal((await findMarketReturn(db, "stock", "QQQ", 1095, "2026-08-03")).annualRate, 9);
  assert.equal((await findLatestMarketReturn(db, "stock", "QQQ", 1095)).calculationDate, "2026-08-03");
});

function sampleCalculation(overrides = {}) {
  return {
    annualRate: 8, periodReturn: 25.97, requestedDays: 1095, actualDays: 1096,
    historyLimited: false, startDate: "2023-08-03", endDate: "2026-08-03",
    source: "test", ...overrides,
  };
}

function captureLogger() {
  const events = [];
  return {
    events,
    logger: {
      info: (...args) => events.push(["info", ...args]),
      warn: (...args) => events.push(["warn", ...args]),
      error: (...args) => events.push(["error", ...args]),
    },
  };
}

test("market return read-through logs cache hits and fills daily misses", async () => {
  const { saveMarketReturn } = await load("db/market-returns.ts");
  const { createMarketReturnService } = await load("app/api/market/market-return-service.ts");
  const db = createMarketReturnDb();
  await saveMarketReturn(db, sampleMarketReturn());
  let calculatorCalls = 0;
  const { events, logger } = captureLogger();
  const service = createMarketReturnService({
    db, now: () => new Date("2026-08-03T12:00:00+08:00"), logger,
    calculate: async () => { calculatorCalls += 1; return sampleCalculation(); },
  });

  const hit = await service.get("stock", "QQQ", 1095);
  assert.equal(hit.annualRate, 8);
  assert.equal(calculatorCalls, 0);
  assert.ok(events.some((event) => event[2]?.event === "cache_hit"));

  const miss = await service.get("fund", "021000", 1095);
  assert.equal(miss.code, "021000");
  assert.equal(calculatorCalls, 1);
  assert.equal(db.rows.size, 2);
  assert.ok(events.some((event) => event[2]?.event === "cache_miss"));
  assert.ok(events.some((event) => event[2]?.event === "calculate_success"));
});

test("market quote cache reuses a fresh quote and shares in-flight requests", async () => {
  const { saveMarketReturn } = await load("db/market-returns.ts");
  const { createMarketReturnService } = await load("app/api/market/market-return-service.ts");
  const db = createMarketReturnDb();
  await saveMarketReturn(db, sampleMarketReturn({ category: "stock", code: "600519" }));
  let quoteCalls = 0;
  let releaseQuote;
  const service = createMarketReturnService({
    db,
    now: () => new Date("2026-08-03T12:00:00+08:00"),
    calculate: async () => sampleCalculation(),
    quote: async () => {
      quoteCalls += 1;
      await new Promise((resolve) => { releaseQuote = resolve; });
      return { currentPrice: 1500, priceCurrency: "CNY", priceDate: "2026-08-03" };
    },
  });
  const first = service.get("stock", "600519", 1095);
  const second = service.get("stock", "600519", 1095);
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(quoteCalls, 1);
  releaseQuote();
  assert.equal((await first).currentPrice, 1500);
  assert.equal((await second).currentPrice, 1500);
  assert.equal((await service.get("stock", "600519", 1095)).currentPrice, 1500);
  assert.equal(quoteCalls, 1);
});

test("legacy stock returns are recalculated instead of served from cache", async () => {
  const { saveMarketReturn } = await load("db/market-returns.ts");
  const { createMarketReturnService } = await load("app/api/market/market-return-service.ts");
  const db = createMarketReturnDb();
  await saveMarketReturn(db, sampleMarketReturn({
    annualRate: -7,
    source: "腾讯证券美股历史行情（人民币汇率调整）",
  }));
  let calculatorCalls = 0;
  const service = createMarketReturnService({
    db,
    now: () => new Date("2026-08-03T12:00:00+08:00"),
    calculate: async () => {
      calculatorCalls += 1;
      return sampleCalculation({ annualRate: 25.5, source: "Yahoo Finance 复权收盘价（人民币汇率调整）" });
    },
  });

  const result = await service.get("stock", "GOOG", 1095);
  assert.equal(calculatorCalls, 1);
  assert.equal(result.annualRate, 25.5);
  assert.match(result.source, /复权收盘价/);

  await saveMarketReturn(db, sampleMarketReturn({
    code: "600519",
    annualRate: 3,
    source: "腾讯证券历史复权行情",
  }));
  const domestic = await service.get("stock", "600519", 1095);
  assert.equal(calculatorCalls, 2);
  assert.equal(domestic.annualRate, 25.5);
});

test("market return read-through falls back to stale cache and logs the reason", async () => {
  const { saveMarketReturn } = await load("db/market-returns.ts");
  const { createMarketReturnService } = await load("app/api/market/market-return-service.ts");
  const db = createMarketReturnDb();
  await saveMarketReturn(db, sampleMarketReturn({ calculationDate: "2026-08-02" }));
  const { events, logger } = captureLogger();
  const service = createMarketReturnService({
    db, now: () => new Date("2026-08-03T12:00:00+08:00"), logger,
    calculate: async () => { throw new Error("upstream down"); },
  });
  const result = await service.get("stock", "QQQ", 1095);
  assert.equal(result.stale, true);
  assert.equal(result.calculationDate, "2026-08-02");
  assert.ok(events.some((event) => event[2]?.event === "stale_fallback" && event[2]?.error === "upstream down"));
});

test("market return prewarm deduplicates assets, covers four lookbacks, and isolates failures", async () => {
  const { createMarketReturnService, LOOKBACK_DAYS } = await load("app/api/market/market-return-service.ts");
  const db = createMarketReturnDb();
  db.assets.push(
    { user_id: 1, category: "stock", code: " qqq " },
    { user_id: 2, category: "stock", code: "QQQ" },
    { user_id: 1, category: "fund", code: "FAIL" },
    { user_id: 1, category: "deposit", code: null },
  );
  const calls = [];
  const { events, logger } = captureLogger();
  const service = createMarketReturnService({
    db, now: () => new Date("2026-08-03T12:00:00+08:00"), logger, concurrency: 2,
    calculate: async (category, code, lookbackDays) => {
      calls.push({ category, code, lookbackDays });
      if (code === "FAIL") throw new Error("bad symbol");
      return sampleCalculation({ requestedDays: lookbackDays });
    },
  });
  const summary = await service.prewarmAll();
  assert.deepEqual(LOOKBACK_DAYS, [365, 1095, 1825, 3650]);
  assert.deepEqual(calls.filter((call) => call.code === "QQQ").map((call) => call.lookbackDays).sort((a, b) => a - b), LOOKBACK_DAYS);
  assert.equal(summary.succeeded, 4);
  assert.equal(summary.failed, 4);
  assert.ok(events.some((event) => event[2]?.event === "prewarm_start"));
  assert.ok(events.some((event) => event[2]?.event === "prewarm_complete" && event[2]?.failed === 4));
});

test("Worker lifecycle starts exchange then market once without blocking and forwards Cron schedules", async () => {
  const { createWorkerLifecycle } = await load("worker/lifecycle.ts");
  let startupCalls = 0;
  let releaseStartup;
  const startupPending = new Promise((resolve) => { releaseStartup = resolve; });
  const waits = [];
  const order = [];
  const context = { waitUntil: (promise) => waits.push(promise) };
  const lifecycle = createWorkerLifecycle({
    handleRequest: async () => new Response("ready"),
    startup: async () => {
      startupCalls += 1;
      order.push("exchange");
      await startupPending;
      order.push("market");
    },
    scheduled: async (controller) => {
      if (controller.cron === "0 16 * * *") order.push("exchange-cron");
      if (controller.cron === "10 20 * * *") order.push("market-cron");
      if (controller.cron === "58 3 * * *") order.push("snapshot-cron");
    },
    logger: captureLogger().logger,
  });

  const first = await lifecycle.fetch(new Request("http://local/"), {}, context);
  assert.equal(await first.text(), "ready");
  assert.equal(startupCalls, 1);
  assert.deepEqual(order, ["exchange"]);
  assert.equal(waits.length, 1);
  await lifecycle.fetch(new Request("http://local/again"), {}, context);
  assert.equal(startupCalls, 1);
  releaseStartup();
  await waits[0];
  assert.deepEqual(order, ["exchange", "market"]);

  lifecycle.scheduled({ cron: "0 16 * * *" }, {}, context);
  await waits.at(-1);
  lifecycle.scheduled({ cron: "10 20 * * *" }, {}, context);
  await waits.at(-1);
  lifecycle.scheduled({ cron: "58 3 * * *" }, {}, context);
  await waits.at(-1);
  assert.deepEqual(order, ["exchange", "market", "exchange-cron", "market-cron", "snapshot-cron"]);
});

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

test("11:58 daily job refreshes quantity-based values and records each user's snapshot", async () => {
  const { createDailyAssetSnapshotService } = await load("app/api/history/daily-snapshot.ts");
  const assets = [
    { id: 1, user_id: 1, category: "stock", code: "600519", amount: 10000, quantity: 2.5, currency: "CNY" },
    { id: 2, user_id: 1, category: "stock", code: "600519", amount: 20000, quantity: 1, currency: "CNY" },
    { id: 3, user_id: 2, category: "deposit", code: null, amount: 50000, quantity: null, currency: "CNY" },
  ];
  const statements = [];
  const statement = (sql, values = []) => ({
    sql, values,
    bind(...nextValues) { return statement(sql, nextValues); },
    async all() { return { results: assets }; },
  });
  const db = {
    prepare(sql) { return statement(sql); },
    async batch(batch) {
      for (const item of batch) {
        statements.push(item);
        const [amount, currency, id, userId] = item.values;
        const asset = assets.find((row) => row.id === id && row.user_id === userId);
        asset.amount = amount;
        asset.currency = currency;
      }
    },
  };
  const quotes = [];
  const snapshots = [];
  const run = createDailyAssetSnapshotService({
    db,
    quote: async (...args) => {
      quotes.push(args);
      return { currentPrice: 100, priceCurrency: "CNY", priceDate: "2026-08-03 11:58" };
    },
    snapshot: async (...args) => {
      snapshots.push(args);
      return { snapshot_date: "2026-08-03" };
    },
    now: () => new Date("2026-08-03T03:58:00Z"),
  });
  const summary = await run();
  assert.equal(quotes.length, 1);
  assert.equal(statements.length, 2);
  assert.equal(assets[0].amount, 25000);
  assert.equal(assets[1].amount, 10000);
  assert.deepEqual(snapshots.map((call) => call.slice(1, 3)), [[1, "scheduled_daily"], [2, "scheduled_daily"]]);
  assert.deepEqual(summary, { users: 2, updatedAssets: 2, recordedSnapshots: 2, errors: [] });
});

test("opening the dashboard always refreshes the signed-in user's daily snapshot", async () => {
  const { createDailyRefreshHandler } = await load("app/api/history/daily-refresh.ts");
  const db = {};
  let refreshCalls = 0;
  const handler = createDailyRefreshHandler({
    getAuthenticatedUser: async () => ({ id: 7 }),
    getAssetsDb: async () => db,
    refreshUser: async (_db, userId) => {
      assert.equal(userId, 7);
      refreshCalls += 1;
      return { recordedSnapshots: 1 };
    },
  });

  const first = await handler(new Request("http://local/api/history", { method: "POST" }));
  assert.deepEqual(await first.json(), { refreshed: true, result: { recordedSnapshots: 1 } });
  const second = await handler(new Request("http://local/api/history", { method: "POST" }));
  assert.deepEqual(await second.json(), { refreshed: true, result: { recordedSnapshots: 1 } });
  assert.equal(refreshCalls, 2);
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

test("fund investment strategy can be changed after creation", async () => {
  const { createAssetsHandlers } = await load("app/api/assets/handlers.ts");
  const db = createAssetDb();
  const handlers = createAssetsHandlers({ getAuthenticatedUser: async () => ({ id: 1 }), getAssetsDb: async () => db });
  const created = await handlers.POST(new Request("http://local/api/assets", {
    method: "POST",
    body: JSON.stringify({ name: "指数基金", category: "fund", code: "510300", amount: 1000, quantity: 250.5, currency: "CNY", annualRate: 5 }),
  }));
  const asset = (await created.json()).asset;
  const changed = await handlers.PATCH(new Request("http://local/api/assets", {
    method: "PATCH",
    body: JSON.stringify({ id: asset.id, investmentStrategy: "monthly", investmentAmount: 500 }),
  }));
  assert.equal(changed.status, 200);
  assert.equal((await changed.json()).investmentStrategy, "monthly");
});

test("stocks cannot be created with an investment strategy", async () => {
  const { createAssetsHandlers } = await load("app/api/assets/handlers.ts");
  const handlers = createAssetsHandlers({ getAuthenticatedUser: async () => ({ id: 1 }), getAssetsDb: async () => createAssetDb() });
  const response = await handlers.POST(new Request("http://local/api/assets", {
    method: "POST",
    body: JSON.stringify({
      name: "QQQ", category: "stock", code: "QQQ", amount: 700, quantity: 1,
      currency: "USD", annualRate: 5, investmentStrategy: "monthly", investmentAmount: 100,
    }),
  }));
  assert.equal(response.status, 400);
  assert.match((await response.json()).error, /基金定投策略/);
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

function historicalRateResponse(url, rate = 7) {
  const date = new URL(url).searchParams.get("to");
  return Response.json([{ date, base: "USD", quote: "CNY", rate }]);
}

function yahooChartResponse(points) {
  return Response.json({
    chart: {
      result: [{
        timestamp: points.map(([date]) => Date.parse(`${date}T00:00:00Z`) / 1000),
        indicators: { adjclose: [{ adjclose: points.map(([, adjustedClose]) => adjustedClose) }] },
      }],
      error: null,
    },
  });
}

test("historical USD/CNY rate uses the latest valid rate not after the market date", async () => {
  const { fetchHistoricalUsdCnyRate } = await load("app/api/market/historical-rates.ts");
  let requestedUrl = "";
  const result = await fetchHistoricalUsdCnyRate(async (url) => {
    requestedUrl = String(url);
    return Response.json([
      { date: "2025-07-29", base: "USD", quote: "CNY", rate: 7.1 },
      { date: "2025-07-31", base: "USD", quote: "CNY", rate: 7.2 },
      { date: "2025-08-01", base: "USD", quote: "CNY", rate: 7.3 },
    ]);
  }, "2025-07-31");
  assert.deepEqual(result, { date: "2025-07-31", rate: 7.2 });
  assert.ok(requestedUrl.includes("base=USD"));
  assert.ok(requestedUrl.includes("quotes=CNY"));
  assert.ok(requestedUrl.includes("from=2025-07-24"));
  assert.ok(requestedUrl.includes("to=2025-07-31"));
});

test("historical USD/CNY prefers D1 and backfills D1 after an upstream fallback", async () => {
  const { fetchHistoricalUsdCnyRate } = await load("app/api/market/historical-rates.ts");
  const { importExchangeRateHistory } = await load("db/exchange-rate-history.ts");
  const db = createExchangeRateHistoryDb();
  await importExchangeRateHistory(db, [sampleExchangeRateHistoryEntry(0, {
    currency: "USD",
    cnyRate: 7.2,
    rateDate: "2025-07-31",
  })]);
  let upstreamCalls = 0;

  assert.deepEqual(await fetchHistoricalUsdCnyRate({
    db,
    fetcher: async () => {
      upstreamCalls += 1;
      return Response.json([]);
    },
  }, "2025-07-31"), { date: "2025-07-31", rate: 7.2 });
  assert.equal(upstreamCalls, 0);

  const emptyDb = createExchangeRateHistoryDb();
  assert.deepEqual(await fetchHistoricalUsdCnyRate({
    db: emptyDb,
    fetcher: async (url) => {
      upstreamCalls += 1;
      return historicalRateResponse(url, 7.1);
    },
  }, "2025-08-01"), { date: "2025-08-01", rate: 7.1 });
  assert.equal(upstreamCalls, 1);
  assert.equal(emptyDb.rows.get("USD:2025-08-01").cny_rate, 7.1);
});

test("historical USD/CNY rate rejects an empty rate window", async () => {
  const { fetchHistoricalUsdCnyRate } = await load("app/api/market/historical-rates.ts");
  await assert.rejects(
    fetchHistoricalUsdCnyRate(async () => Response.json([]), "2025-07-31"),
    /没有找到对应日期的美元人民币历史汇率/,
  );
});

test("historical USD/CNY rate reports upstream network failures clearly", async () => {
  const { fetchHistoricalUsdCnyRate } = await load("app/api/market/historical-rates.ts");
  await assert.rejects(
    fetchHistoricalUsdCnyRate(async () => { throw new TypeError("fetch failed"); }, "2025-07-31"),
    /美元人民币历史汇率服务暂不可用/,
  );
});

test("historical USD/CNY rate retries one transient network failure", async () => {
  const { fetchHistoricalUsdCnyRate } = await load("app/api/market/historical-rates.ts");
  let attempts = 0;
  const result = await fetchHistoricalUsdCnyRate(async (url) => {
    attempts += 1;
    if (attempts === 1) throw new TypeError("fetch failed");
    return historicalRateResponse(url, 7.2);
  }, "2025-07-31");
  assert.equal(attempts, 2);
  assert.deepEqual(result, { date: "2025-07-31", rate: 7.2 });
});

test("historical USD/CNY rate does not retry a timeout", async () => {
  const { fetchHistoricalUsdCnyRate } = await load("app/api/market/historical-rates.ts");
  let attempts = 0;
  await assert.rejects(
    fetchHistoricalUsdCnyRate(async () => {
      attempts += 1;
      throw new DOMException("timeout", "TimeoutError");
    }, "2025-07-31"),
    /美元人民币历史汇率服务暂不可用/,
  );
  assert.equal(attempts, 1);
});

test("market API authenticates, targets the requested lookback, and caches", async () => {
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
    const params = new URL(url).searchParams;
    if (params.has("startDate")) {
      const date = params.get("endDate");
      return Response.json({ Data: { LSJZList: [{ FSRQ: date, DWJZ: "1", LJJZ: "1" }] }, TotalCount: 1, PageSize: 20, PageIndex: 1 });
    }
    const pageIndex = Number(params.get("pageIndex"));
    return Response.json(fundPage(pageIndex));
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch, maxConcurrent: 3 });
  const request = new Request("http://local/api/market?code=000001&category=fund&days=3650");
  const first = await handler(request);
  assert.equal(first.status, 200);
  const payload = await first.json();
  assert.ok(Date.parse(payload.endDate) - Date.parse(payload.startDate) >= 3400 * 86400000);
  assert.equal(calls, 2);
  assert.ok(maximumActive <= 3);

  const beforeCache = calls;
  assert.equal((await handler(request)).status, 200);
  assert.equal(calls, beforeCache);

  const unauthorized = createMarketHandler({ authenticate: async () => null, fetch });
  assert.equal((await unauthorized(request)).status, 401);
});

test("batch market API reads the signed-in user's cached returns and validates lookbacks", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const calls = [];
  const service = {
    async get(category, code, lookbackDays) {
      calls.push({ mode: "single", category, code, lookbackDays });
      return sampleMarketReturn({ category, code, lookbackDays });
    },
    async getForUser(userId, lookbackDays) {
      calls.push({ mode: "batch", userId, lookbackDays });
      return {
        results: [sampleMarketReturn({ category: "fund", code: "021000", lookbackDays }), sampleMarketReturn({ code: "QQQ", lookbackDays })],
        errors: [],
      };
    },
  };
  const handler = createMarketHandler({
    authenticate: async () => ({ id: 7 }),
    getService: async () => service,
  });

  const batch = await handler(new Request("http://local/api/market?days=1095"));
  assert.equal(batch.status, 200);
  assert.deepEqual((await batch.json()).results.map((item) => item.code), ["021000", "QQQ"]);
  assert.deepEqual(calls[0], { mode: "batch", userId: 7, lookbackDays: 1095 });

  const single = await handler(new Request("http://local/api/market?code=qqq&category=stock&days=365"));
  assert.equal(single.status, 200);
  assert.deepEqual(calls[1], { mode: "single", category: "stock", code: "QQQ", lookbackDays: 365 });
  assert.equal((await handler(new Request("http://local/api/market?days=30"))).status, 400);
});

test("fund returns request only the latest page and the target-date window", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const urls = [];
  const fetch = async (url) => {
    urls.push(String(url));
    const params = new URL(url).searchParams;
    if (params.has("startDate")) {
      return Response.json({ Data: { LSJZList: [{ FSRQ: "2025-08-03", DWJZ: "1.5", LJJZ: "1.5" }] }, TotalCount: 1, PageSize: 20, PageIndex: 1 });
    }
    return Response.json({ Data: { LSJZList: [{ FSRQ: "2026-08-03", DWJZ: "2", LJJZ: "2" }] }, TotalCount: 2000, PageSize: 20, PageIndex: 1 });
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=021000&category=fund&days=365"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.startDate, "2025-08-03");
  assert.equal(payload.endDate, "2026-08-03");
  assert.equal(payload.requestedDays, 365);
  assert.equal(payload.actualDays, 365);
  assert.equal(payload.historyLimited, false);
  assert.equal(urls.length, 2);
  assert.ok(urls[1].includes("startDate=2025-07-04"));
  assert.ok(urls[1].includes("endDate=2025-08-03"));
});

test("fund returns use the earliest net value when the target date predates the fund", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const urls = [];
  const fetch = async (url) => {
    urls.push(String(url));
    const params = new URL(url).searchParams;
    if (params.has("startDate")) return Response.json({ Data: { LSJZList: [] }, TotalCount: 0, PageSize: 20, PageIndex: 1 });
    if (params.get("pageIndex") === "3") {
      return Response.json({ Data: { LSJZList: [{ FSRQ: "2024-03-20", DWJZ: "1.01", LJJZ: "1.01" }, { FSRQ: "2024-03-19", DWJZ: "1", LJJZ: "1" }] }, TotalCount: 42, PageSize: 20, PageIndex: 3 });
    }
    return Response.json({ Data: { LSJZList: [{ FSRQ: "2026-08-03", DWJZ: "2", LJJZ: "2" }] }, TotalCount: 42, PageSize: 20, PageIndex: 1 });
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=021000&category=fund&days=3650"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.startDate, "2024-03-19");
  assert.equal(payload.endDate, "2026-08-03");
  assert.equal(payload.requestedDays, 3650);
  assert.equal(payload.actualDays, 867);
  assert.equal(payload.historyLimited, true);
  assert.equal(urls.length, 3);
  assert.ok(urls.some((url) => url.includes("startDate=")));
  assert.ok(urls.some((url) => url.includes("pageIndex=3")));
});

test("stock returns trim overfetched history to the requested lookback", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const urls = [];
  const fetch = async (url) => {
    urls.push(String(url));
    if (String(url).includes("frankfurter.dev")) return historicalRateResponse(url);
    if (String(url).includes("query1.finance.yahoo.com")) return yahooChartResponse([
      ["2025-07-30", 99], ["2025-08-03", 100], ["2026-08-03", 140],
    ]);
    throw new Error(`Unexpected URL: ${url}`);
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=QQQ&category=stock&days=365"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.startDate, "2025-08-03");
  assert.equal(payload.endDate, "2026-08-03");
  assert.equal(payload.requestedDays, 365);
  assert.equal(payload.actualDays, 365);
  assert.equal(payload.historyLimited, false);
  assert.equal(urls.length, 3);
  assert.ok(urls[0].includes("query1.finance.yahoo.com/v8/finance/chart/QQQ"));
});

test("US stock returns are converted to CNY with historical exchange rates", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  let activeRates = 0;
  let maximumActiveRates = 0;
  const fetch = async (url) => {
    const value = String(url);
    if (value.includes("frankfurter.dev")) {
      activeRates += 1;
      maximumActiveRates = Math.max(maximumActiveRates, activeRates);
      await new Promise((resolve) => setTimeout(resolve, 1));
      activeRates -= 1;
      const date = new URL(value).searchParams.get("to");
      return historicalRateResponse(value, date === "2025-08-03" ? 7.2 : 6.6);
    }
    if (value.includes("query1.finance.yahoo.com")) return yahooChartResponse([["2025-08-03", 100], ["2026-08-03", 120]]);
    throw new Error(`Unexpected URL: ${url}`);
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=QQQ&category=stock&days=365"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.ok(Math.abs(payload.periodReturn - 10) < 1e-9);
  assert.ok(Math.abs(payload.annualRate - 10) < 1e-9);
  assert.match(payload.source, /人民币汇率调整/);
  assert.equal(maximumActiveRates, 1);
});

test("US stock calculator uses an injected historical D1 rate reader", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const rateDates = [];
  const fetch = async (url) => {
    const value = String(url);
    if (value.includes("frankfurter.dev")) throw new Error("Frankfurter must not be called");
    if (value.includes("query1.finance.yahoo.com")) return yahooChartResponse([["2025-08-03", 100], ["2026-08-03", 120]]);
    throw new Error(`Unexpected URL: ${url}`);
  };
  const calculator = createMarketCalculator({
    fetch,
    historicalRate: async (date) => {
      rateDates.push(date);
      return { date, rate: date === "2025-08-03" ? 7.2 : 6.6 };
    },
  });

  const result = await calculator.calculate("stock", "QQQ", 365);
  assert.ok(Math.abs(result.periodReturn - 10) < 1e-9);
  assert.deepEqual(rateDates, ["2025-08-03", "2026-08-03"]);
});

test("US stock return fails instead of falling back to USD when historical rates are missing", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const fetch = async (url) => {
    const value = String(url);
    if (value.includes("frankfurter.dev")) return Response.json([]);
    if (value.includes("query1.finance.yahoo.com")) return yahooChartResponse([["2025-08-03", 100], ["2026-08-03", 120]]);
    throw new Error(`Unexpected URL: ${url}`);
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=QQQ&category=stock&days=365"));
  assert.equal(response.status, 502);
  assert.match((await response.json()).error, /历史汇率/);
});

test("stock returns use the earliest price when the target date predates listing", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const urls = [];
  const fetch = async (url) => {
    urls.push(String(url));
    if (String(url).includes("frankfurter.dev")) return historicalRateResponse(url);
    if (String(url).includes("query1.finance.yahoo.com")) return yahooChartResponse([["2024-03-19", 100], ["2026-08-03", 140]]);
    throw new Error(`Unexpected URL: ${url}`);
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=NEW&category=stock&days=3650"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.startDate, "2024-03-19");
  assert.equal(payload.endDate, "2026-08-03");
  assert.equal(payload.requestedDays, 3650);
  assert.equal(payload.actualDays, 867);
  assert.equal(payload.historyLimited, true);
  assert.equal(urls.length, 3);
});

test("money fund returns never report n-year history as limited", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const points = Array.from({ length: 7 }, (_, index) => ({
    FSRQ: `2026-08-0${7 - index}`,
    DWJZ: "0.5",
    LJJZ: "1",
  }));
  const handler = createMarketHandler({
    authenticate: async () => ({ id: 1 }),
    fetch: async () => Response.json({ Data: { LSJZList: points, SYType: "每万份收益" } }),
  });
  const response = await handler(new Request("http://local/api/market?code=000001&category=money&days=3650"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.requestedDays, 3650);
  assert.equal(payload.actualDays, 6);
  assert.equal(payload.historyLimited, false);
});

test("BNY USD liquidity fund uses the manager's USD 7-day yield instead of a domestic money-fund feed", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const calculator = createMarketCalculator({
    fetch: async (url) => {
      assert.match(String(url), /dreyfus\.com/);
      return new Response(`
        <div>7-Day Yield With Waiver</div>
        <div class="overview-stats__value" title="3.66">3.66% <small>As of&nbsp; 08/07/26</small></div>
      `);
    },
  });
  const result = await calculator.calculate("fund", "IE0004514828", 365);
  assert.deepEqual(result, {
    annualRate: 3.66, periodReturn: 3.66, requestedDays: 365, actualDays: 7,
    historyLimited: false, startDate: "2026-07-31", endDate: "2026-08-07",
    source: "BNY Mellon 官方 7 日年化收益率（美元）",
  });
  assert.deepEqual(await calculator.quote("fund", "IE0004514828"), {
    currentPrice: 1, priceCurrency: "USD", priceDate: "2026-08-07",
  });
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

test("market calculator reads live stock prices and latest fund unit values", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const calculator = createMarketCalculator({
    fetch: async (url) => {
      const href = String(url);
      if (href.includes("qt.gtimg.cn")) return new Response('v_sh600519="1~stock~600519~1358.98~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~20260803154141"');
      if (href.includes("fundCode=000001")) return Response.json({ Data: { LSJZList: [{ FSRQ: "2026-08-01", DWJZ: "1.2345", LJJZ: "4.2" }] } });
      throw new Error(`Unexpected quote URL: ${href}`);
    },
  });
  assert.deepEqual(await calculator.quote("stock", "600519"), {
    currentPrice: 1358.98, priceCurrency: "CNY", priceDate: "2026-08-03 15:41",
  });
  assert.deepEqual(await calculator.quote("fund", "000001"), {
    currentPrice: 1.2345, priceCurrency: "CNY", priceDate: "2026-08-01",
  });
});

test("separate market calculators can share the live quote cache", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const { createQuoteCache } = await load("app/api/market/quote-cache.ts");
  let quoteCalls = 0;
  const fetch = async (url) => {
    const href = String(url);
    if (href.includes("qt.gtimg.cn")) {
      quoteCalls += 1;
      return new Response('v_sh600519="1~stock~600519~1358.98~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~20260803154141"');
    }
    throw new Error(`Unexpected quote URL: ${href}`);
  };
  const cache = createQuoteCache();
  const first = createMarketCalculator({ fetch, quoteCache: cache });
  const second = createMarketCalculator({ fetch, quoteCache: cache });
  await Promise.all([first.quote("stock", "600519"), second.quote("stock", "600519")]);
  assert.equal(quoteCalls, 1);
});

test("QQQ live quote falls back from the exchange-suffixed symbol to the raw ticker", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const requested = [];
  const calculator = createMarketCalculator({
    fetch: async (url) => {
      const href = String(url);
      requested.push(href);
      if (href.includes("fqkline")) return Response.json({ data: { usQQQ: { qt: { usQQQ: ["", "", "QQQ.OQ"] } } } });
      if (href.endsWith("q=usQQQ.OQ")) return new Response('v_pv_none_match="1";');
      if (href.endsWith("q=usQQQ")) return new Response('v_usQQQ="200~QQQ~QQQ.OQ~700.07~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~0~2026-08-04 10:30:00";');
      throw new Error(`Unexpected URL: ${href}`);
    },
  });
  assert.deepEqual(await calculator.quote("stock", "QQQ"), {
    currentPrice: 700.07,
    priceCurrency: "USD",
    priceDate: "2026-08-04 10:30:00",
  });
  assert.ok(requested.some((url) => url.endsWith("q=usQQQ.OQ")));
  assert.ok(requested.some((url) => url.endsWith("q=usQQQ")));
});

test("QQQ ten-year history uses a target-date window", async () => {
  const { createMarketHandler } = await load("app/api/market/handler.ts");
  const urls = [];
  const fetch = async (url) => {
    urls.push(String(url));
    if (String(url).includes("frankfurter.dev")) return historicalRateResponse(url);
    if (String(url).includes("query1.finance.yahoo.com")) return yahooChartResponse([["2016-08-02", 100], ["2026-07-31", 687.99]]);
    throw new Error(`Unexpected URL: ${url}`);
  };
  const handler = createMarketHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/market?code=QQQ&category=stock&days=3650"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.startDate, "2016-08-02");
  assert.equal(payload.endDate, "2026-07-31");
  assert.equal(urls.length, 3);
  assert.ok(urls[0].includes("events=div%2Csplits"));
});

test("GOOG ten-year return uses split-adjusted close prices", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const calculator = createMarketCalculator({
    fetch: async (url) => {
      if (String(url).includes("query1.finance.yahoo.com")) {
        return yahooChartResponse([["2016-08-05", 38.769184], ["2026-08-03", 372.47]]);
      }
      throw new Error(`Unexpected URL: ${url}`);
    },
    historicalRate: async (date) => ({ date, rate: date === "2016-08-05" ? 6.6447 : 6.7491 }),
  });
  const result = await calculator.calculate("stock", "GOOG", 3650);
  assert.ok(result.annualRate > 25 && result.annualRate < 26);
  assert.ok(result.periodReturn > 800);
  assert.match(result.source, /复权收盘价/);
});

test("domestic stock returns reject unadjusted history instead of publishing a wrong rate", async () => {
  const { createMarketCalculator } = await load("app/api/market/calculator.ts");
  const calculator = createMarketCalculator({
    fetch: async () => Response.json({
      data: { sh600519: { day: [["2016-08-05", "190", "190"], ["2026-08-03", "1358", "1358"]] } },
    }),
  });
  await assert.rejects(calculator.calculate("stock", "600519", 3650), /没有找到这个股票代码的历史行情/);
});

const validRates = ["CNY", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"].map((quote) => ({
  date: "2026-08-03", base: "USD", quote, rate: quote === "CNY" ? 7.2 : 1,
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

test("fund forecasts include scheduled investments in the fund currency", async () => {
  const { calculatePortfolio, calculatePortfolioSeries } = await load("app/portfolio.ts");
  const asOf = new Date("2026-01-01T12:00:00Z");
  const base = { category: "fund", code: "510300", amount: 100000, currency: "CNY", annual_rate: 0, investment_amount: 10000 };
  const monthly = calculatePortfolio([{ ...base, investment_strategy: "monthly" }], { CNY: 1 }, 1, asOf);
  const daily = calculatePortfolio([{ ...base, investment_strategy: "daily" }], { CNY: 1 }, 1, asOf);
  assert.equal(monthly.forecast, 220000);
  assert.ok(daily.forecast > monthly.forecast);

  const usd = calculatePortfolio([{ ...base, code: "QQQ", currency: "USD", investment_strategy: "yearly" }], { USD: 7 }, 1, asOf);
  assert.equal(usd.forecast, 770000);

  const series = calculatePortfolioSeries([{ ...base, investment_strategy: "daily" }], { CNY: 1 }, 3, asOf);
  assert.deepEqual(
    series.map((item) => item.forecast),
    [0, 1, 2, 3].map((horizon) => calculatePortfolio([{ ...base, investment_strategy: "daily" }], { CNY: 1 }, horizon, asOf).forecast),
  );
});

test("portfolio forecasts add monthly savings for every forecast month", async () => {
  const { calculatePortfolio } = await load("app/portfolio.ts");
  const assets = [{ category: "deposit", amount: 100000, currency: "CNY", annual_rate: 0 }];
  const result = calculatePortfolio(assets, { CNY: 1 }, 3, new Date("2026-01-01T00:00:00Z"), 30000);
  assert.equal(result.total, 100000);
  assert.equal(result.savingsContribution, 1080000);
  assert.equal(result.forecast, 1180000);
  assert.equal(result.expectedGain, 1080000);
});

test("income settings are private to the signed-in user and stored in cents", async () => {
  const { createIncomeHandlers } = await load("app/api/income/handlers.ts");
  const rows = new Map();
  const db = {
    prepare(sql) {
      return {
        bind(...values) {
          return {
            async first() {
              if (sql.startsWith("SELECT")) return rows.get(values[0]) ?? null;
              if (sql.includes("INSERT INTO income_settings")) {
                const row = { monthly_salary: values[1], monthly_savings: values[2], updated_at: "2026-08-04 00:00:00" };
                rows.set(values[0], row);
                return row;
              }
              throw new Error(`Unexpected income query: ${sql}`);
            },
          };
        },
      };
    },
  };
  const handlers = createIncomeHandlers({
    getAuthenticatedUser: async () => ({ id: 7 }),
    getAssetsDb: async () => db,
  });
  const saved = await handlers.PUT(new Request("http://local/api/income", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ monthlySalary: 20000, monthlySavings: 8000.5 }),
  }));
  assert.equal(saved.status, 200);
  assert.deepEqual((await saved.json()).income, { monthly_salary: 2000000, monthly_savings: 800050, updated_at: "2026-08-04 00:00:00" });

  const read = await handlers.GET(new Request("http://local/api/income"));
  assert.deepEqual((await read.json()).income, { monthly_salary: 2000000, monthly_savings: 800050, updated_at: "2026-08-04 00:00:00" });
});

test("exchange-rate API prefers current D1 rates without an upstream request", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  let upstreamCalls = 0;
  let syncCalls = 0;
  const db = { name: "db" };
  const handler = createExchangeRatesHandler({
    authenticate: async () => ({ id: 1 }),
    fetch: async () => {
      upstreamCalls += 1;
      return Response.json(validRates);
    },
    getAssetsDb: async () => db,
    readLatestRates: async () => ({
      rates: Object.fromEntries(supportedExchangeRateCurrencies.map((currency, index) => [currency, index + 1])),
      date: "2026-08-03",
    }),
    syncHistory: async () => { syncCalls += 1; },
    now: () => Date.parse("2026-08-03T12:00:00.000Z"),
  });

  const response = await handler(new Request("http://local/api/exchange-rates"));
  const payload = await response.json();
  assert.equal(response.status, 200);
  assert.equal(payload.date, "2026-08-03");
  assert.equal(payload.rates.CNY, 1);
  assert.equal(upstreamCalls, 0);
  assert.equal(syncCalls, 0);
});

test("exchange-rate API always returns the stored D1 rate and never syncs on a page request", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  const db = { name: "db" };
  const syncOptions = [];
  const snapshots = [];
  const currentDate = "2026-07-31";
  const handler = createExchangeRatesHandler({
    authenticate: async () => ({ id: 7 }),
    fetch: async () => Response.json(validRates),
    getAssetsDb: async () => db,
    readLatestRates: async () => ({
      rates: Object.fromEntries(supportedExchangeRateCurrencies.map((currency, index) => [currency, index + 1])),
      date: currentDate,
    }),
    syncHistory: async (_db, options) => {
      syncOptions.push(options);
    },
    recordDailySnapshot: async (...args) => {
      snapshots.push(args);
      return { snapshot_date: "2026-08-03" };
    },
    now: () => Date.parse("2026-08-03T12:00:00.000Z"),
  });

  assert.equal((await (await handler(new Request("http://local/api/exchange-rates"))).json()).date, "2026-07-31");
  assert.equal((await (await handler(new Request("http://local/api/exchange-rates"))).json()).date, "2026-07-31");
  assert.deepEqual(syncOptions, []);
  const forced = await handler(new Request("http://local/api/exchange-rates?refresh=1"));
  assert.deepEqual(syncOptions, []);
  assert.deepEqual(snapshots, []);
  assert.equal((await forced.json()).date, "2026-07-31");
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
  assert.ok(Math.abs((await first.json()).rates.USD - 7.2) < 1e-10);

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
    return Response.json({ base: "USD", date: "2026-08-03", rates: Object.fromEntries(validRates.map((row) => [row.quote, row.rate])) });
  };
  const handler = createExchangeRatesHandler({ authenticate: async () => ({ id: 1 }), fetch });
  const response = await handler(new Request("http://local/api/exchange-rates"));
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.equal(payload.source, "ExchangeRate-API 备用汇率");
  assert.equal(payload.rates.CNY, 1);
});

test("exchange-rate API keeps serving stored rates when a cache-only request includes refresh=1", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  const db = { name: "db" };
  const fetch = async (url) => {
    if (String(url).includes("frankfurter")) return new Response("down", { status: 503 });
    return Response.json({ base: "USD", date: "2026-08-03", rates: Object.fromEntries(validRates.map((row) => [row.quote, row.rate])) });
  };
  const handler = createExchangeRatesHandler({
    authenticate: async () => ({ id: 1 }),
    fetch,
    getAssetsDb: async () => db,
    readLatestRates: async () => ({
      rates: Object.fromEntries(supportedExchangeRateCurrencies.map((currency, index) => [currency, index + 1])),
      date: "2026-08-02",
    }),
    syncHistory: async () => { throw new Error("history upstream down"); },
    now: () => Date.parse("2026-08-03T12:00:00.000Z"),
  });

  const response = await handler(new Request("http://local/api/exchange-rates?refresh=1"));
  const payload = await response.json();
  assert.equal(response.status, 200);
  assert.equal(payload.source, "本地历史汇率数据库");
  assert.equal(payload.stale, undefined);
});

test("production exchange-rate API reports an empty cache without fetching upstream", async () => {
  const { createExchangeRatesHandler } = await load("app/api/exchange-rates/handler.ts");
  let upstreamCalls = 0;
  const db = { name: "db" };
  const handler = createExchangeRatesHandler({
    authenticate: async () => ({ id: 7 }),
    fetch: async () => { upstreamCalls += 1; return Response.json(validRates); },
    getAssetsDb: async () => db,
    readLatestRates: async () => null,
  });
  const response = await handler(new Request("http://local/api/exchange-rates?refresh=1"));
  assert.equal(response.status, 503);
  assert.equal(upstreamCalls, 0);
});
