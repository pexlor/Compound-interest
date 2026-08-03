# 资产历史走势实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 每次资产总额变化或手动刷新汇率时保存当天人民币总资产快照，并在 Dashboard 展示折线走势和变化明细。

**架构：** D1 持久化最近汇率和按用户/日期唯一的资产快照。资产与汇率 API 在成功写操作后调用共享快照服务，历史 API 只读取当前用户数据，前端在相关操作后刷新历史视图。

**技术栈：** TypeScript、Next.js Route Handlers、Cloudflare D1、React、Node Test Runner、CSS/SVG。

---

## 文件结构

- 创建 `db/history.ts`：汇率持久化、快照计算与同日 UPSERT。
- 修改 `db/schema.ts`、`db/assets.ts`：声明并初始化两张新表。
- 创建 `drizzle/0003_*.sql`：数据库迁移。
- 创建 `app/api/history/handlers.ts`、`route.ts`：当前用户历史查询接口。
- 修改 `app/api/assets/handlers.ts`、`route.ts`：资产总额变化后触发快照。
- 修改 `app/api/exchange-rates/handler.ts`、`route.ts`：成功刷新后保存汇率和快照。
- 修改 `app/Dashboard.tsx`、`app/globals.css`：历史折线图、明细表和刷新逻辑。
- 修改 `tests/api-behavior.test.mjs`、`tests/rendered-html.test.mjs`：服务端行为和 UI 回归测试。

### 任务 1：快照领域逻辑与数据表

**文件：**
- 创建：`db/history.ts`
- 修改：`db/schema.ts`
- 修改：`db/assets.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败测试**

测试 `calculateSnapshotTotal()`：完整汇率返回人民币分，缺失任一外币汇率返回 `null`；测试同一日期第二次 UPSERT 覆盖第一次结果。

- [ ] **步骤 2：验证红灯**

运行：`node --test --test-name-pattern='daily asset snapshot' tests/api-behavior.test.mjs`

预期：FAIL，提示 `db/history.ts` 或导出函数不存在。

- [ ] **步骤 3：实现领域逻辑和表结构**

核心接口：

```ts
export function calculateSnapshotTotal(
  assets: Array<{ amount: number; currency: string }>,
  rates: Record<string, number>,
): number | null;

export async function saveExchangeRates(
  db: D1Database,
  rates: Record<string, number>,
  rateDate: string,
): Promise<void>;

export async function recordDailySnapshot(
  db: D1Database,
  userId: number,
  trigger: "asset_change" | "exchange_refresh",
): Promise<HistoryRow | null>;
```

新增 `(user_id, snapshot_date)` 唯一索引，并使用 `Asia/Shanghai` 生成日期。

- [ ] **步骤 4：生成迁移并验证绿灯**

运行：`npm run db:generate`，再运行目标测试。预期：PASS。

### 任务 2：历史 API 与用户隔离

**文件：**
- 创建：`app/api/history/handlers.ts`
- 创建：`app/api/history/route.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败测试**

直接调用 handler，验证未登录返回 401；用户 1 只得到自己的记录；结果按日期升序；`limit` 被限制在 1 到 3650。

- [ ] **步骤 2：验证红灯**

运行：`node --test --test-name-pattern='history API' tests/api-behavior.test.mjs`

预期：FAIL，提示 history handler 不存在。

- [ ] **步骤 3：实现最小 handler**

```ts
export function createHistoryHandler(deps: Dependencies) {
  return async function GET(request: Request) {
    // authenticate, clamp limit, query current user, return ascending rows
  };
}
```

- [ ] **步骤 4：验证绿灯**

运行目标测试，预期全部 PASS。

### 任务 3：资产与汇率写操作触发快照

**文件：**
- 修改：`app/api/assets/handlers.ts`
- 修改：`app/api/assets/route.ts`
- 修改：`app/api/exchange-rates/handler.ts`
- 修改：`app/api/exchange-rates/route.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败测试**

断言新增、金额/币种修改、删除各调用一次 `recordDailySnapshot(..., "asset_change")`；年化收益率更新不调用。汇率成功刷新依次调用 `saveExchangeRates` 和 `recordDailySnapshot(..., "exchange_refresh")`。

- [ ] **步骤 2：验证红灯**

运行：`node --test --test-name-pattern='records a snapshot' tests/api-behavior.test.mjs`

预期：FAIL，快照调用次数为 0。

- [ ] **步骤 3：接入快照依赖**

资产响应添加 `snapshot` 字段。快照返回 `null` 时资产写操作仍成功。汇率刷新只在上游成功取得有效汇率时持久化汇率并更新快照。

- [ ] **步骤 4：验证绿灯与回归**

运行：`node --test tests/*.test.mjs`

预期：全部 PASS。

### 任务 4：Dashboard 历史走势图和明细表

**文件：**
- 修改：`app/Dashboard.tsx`
- 修改：`app/globals.css`
- 测试：`tests/rendered-html.test.mjs`

- [ ] **步骤 1：编写失败测试**

源码回归断言包含 `/api/history`、`资产历史`、`较上次`、`history-chart` 和空状态文案。

- [ ] **步骤 2：验证红灯**

运行：`node --test --test-name-pattern='asset history' tests/rendered-html.test.mjs`

预期：FAIL，Dashboard 尚无历史区域。

- [ ] **步骤 3：实现前端展示**

新增 `HistoryEntry` 状态；登录加载时并行请求历史数据；资产新增、金额/币种修改、删除和手动刷新汇率后重新读取。使用固定 `viewBox` 的 SVG `polyline` 绘制折线，并在表格中计算：

```ts
const change = current.total_cny - previous.total_cny;
const changeRate = previous.total_cny ? change / previous.total_cny * 100 : null;
```

- [ ] **步骤 4：验证响应式样式和测试**

运行 UI 回归测试和 `npm run lint`，预期 PASS。

### 任务 5：完整验证

**文件：**
- 修改：`README.md`

- [ ] **步骤 1：更新功能说明**

写明每日最后一条快照、触发操作、人民币历史口径和汇率缺失行为。

- [ ] **步骤 2：运行完整验证**

运行：

```bash
npm test
npm run lint
curl -sS -o /tmp/compound-history.html -w '%{http_code}' http://localhost:3000/
```

预期：构建成功、全部测试通过、lint 无错误、首页返回 200。

- [ ] **步骤 3：真实 API 验证与清理**

注册专用临时用户，新增 CNY 和 USD 资产，刷新汇率、修改金额并读取 `/api/history`；确认同日只有一条且为最后总额。验证后精确删除临时用户及级联数据。
