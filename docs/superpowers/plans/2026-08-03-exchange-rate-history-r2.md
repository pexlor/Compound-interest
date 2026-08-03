# R2 汇率历史库实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 使用一个 R2 JSON 文件和 D1 历史表保存 9 种外币滚动 10 年汇率，在启动和北京时间午夜同步，并让页面和美股收益优先读取数据库。

**架构：** `history-file.ts` 负责纯数据校验、窗口和合并，`exchange-rate-history.ts` 负责 D1 幂等存储，`history-sync.ts` 组合 Frankfurter、R2 和 D1 完成同步。Worker 根据 Cron 分流汇率同步与市场预热，启动流水线先同步汇率再计算市场收益。

**技术栈：** TypeScript、Cloudflare Workers、R2、D1 SQLite、Drizzle ORM、Next.js 16、Node.js Test Runner。

---

## 文件结构

- 创建 `db/exchange-rate-history.ts`：历史汇率 D1 读写、分批导入、裁剪和最新值刷新。
- 创建 `app/api/exchange-rates/history-file.ts`：R2 JSON 类型、校验、日期窗口、汇率转换和滚动合并。
- 创建 `app/api/exchange-rates/history-sync.ts`：分段抓取、R2 条件写、重试、D1 导入和结构化日志。
- 修改 `db/schema.ts`、`db/assets.ts` 并生成 `drizzle/0006_*.sql`：新增历史表和索引。
- 修改 `app/api/exchange-rates/handler.ts`、`route.ts`：数据库优先读取和手动强制同步。
- 修改 `app/api/market/historical-rates.ts`、`calculator.ts`、`prewarm.ts`、`route.ts`：D1 历史汇率优先与外部兜底。
- 修改 `worker/lifecycle.ts`、`worker/index.ts`、`vite.config.ts`、`.openai/hosting.json`：R2 绑定、启动顺序和两条 Cron 分流。
- 修改 `tests/api-behavior.test.mjs`、`tests/rendered-html.test.mjs`：覆盖存储、文件、同步、API、行情和调度。
- 修改 `README.md`：说明 R2 文件、滚动 10 年和日志。

### 任务 1：D1 历史汇率表与幂等仓库

**文件：**
- 创建：`db/exchange-rate-history.ts`
- 修改：`db/schema.ts`
- 修改：`db/assets.ts`
- 创建：`drizzle/0006_*.sql`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败测试**

```js
test("exchange rate history inserts missing rows, keeps existing values, and finds the latest prior date", async () => {
  const { importExchangeRateHistory, findHistoricalCnyRate } = await load("db/exchange-rate-history.ts");
  const db = createExchangeRateHistoryDb();
  const first = await importExchangeRateHistory(db, sampleHistoryEntries());
  const second = await importExchangeRateHistory(db, sampleHistoryEntries({ USD: 99 }));
  assert.equal(first.inserted, 18);
  assert.equal(second.inserted, 0);
  assert.deepEqual(await findHistoricalCnyRate(db, "USD", "2026-08-02"), {
    currency: "USD", cnyRate: 7.18, rateDate: "2026-07-31", source: "test",
  });
});
```

同时让 mock 统计每个 D1 batch 的语句数，并断言导入 202 条记录时每批不超过 100 条。

- [ ] **步骤 2：运行红灯**

运行：`node --test --test-name-pattern="exchange rate history inserts" tests/api-behavior.test.mjs`

预期：FAIL，指出 `db/exchange-rate-history.ts` 缺失。

- [ ] **步骤 3：实现最小仓库和表结构**

导出：

```ts
export type ExchangeRateHistoryEntry = {
  currency: string; cnyRate: number; rateDate: string; source: string; fetchedAt: string;
};
export async function importExchangeRateHistory(db: D1Database, entries: ExchangeRateHistoryEntry[]): Promise<{ inserted: number }>;
export async function findHistoricalCnyRate(db: D1Database, currency: string, onOrBefore: string): Promise<StoredHistoricalRate | null>;
export async function readLatestExchangeRates(db: D1Database): Promise<{ rates: Record<string, number>; date: string } | null>;
export async function pruneExchangeRateHistory(db: D1Database, cutoffDate: string): Promise<void>;
export async function refreshLatestExchangeRates(db: D1Database): Promise<void>;
```

