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
  assert.match(route, /UPDATE assets SET amount = \?, currency = \? WHERE id = \? AND user_id = \?/);
  assert.match(route, /if \(!result\.meta\.changes\).*404/);
  assert.doesNotMatch(route, /seedIfEmpty|sample-assets|贵州茅台/);
});

test("market-rate sync is persisted and delete updates the visible list", async () => {
  const [dashboard, marketHandler, marketCalculator] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/market/handler.ts"),
    read("app/api/market/calculator.ts"),
  ]);
  const market = `${marketHandler}\n${marketCalculator}`;

  assert.match(dashboard, /method: "PATCH"/);
  assert.match(dashboard, /body: JSON\.stringify\(\{ id: asset\.id, annualRate \}\)/);
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
  const [dashboard, assetsRoute, ratesRoute, schema, migration] = await Promise.all([
    read("app/Dashboard.tsx"),
    read("app/api/assets/handlers.ts"),
    read("app/api/exchange-rates/handler.ts"),
    read("db/schema.ts"),
    read("drizzle/0002_uneven_gunslinger.sql"),
  ]);

  assert.match(dashboard, /name="currency"/);
  assert.match(dashboard, /toCny\(asset, exchangeRates\)/);
  assert.match(dashboard, /总资产 · 折合人民币/);
  assert.match(dashboard, /外币按当前汇率不变测算/);
  assert.match(dashboard, /仅可修改币种和当前市值/);
  assert.match(dashboard, /资产金额与币种已保存/);
  assert.match(dashboard, /记录资产/);
  assert.match(assetsRoute, /supportedCurrencies/);
  assert.match(assetsRoute, /amount, currency, annual_rate/);
  assert.match(ratesRoute, /api\.frankfurter\.dev\/v2\/rates/);
  assert.match(ratesRoute, /rates\[currency\] = 1 \/ row\.rate/);
  assert.match(schema, /currency: text\("currency"\)\.notNull\(\)\.default\("CNY"\)/);
  assert.match(migration, /ADD `currency` text DEFAULT 'CNY' NOT NULL/);
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
