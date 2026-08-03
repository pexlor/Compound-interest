import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(`../${path}`, import.meta.url), "utf8");

test("new accounts start with an empty asset list", async () => {
  const [dashboard, register, readme] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/auth/register/handler.ts"),
    read("README.md"),
  ]);

  assert.match(dashboard, /useState<Asset\[\]>\(\[\]\)/);
  assert.match(dashboard, /账本还是空的/);
  assert.doesNotMatch(dashboard, /fallbackAssets|贵州茅台|三年期定期存款/);
  assert.match(register, /INSERT INTO users/);
  assert.doesNotMatch(register, /INSERT INTO assets|seed/i);
  assert.match(readme, /新注册用户从空账本开始，不会自动生成示例资产/);
});

test("asset reads, writes, updates, and deletes are scoped to the signed-in user", async () => {
  const route = await read("app/api/assets/handlers.ts");

  assert.match(route, /getAuthenticatedUser/);
  assert.match(route, /WHERE user_id = \?/);
  assert.match(route, /INSERT INTO assets \(user_id,/);
  assert.match(route, /DELETE FROM assets WHERE id = \? AND user_id = \?/);
  assert.match(route, /UPDATE assets SET annual_rate = \? WHERE id = \? AND user_id = \?/);
  assert.match(route, /UPDATE assets SET amount = \?, quantity = COALESCE\(\?, quantity\), currency = \? WHERE id = \? AND user_id = \?/);
  assert.match(route, /if \(!result\.meta\.changes\).*404/);
  assert.doesNotMatch(route, /seedIfEmpty|sample-assets|贵州茅台/);
});

test("market rates load in a batch and delete updates the visible list", async () => {
  const [dashboard, marketHandler, marketCalculator] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/market/handler.ts"),
    read("app/api/market/calculator.ts"),
  ]);
  const market = `${marketHandler}\n${marketCalculator}`;

  assert.match(dashboard, /loadMarketRates/);
  assert.match(dashboard, /\/api\/market\?days=/);
  assert.doesNotMatch(dashboard, /body: JSON\.stringify\(\{ id: asset\.id, annualRate \}\)/);
  assert.match(dashboard, /method: "DELETE"/);
  assert.match(dashboard, /current\.filter\(\(item\) => item\.id !== asset\.id\)/);
  assert.match(dashboard, /setAssets\((?:data|assetData)\.assets \?\? \[\]\)/);
  assert.match(dashboard, /例如 510300、QQQ、VOO/);
  assert.match(dashboard, /近10年/);
  assert.match(market, /isUsSecurityCode/);
  assert.match(market, /`us\$\{resolvedCode\}`/);
  assert.match(market, /腾讯证券美股历史行情/);
});

test("local authentication uses hashed passwords and server-only session cookies", async () => {
  const [auth, schema] = await Promise.all([
    read("db/auth.ts"),
    read("db/schema.ts"),
  ]);

  assert.match(auth, /PBKDF2/);
  assert.match(auth, /SHA-256/);
  assert.match(auth, /HttpOnly; SameSite=Lax/);
  assert.match(auth, /sessions\.token_hash/);
  assert.match(schema, /sqliteTable\("users"/);
  assert.match(schema, /sqliteTable\("sessions"/);
  assert.match(schema, /references\(\(\) => users\.id/);
});

test("multi-currency assets are persisted and totals are converted with latest rates", async () => {
  const [dashboard, assetsRoute, ratesRoute, marketRoute, marketPrewarm, schema, migration] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/assets/handlers.ts"),
    read("app/api/exchange-rates/handler.ts"),
    read("app/api/market/route.ts"),
    read("app/api/market/prewarm.ts"),
    read("db/schema.ts"),
    read("drizzle/0002_uneven_gunslinger.sql"),
  ]);

  assert.match(dashboard, /name="currency"/);
  assert.match(dashboard, /toCny\(asset, exchangeRates\)/);
  assert.match(dashboard, /总资产 · 折合人民币/);
  assert.match(dashboard, /外币按当前汇率不变测算/);
  assert.match(dashboard, /仅可修改币种和当前市值/);
  assert.match(dashboard, /持有数量/);
  assert.match(dashboard, /记录资产/);
  assert.match(assetsRoute, /supportedCurrencies/);
  assert.match(assetsRoute, /amount, quantity, currency, annual_rate/);
  assert.match(ratesRoute, /api\.frankfurter\.dev\/v2\/rates/);
  assert.match(ratesRoute, /rates\[currency\] = 1 \/ row\.rate/);
  assert.match(marketRoute, /historicalRate/);
  assert.match(marketPrewarm, /historicalRate/);
  assert.match(schema, /currency: text\("currency"\)\.notNull\(\)\.default\("CNY"\)/);
  assert.match(migration, /ADD `currency` text DEFAULT 'CNY' NOT NULL/);
});

test("stocks and funds use decimal quantities with live market prices", async () => {
  const [dashboard, assetsRoute, calculator, schema, migration] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/assets/handlers.ts"),
    read("app/api/market/calculator.ts"),
    read("db/schema.ts"),
    read("drizzle/0007_cute_mister_fear.sql"),
  ]);
  assert.match(dashboard, /name="quantity"/);
  assert.match(dashboard, /step="any"/);
  assert.match(dashboard, /quantity! \* marketReturn!\.currentPrice!/);
  assert.match(dashboard, /股票代码/);
  assert.match(assetsRoute, /validQuantity/);
  assert.match(assetsRoute, /股票代码和股数/);
  assert.match(calculator, /currentPrice/);
  assert.match(calculator, /qt\.gtimg\.cn/);
  assert.match(calculator, /最新单位净值/);
  assert.match(schema, /quantity: real\("quantity"\)/);
  assert.match(migration, /ADD `quantity` real/);
});