历史导入按 100 条分组，每条执行 `INSERT OR IGNORE`；用 D1 batch 结果的 `meta.changes` 求新增数。在初始化 SQL 和 Drizzle schema 中添加表及唯一索引，运行 `npm run db:generate`。

- [ ] **步骤 4：运行绿灯**

运行：`node --test --test-name-pattern="exchange rate history inserts" tests/api-behavior.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交任务 1**

```bash
git add db/exchange-rate-history.ts db/schema.ts db/assets.ts drizzle tests/api-behavior.test.mjs
git commit -m "feat: 添加历史汇率数据库（任务 1/6）"
```

### 任务 2：R2 历史文件模型与 Frankfurter 分段读取

**文件：**
- 创建：`app/api/exchange-rates/history-file.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写文件模型失败测试**

```js
test("exchange history file validates, converts all currencies, and keeps a rolling ten-year window", async () => {
  const { createEmptyHistoryFile, mergeRateRows, parseHistoryFile } = await load("app/api/exchange-rates/history-file.ts");
  const empty = createEmptyHistoryFile("2016-08-03");
  const merged = mergeRateRows(empty, frankfurterRows("2026-07-31"), "2026-08-02", new Date("2026-08-03T00:00:00+08:00"));
  assert.equal(merged.dates["2026-07-31"].rates.USD, 1 / 0.139275766);
  assert.deepEqual(Object.keys(merged.dates["2026-07-31"].rates).sort(), SUPPORTED_CURRENCIES);
  assert.equal(merged.checkedThrough, "2026-08-02");
  assert.throws(() => parseHistoryFile('{"version":2}'), /不支持的汇率历史文件版本/);
});
```

另测 `historyWindows("2016-08-03", "2026-08-02")` 生成连续、无重叠、每段不超过 1 年的窗口。

- [ ] **步骤 2：运行红灯**

运行：`node --test --test-name-pattern="exchange history file" tests/api-behavior.test.mjs`

预期：FAIL，指出 `history-file.ts` 缺失。

- [ ] **步骤 3：实现纯函数**

定义：

```ts
export const SUPPORTED_CURRENCIES = ["AUD", "CAD", "CHF", "EUR", "GBP", "HKD", "JPY", "SGD", "USD"] as const;
export type ExchangeRateHistoryFile = {
  version: 1; base: "CNY"; checkedThrough: string; updatedAt: string;
  dates: Record<string, { source: string; rates: Record<string, number> }>;
};
export function parseHistoryFile(json: string): ExchangeRateHistoryFile;
export function createEmptyHistoryFile(checkedThrough: string): ExchangeRateHistoryFile;
export function historyWindows(from: string, to: string): Array<{ from: string; to: string }>;
export function mergeRateRows(file: ExchangeRateHistoryFile, rows: RateRow[], checkedThrough: string, now: Date): ExchangeRateHistoryFile;
export function pruneHistoryFile(file: ExchangeRateHistoryFile, cutoffDate: string): ExchangeRateHistoryFile;
```

严格验证日期、正数、币种集合和基准币种。按日期聚合 Frankfurter 行并取倒数，只保存 9 种币种均完整的日期。

- [ ] **步骤 4：运行绿灯**

运行：`node --test --test-name-pattern="exchange history file" tests/api-behavior.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交任务 2**

```bash
git add app/api/exchange-rates/history-file.ts tests/api-behavior.test.mjs
git commit -m "feat: 添加 R2 汇率文件模型（任务 2/6）"
```

### 任务 3：R2 同步服务、条件写与日志

**文件：**
- 创建：`app/api/exchange-rates/history-sync.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写同步服务失败测试**

