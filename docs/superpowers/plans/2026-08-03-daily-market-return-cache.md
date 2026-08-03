# 每日市场收益缓存实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 将股票、基金和货币基金的 1、3、5、10 年收益持久化为每日共享缓存，并通过定时、启动预热和读取缺失回填维护缓存，同时展示历史不足提示。

**架构：** 行情计算器只负责从上游生成完整收益结果，`db/market-returns.ts` 负责 D1 缓存读写，`market-return-service.ts` 组合两者实现 read-through 和批量预热。API 提供单代码与当前用户批量读取，Worker 负责 Cron 和首次请求后台预热，前端只进行一次批量读取并把所选区间结果合并到资产状态。

**技术栈：** TypeScript、Next.js 16、React 19、Cloudflare Workers、D1 SQLite、Drizzle ORM、Node.js Test Runner。

---

## 文件结构

- 创建 `db/market-returns.ts`：北京日期、缓存记录类型以及 D1 查询和 upsert。
- 创建 `app/api/market/calculator.ts`：从现有 handler 提取行情计算，并返回历史不足元数据。
- 创建 `app/api/market/market-return-service.ts`：read-through、旧缓存回退、用户批量读取和全库预热。
- 修改 `db/schema.ts`、`db/assets.ts` 并生成 `drizzle/0005_*.sql`：定义和初始化 `market_returns`。
- 修改 `app/api/market/handler.ts`、`app/api/market/route.ts`：接入持久化缓存及批量模式。
- 修改 `worker/index.ts`、`vite.config.ts`：接入 Cron 和首次请求后台预热。
- 修改 `app/Dashboard.tsx`、`app/globals.css`：批量加载、区间切换和历史不足/旧数据提示。
- 修改 `tests/api-behavior.test.mjs`：覆盖计算、缓存、回退、去重、预热与时区。
- 修改 `tests/rendered-html.test.mjs`：覆盖前端批量读取和提示文案。
- 修改 `README.md`：说明每日缓存、调度和读取回填行为。

### 任务 1：持久化缓存表和存储 API

**文件：**
- 创建：`db/market-returns.ts`
- 修改：`db/schema.ts`
- 修改：`db/assets.ts`
- 创建：`drizzle/0005_*.sql`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败测试**

在 `tests/api-behavior.test.mjs` 增加测试，固定当前时刻并验证北京日期以及缓存仓库行为：

```js
test("market return cache uses the Shanghai date and upserts one daily row", async () => {
  const { shanghaiDate, saveMarketReturn } = await load("db/market-returns.ts");
  assert.equal(shanghaiDate(new Date("2026-08-02T20:10:00Z")), "2026-08-03");
  const db = createMarketReturnDb();
  await saveMarketReturn(db, sampleMarketReturn({ annualRate: 8 }));
  await saveMarketReturn(db, sampleMarketReturn({ annualRate: 9 }));
  assert.equal(db.rows.size, 1);
  assert.equal([...db.rows.values()][0].annual_rate, 9);
});
```

- [ ] **步骤 2：运行测试并确认因模块缺失而失败**

运行：`node --test --test-name-pattern="market return cache" tests/api-behavior.test.mjs`

预期：FAIL，错误指出 `db/market-returns.ts behavior module is missing`。

- [ ] **步骤 3：实现表结构和最小缓存仓库**

定义统一返回类型和函数签名：

```ts
export type MarketReturnRecord = {
  category: string; code: string; lookbackDays: number; calculationDate: string;
  annualRate: number; periodReturn: number; requestedDays: number; actualDays: number;
  historyLimited: boolean; startDate: string; endDate: string; source: string;
  calculatedAt?: string; stale?: boolean;
};

export function shanghaiDate(now = new Date()): string;
export async function findMarketReturn(db: D1Database, category: string, code: string, lookbackDays: number, calculationDate: string): Promise<MarketReturnRecord | null>;
export async function findLatestMarketReturn(db: D1Database, category: string, code: string, lookbackDays: number): Promise<MarketReturnRecord | null>;
export async function saveMarketReturn(db: D1Database, record: MarketReturnRecord): Promise<MarketReturnRecord>;
```

在 `db/assets.ts` 的初始化 batch 中创建表和唯一索引；在 `db/schema.ts` 添加 Drizzle 定义，然后运行 `npm run db:generate` 生成迁移。

- [ ] **步骤 4：运行聚焦测试并确认通过**

运行：`node --test --test-name-pattern="market return cache" tests/api-behavior.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交缓存存储单元**

```bash
git add db/market-returns.ts db/schema.ts db/assets.ts drizzle tests/api-behavior.test.mjs
git commit -m "feat: 添加每日市场收益缓存表"
```

### 任务 2：提取行情计算器并标记历史不足

**文件：**
- 创建：`app/api/market/calculator.ts`
- 修改：`app/api/market/handler.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：扩展现有行情测试使其先失败**

在基金和股票上市时间不足的既有测试中增加：

```js
assert.equal(payload.requestedDays, 3650);
assert.equal(payload.actualDays, 867);
assert.equal(payload.historyLimited, true);
```

