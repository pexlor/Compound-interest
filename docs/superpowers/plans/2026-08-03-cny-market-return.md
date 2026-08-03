# 人民币口径市场收益实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 将美股价格收益按起止日期的 USD/CNY 历史汇率换算为人民币收益，并保持国内资产现有口径。

**架构：** 新增独立历史汇率模块，负责查询行情日期前七天窗口并选择最近有效汇率。市场处理器仅在美股路径调用该模块，将价格乘以汇率后复用现有区间收益和年化公式；历史汇率不可用时整次同步失败。

**技术栈：** TypeScript、Fetch API、Frankfurter v2、Node.js test runner

---

## 文件结构

- 创建：`app/api/market/historical-rates.ts`，封装历史 USD/CNY 汇率窗口查询和校验。
- 修改：`app/api/market/handler.ts`，注入人民币换算并更新来源说明。
- 修改：`tests/api-behavior.test.mjs`，覆盖汇率选点、人民币收益、错误和国内资产隔离。

### 任务 1：历史汇率窗口查询

**文件：**
- 创建：`app/api/market/historical-rates.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败的汇率选点测试**

构造包含目标日前多个 USD/CNY 行的响应，断言选择不晚于目标日的最新有效行；再构造空响应，断言抛出 `没有找到对应日期的美元人民币历史汇率`。

- [ ] **步骤 2：运行测试验证失败**

运行：`node --test --test-name-pattern='historical USD/CNY' tests/api-behavior.test.mjs`

预期：FAIL，因为 `app/api/market/historical-rates.ts` 尚不存在。

- [ ] **步骤 3：实现最少汇率查询模块**

导出以下接口：

```ts
export async function fetchHistoricalUsdCnyRate(
  fetcher: (url: string) => Promise<Response>,
  marketDate: string,
): Promise<{ date: string; rate: number }>
```

请求 `https://api.frankfurter.dev/v2/rates`，参数为 `base=USD`、`quotes=CNY`、`from=目标日前七天`、`to=目标日`，过滤无效值并返回日期最近的一行。

- [ ] **步骤 4：运行汇率测试验证通过**

运行：`node --test --test-name-pattern='historical USD/CNY' tests/api-behavior.test.mjs`

预期：PASS。

### 任务 2：美股人民币收益换算

**文件：**
- 修改：`app/api/market/handler.ts`
- 测试：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：编写失败的美股人民币收益测试**

模拟起价 `100`、终价 `120`、起始汇率 `7.2`、结束汇率 `6.6`，断言：

```text
periodReturn = (120 * 6.6 / (100 * 7.2) - 1) * 100 = 10%
```

同时断言国内基金请求中不出现 `frankfurter.dev`。

- [ ] **步骤 2：运行测试验证失败**

运行：`node --test --test-name-pattern='CNY return|domestic market return' tests/api-behavior.test.mjs`

预期：FAIL，美股仍返回 20% 美元收益。

- [ ] **步骤 3：接入历史汇率换算**

在美股选定起止行情后并行查询两端汇率：

```ts
const [startRate, endRate] = await Promise.all([
  fetchHistoricalUsdCnyRate((url) => upstreamFetch(url), String(first[0])),
  fetchHistoricalUsdCnyRate((url) => upstreamFetch(url), String(last[0])),
]);
const start = Number(first[2]) * startRate.rate;
const end = Number(last[2]) * endRate.rate;
```

国内证券仍直接使用价格；美股 `source` 改为 `腾讯证券美股历史行情（人民币汇率调整）`。

- [ ] **步骤 4：验证失败策略**

增加历史汇率空响应测试，断言市场 API 返回 502 且不包含美元收益结果。

- [ ] **步骤 5：运行相关测试验证通过**

运行：`node --test --test-name-pattern='CNY return|domestic market return|historical USD/CNY' tests/api-behavior.test.mjs`

预期：全部 PASS。

### 任务 3：全量与真实数据验证

**文件：**
- 修改：`tests/api-behavior.test.mjs`

- [ ] **步骤 1：运行静态检查**

运行：`npx eslint app/api/market/handler.ts app/api/market/historical-rates.ts tests/api-behavior.test.mjs`

预期：退出码 0。

- [ ] **步骤 2：运行构建和全部测试**

运行：`npm test`

预期：构建成功且全部测试通过。

- [ ] **步骤 3：真实接口复算**

通过 `createMarketHandler` 请求 QQQ 和 021000 的 `days=365`，确认两者均返回 200、日期约为 365 天，QQQ 人民币收益接近 021000，且 QQQ 来源包含人民币汇率调整。