使用内存 R2 mock 覆盖：

```js
assert.equal(fetchWindows.length, 10); // 首次按年度回填
assert.equal(r2.puts.length, 1); // 全部分段成功后只写一个对象
assert.equal(summary.currencies, 9);
assert.equal(summary.inserted, 18);
assert.ok(logs.some((entry) => entry.event === "sync_complete"));
```

再覆盖已有文件仅请求尾部、周末空结果仍推进 `checkedThrough`、任一窗口失败时 `r2.puts.length === 0`、损坏文件不覆盖、第一次 ETag 冲突后重读合并成功。

- [ ] **步骤 2：运行红灯**

运行：`node --test --test-name-pattern="exchange history sync" tests/api-behavior.test.mjs`

预期：FAIL，指出 `history-sync.ts` 缺失。

- [ ] **步骤 3：实现同步服务**

导出：

```ts
export function createExchangeRateHistorySync(dependencies: {
  db: D1Database; bucket: R2Bucket; fetch: typeof fetch; now?: () => Date;
  logger?: Pick<Console, "info" | "warn" | "error">; timeoutMs?: number;
}) {
  return { sync(options?: { forceLatest?: boolean }): Promise<ExchangeRateSyncSummary> };
}
```

对象键固定为 `exchange-rates/history.json`。首次从 UTC 截止日向前 10 年分段抓取；增量从 `checkedThrough + 1` 开始。`forceLatest` 即使无日期缺口也请求最新接口。所有请求成功后裁剪、序列化并使用 R2 `onlyIf` 条件写；冲突最多重试 2 次。文件写成功后导入 D1、裁剪 D1 并刷新最新表。

日志调用形式固定为：

```ts
logger.info("[exchange-rate-history]", { event: "sync_complete", inserted, dates, durationMs });
```

- [ ] **步骤 4：运行绿灯并做中途构建**

运行：`node --test --test-name-pattern="exchange history sync" tests/api-behavior.test.mjs && npm run build`

预期：测试通过，构建退出码 0。

- [ ] **步骤 5：提交任务 3**

```bash
git add app/api/exchange-rates/history-sync.ts tests/api-behavior.test.mjs
git commit -m "feat: 实现 R2 汇率历史同步（任务 3/6）"
```

### 任务 4：最新汇率 API 和美股历史汇率接入 D1

**文件：**
- 修改：`app/api/exchange-rates/handler.ts`
- 修改：`app/api/exchange-rates/route.ts`
- 修改：`app/api/market/historical-rates.ts`
- 修改：`app/api/market/calculator.ts`
- 修改：`app/api/market/route.ts`
- 修改：`app/api/market/prewarm.ts`
- 测试：`tests/api-behavior.test.mjs`
- 测试：`tests/rendered-html.test.mjs`

- [ ] **步骤 1：编写数据库优先失败测试**

```js
const response = await latestHandler(new Request("http://local/api/exchange-rates"));
assert.equal(upstreamCalls, 0);
assert.equal((await response.json()).date, "2026-07-31");

const historical = await fetchHistoricalUsdCnyRate({ db, fetcher }, "2025-07-31");
assert.equal(historical.rate, 7.2);
assert.equal(upstreamCalls, 0);
```

验证数据库不完整时调用同步，`refresh=1` 调用 `sync({ forceLatest: true })` 并记录快照；历史 D1 缺失时保留外部兜底并插入 D1。

- [ ] **步骤 2：运行红灯**

运行：`node --test --test-name-pattern="exchange-rate API prefers|historical USD/CNY prefers" tests/api-behavior.test.mjs`

预期：FAIL，当前代码仍访问外部接口或函数签名不支持 D1。

- [ ] **步骤 3：实现 API 和计算器注入**

`createExchangeRatesHandler` 注入 `readLatestRates` 和 `syncHistory`。普通 GET 先读 D1；强制刷新调用同步后再读 D1。`fetchHistoricalUsdCnyRate` 接收 `{ db, fetcher }`，先调用 `findHistoricalCnyRate`，缺失时执行原 7 天上游查询并用单条 `INSERT OR IGNORE` 补 D1。