在正常目标日期窗口测试中增加 `assert.equal(payload.historyLimited, false)`；货币基金测试增加 `assert.equal(payload.historyLimited, false)`。

- [ ] **步骤 2：运行历史不足测试并确认字段缺失导致失败**

运行：`node --test --test-name-pattern="predates|target-date|money" tests/api-behavior.test.mjs`

预期：FAIL，实际 `historyLimited` 为 `undefined`。

- [ ] **步骤 3：提取并实现最小计算器**

从 handler 移动上游请求、股票/基金计算和并发限制，导出：

```ts
export type MarketCalculation = Omit<MarketReturnRecord, "calculationDate" | "calculatedAt" | "stale">;
export function createMarketCalculator(dependencies: { fetch: typeof fetch; timeoutMs?: number; maxConcurrent?: number }): {
  calculate(category: string, code: string, lookbackDays: number): Promise<MarketCalculation>;
};
```

当目标日期窗口有数据时返回 `historyLimited: false`；只有使用最早行情回退分支时返回 `true`。`actualDays` 取完整自然日差，货币基金始终返回 `false`。

- [ ] **步骤 4：运行全部 market 计算测试**

运行：`node --test --test-name-pattern="market|fund returns|stock returns|QQQ|USD/CNY" tests/api-behavior.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交计算器单元**

```bash
git add app/api/market/calculator.ts app/api/market/handler.ts tests/api-behavior.test.mjs
git commit -m "feat: 标记市场收益历史不足"
```

### 任务 3：实现 read-through、批量读取和全库预热

**文件：**
- 创建：`app/api/market/market-return-service.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写 read-through 和批量预热失败测试**

覆盖以下断言：

```js
assert.equal(calculatorCalls, 0); // 当天命中不访问上游
assert.equal(saved.length, 1); // 当天缺失计算后落库
assert.equal(staleResult.stale, true); // 计算失败回退最近成功记录
assert.deepEqual(prewarmCalls.map((item) => item.lookbackDays), [365, 1095, 1825, 3650]);
assert.equal(prewarmCalls.filter((item) => item.code === "QQQ").length, 4); // 重复资产已去重
assert.equal(summary.failed, 1); // 单项失败不终止批次
```

- [ ] **步骤 2：运行测试并确认服务模块缺失**

运行：`node --test --test-name-pattern="read-through|prewarm" tests/api-behavior.test.mjs`

预期：FAIL，错误指出 `market-return-service.ts` 缺失。

- [ ] **步骤 3：实现最小服务**

导出受支持区间和工厂：

```ts
export const LOOKBACK_DAYS = [365, 1095, 1825, 3650] as const;
export type MarketReturnError = { category: string; code: string; lookbackDays: number; error: string };
export function createMarketReturnService(dependencies: {
  db: D1Database; calculate(category: string, code: string, days: number): Promise<MarketCalculation>;
  now?: () => Date; concurrency?: number;
}) {
  return {
    get(category: string, code: string, lookbackDays: number): Promise<MarketReturnRecord>,
    getForUser(userId: number, lookbackDays: number): Promise<{ results: MarketReturnRecord[]; errors: MarketReturnError[] }>,
    prewarmAll(): Promise<{ succeeded: number; failed: number; errors: MarketReturnError[] }>,
  };
}
```

`get` 依次执行当天查询、计算与保存、失败后最近缓存回退。`getForUser` 查询当前用户资产并去重；`prewarmAll` 查询全库不重复键，并使用固定并发队列隔离失败。

- [ ] **步骤 4：运行服务测试并确认通过**

运行：`node --test --test-name-pattern="read-through|prewarm" tests/api-behavior.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交市场收益服务**

```bash
git add app/api/market/market-return-service.ts tests/api-behavior.test.mjs
git commit -m "feat: 实现市场收益缓存回填与预热"
```

### 任务 4：接入单代码和当前用户批量 API

**文件：**
- 修改：`app/api/market/handler.ts`
- 修改：`app/api/market/route.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写批量 API 失败测试**

```js
const response = await handler(new Request("http://local/api/market?days=1095"));
assert.equal(response.status, 200);
assert.deepEqual((await response.json()).results.map((item) => item.code), ["021000", "QQQ"]);
assert.equal(getForUserCalls[0].lookbackDays, 1095);
```

同时验证无代码批量模式仍需鉴权、非法区间返回 400、单代码模式调用 `service.get`。

- [ ] **步骤 2：运行 market API 测试并确认批量请求返回 400**

运行：`node --test --test-name-pattern="market API.*batch|batch market" tests/api-behavior.test.mjs`

预期：FAIL，当前 handler 报「请输入有效代码」。

- [ ] **步骤 3：让 handler 只负责参数、鉴权和响应映射**

`createMarketHandler` 注入 `getService(db)`；有 `code` 时调用 `service.get`，没有 `code` 时调用 `service.getForUser(user.id, days)`。`route.ts` 通过 `getAssetsDb`、计算器和服务工厂组装依赖。

- [ ] **步骤 4：运行 API 行为测试**