test("only funds expose and accept investment strategies", async () => {
  const [dashboard, assetsRoute] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/assets/handlers.ts"),
  ]);
  assert.match(dashboard, /supportsInvestment = \(asset:[^\n]+\) => asset\.category === "fund"/);
  assert.match(dashboard, /asset\.category === "fund" && asset\.investment_strategy/);
  assert.match(assetsRoute, /return category === "fund"/);
  assert.doesNotMatch(assetsRoute, /category = 'fund' OR \(category = 'stock'/);
});

test("asset history renders a trend chart, change table, and empty state", async () => {
  const dashboard = await read("app/Dashboard.tsx");
  assert.match(dashboard, /\/api\/history/);
  assert.match(dashboard, /资产历史/);
  assert.match(dashboard, /较上次/);
  assert.match(dashboard, /history-chart/);
  assert.match(dashboard, /还没有历史记录/);
  assert.match(dashboard, /再产生一天记录后显示走势/);
});

test("portfolio annual rate explains the selected historical lookback", async () => {
  const dashboard = await read("app/Dashboard.tsx");
  assert.match(dashboard, /组合预期年化（根据最近\{lookback\}年数据计算）/);
});

test("market returns load in one batch and show limited-history details", async () => {
  const dashboard = await read("app/Dashboard.tsx");
  assert.match(dashboard, /\/api\/market\?days=\$\{selectedLookback \* 365\}/);
  assert.match(dashboard, /历史不足，使用 \{asset\.market_return\.actualDays\} 天的数据计算/);
  assert.match(dashboard, /实际行情区间/);
  assert.match(dashboard, /旧数据/);
  assert.doesNotMatch(dashboard, /Promise\.all\(assets\.map\(async \(asset\)/);
});

test("Worker config binds R2 and separates exchange sync, market prewarm, and the 11:58 snapshot", async () => {
  const [viteConfig, worker, hosting, dailySnapshot] = await Promise.all([
    read("vite.config.ts"),
    read("worker/index.ts"),
    read(".openai/hosting.json"),
    read("app/api/history/daily-snapshot.ts"),
  ]);
  assert.match(viteConfig, /crons:\s*\["0 16 \* \* \*",\s*"10 20 \* \* \*",\s*"58 3 \* \* \*"\]/);
  assert.match(hosting, /"r2":\s*"RATES"/);
  assert.match(worker, /createWorkerLifecycle/);
  assert.match(worker, /RATES:\s*R2Bucket/);
  assert.match(worker, /createExchangeRateHistorySync/);
  assert.match(worker, /controller\.cron === "0 16 \* \* \*"/);
  assert.match(worker, /controller\.cron === "10 20 \* \* \*"/);
  assert.match(worker, /controller\.cron === "58 3 \* \* \*"/);
  assert.match(worker, /prewarmMarketReturns/);
  assert.match(worker, /runDailyAssetSnapshot/);
  assert.match(dailySnapshot, /scheduled_daily/);
  assert.match(dailySnapshot, /quantity.*currentPrice.*100/);
});