计算器新增可选 `historicalRate` 依赖，生产 `route.ts` 和 `prewarm.ts` 注入 D1 读取器；现有测试默认继续使用外部读取器。

- [ ] **步骤 4：运行 API、行情和静态测试**

运行：`node --test --test-name-pattern="exchange-rate|historical USD/CNY|US stock" tests/*.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交任务 4**

```bash
git add app/api/exchange-rates app/api/market tests
git commit -m "feat: 汇率读取优先使用历史数据库（任务 4/6）"
```

### 任务 5：R2 绑定、启动顺序和 Cron 分流

**文件：**
- 修改：`.openai/hosting.json`
- 修改：`vite.config.ts`
- 修改：`worker/lifecycle.ts`
- 修改：`worker/index.ts`
- 测试：`tests/api-behavior.test.mjs`
- 测试：`tests/rendered-html.test.mjs`

- [ ] **步骤 1：编写调度失败测试**

```js
assert.deepEqual(order, ["exchange", "market"]); // 首次请求后台流水线顺序
lifecycle.scheduled({ cron: "0 16 * * *" }, env, ctx);
assert.equal(exchangeCalls, 2);
lifecycle.scheduled({ cron: "10 20 * * *" }, env, ctx);
assert.equal(marketCalls, 2);
```

静态断言 `.openai/hosting.json` 使用 `RATES`，Vite 配置包含两条 Cron，Worker 把 `env.RATES` 传入同步服务。

- [ ] **步骤 2：运行红灯**

运行：`node --test --test-name-pattern="Worker.*exchange|R2 exchange" tests/*.test.mjs`

预期：FAIL，当前生命周期只支持一个 prewarm 回调且配置没有 R2。

- [ ] **步骤 3：实现调度分流**

生命周期依赖改为：

```ts
startup(env): Promise<unknown>;
scheduled(controller: { cron: string }, env: Env): Promise<unknown>;
```

Worker 的 `startup` 先 `syncExchangeRateHistory(env.DB, env.RATES)`，捕获汇率错误后继续 `prewarmMarketReturns`。`scheduled` 按表达式只执行对应服务。Vite 配置加入 `RATES` R2 绑定和两条 Cron。

- [ ] **步骤 4：运行调度测试和构建**

运行：`node --test --test-name-pattern="Worker.*exchange|R2 exchange" tests/*.test.mjs && npm run build`

预期：PASS，生成的 `dist/server/wrangler.json` 同时包含 R2 绑定和两条 Cron。

- [ ] **步骤 5：提交任务 5**

```bash
git add .openai/hosting.json vite.config.ts worker tests
git commit -m "feat: 定时同步 R2 汇率历史（任务 5/6）"
```

### 任务 6：文档、完整验证与推送

**文件：**
- 修改：`README.md`
- 修改：`eslint.config.mjs`（仅在新增生成目录需要忽略时）

- [ ] **步骤 1：更新 README**

说明 R2 历史文件、滚动 10 年、北京时间 00:00/04:10 任务、启动导入、D1 优先和 `[exchange-rate-history]` 日志。

- [ ] **步骤 2：运行完整测试**

运行：`npm test`

预期：构建成功，全部 Node.js 测试 0 失败。

- [ ] **步骤 3：运行静态检查**

运行：`npm run lint`

预期：退出码 0，无 ESLint 错误。

- [ ] **步骤 4：检查差异和需求覆盖**

运行：`git diff --check && git status --short`

逐项核对首次回填、增量文件、幂等导入、滚动裁剪、数据库优先、启动顺序、Cron 分流和日志均有通过测试。保留用户原有未跟踪 `tsconfig.tsbuildinfo`。

- [ ] **步骤 5：提交并推送 GitHub**

```bash
git add README.md
git commit -m "docs: 说明汇率历史同步（任务 6/6）"
git push origin main
```