运行：`node --test --test-name-pattern="market API|batch market" tests/api-behavior.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交 API 接入**

```bash
git add app/api/market/handler.ts app/api/market/route.ts tests/api-behavior.test.mjs
git commit -m "feat: 添加批量市场收益读取接口"
```

### 任务 5：接入 Worker Cron 和启动后台预热

**文件：**
- 修改：`worker/index.ts`
- 修改：`vite.config.ts`
- 测试：`tests/api-behavior.test.mjs`
- 测试：`tests/rendered-html.test.mjs`

- [ ] **步骤 1：编写调度和非阻塞启动测试**

对可注入的 Worker 工厂验证：首次 fetch 调用一次 `ctx.waitUntil` 且先返回响应，后续 fetch 不重复启动；`scheduled` 调用预热。静态配置测试验证 `vite.config.ts` 包含：

```js
assert.match(viteConfig, /crons:\s*\["10 20 \* \* \*"\]/);
```

- [ ] **步骤 2：运行测试并确认 scheduled/cron 缺失**

运行：`node --test --test-name-pattern="Worker.*prewarm|cron" tests/*.test.mjs`

预期：FAIL，Worker 没有 `scheduled`，Vite 配置没有 Cron。

- [ ] **步骤 3：实现 Worker 预热入口**

将预热组装放在不依赖用户请求的函数中。Worker 的普通 fetch 执行：

```ts
startupWarmup ??= prewarmMarketReturns(env.DB, fetch).catch((error) => console.error("市场收益启动预热失败", error));
ctx.waitUntil(startupWarmup);
return handler.fetch(request, env, ctx);
```

`scheduled` 使用同一 `prewarmMarketReturns` 并交给 `ctx.waitUntil`。在 `localBindingConfig` 增加 `triggers: { crons: ["10 20 * * *"] }`。

- [ ] **步骤 4：运行 Worker 与配置测试**

运行：`node --test --test-name-pattern="Worker.*prewarm|cron" tests/*.test.mjs`

预期：PASS。

- [ ] **步骤 5：提交调度接入**

```bash
git add worker/index.ts vite.config.ts tests/api-behavior.test.mjs tests/rendered-html.test.mjs
git commit -m "feat: 定时预热每日市场收益"
```

### 任务 6：前端批量读取和历史不足展示

**文件：**
- 修改：`app/Dashboard.tsx`
- 修改：`app/globals.css`
- 修改：`tests/rendered-html.test.mjs`
- 修改：`README.md`

- [ ] **步骤 1：编写前端源代码行为失败测试**

```js
assert.match(dashboard, /fetch\(`\/api\/market\?days=\$\{lookback \* 365\}`\)/);
assert.match(dashboard, /历史不足，使用 \{.*actualDays.*\} 天的数据计算/);
assert.match(dashboard, /行情缓存日期|旧数据/);
assert.doesNotMatch(dashboard, /Promise\.all\(assets\.map\(async \(asset\)/);
```

- [ ] **步骤 2：运行前端测试并确认批量读取和提示缺失**

运行：`node --test --test-name-pattern="market-rate|historical lookback|history limited" tests/rendered-html.test.mjs`

预期：FAIL。

- [ ] **步骤 3：实现最小前端状态和交互**

为资产增加可选市场元数据：

```ts
type MarketReturnMeta = {
  requestedDays: number; actualDays: number; historyLimited: boolean;
  startDate: string; endDate: string; calculationDate: string; stale?: boolean;
};
```

实现 `loadMarketRates(lookback)`，单次调用批量 API，通过 `category + normalized code` 合并 `annual_rate` 和元数据。登录默认加载近 3 年；区间变化自动调用；同步按钮复用该函数。资产行、详情和组合摘要按设计展示提示，并在 CSS 中提供不改变行高布局的稳定提示样式。

- [ ] **步骤 4：运行前端测试**

运行：`node --test --test-name-pattern="market-rate|historical lookback|history limited" tests/rendered-html.test.mjs`

预期：PASS。

- [ ] **步骤 5：更新 README 并提交前端单元**

README 说明北京时间 04:10、启动后台预热、当天缺失自动补算及历史不足提示。

```bash
git add app/Dashboard.tsx app/globals.css tests/rendered-html.test.mjs README.md
git commit -m "feat: 展示缓存收益与历史不足提示"
```

### 任务 7：完整验证与实现审查

**文件：**
- 检查：所有本计划修改文件

- [ ] **步骤 1：运行完整测试**

运行：`npm test`

预期：构建成功，所有 Node.js 测试 0 失败。

- [ ] **步骤 2：运行静态检查**

运行：`npm run lint`

预期：退出码 0，无 ESLint 错误。

- [ ] **步骤 3：检查迁移和差异**

运行：`git diff --check HEAD~6..HEAD && git status --short`

预期：无空白错误；只保留用户原有的未跟踪 `tsconfig.tsbuildinfo`，计划内文件均已提交。

- [ ] **步骤 4：逐项核对验收标准**

核对定时、启动、缓存命中、缺失回填、旧缓存、历史不足、批量隔离和 UI 提示均有对应通过测试。若任何项缺少证据，先补失败测试再实现。
