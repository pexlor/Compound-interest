# CLI HTTP 接口

CLI 只能通过 HTTP 访问本服务。默认部署入口是 `http://localhost:3000`，由现有 Nginx 转发 `/api` 到 Go 后端。本文描述后端接口；CLI 二进制和 AI Skill 尚未交付。

## 认证

先通过 `POST /api/auth/login` 登录，保存响应的 `fulibu_session` Cookie；登录请求为 `{ "email": "…", "password": "…" }`。Cookie 登录已有网页界面也可使用。

仅网页登录会话可管理令牌：

| 方法与路径 | 请求 | 响应 |
|---|---|---|
| `POST /api/auth/tokens` | `name`、`scope: "read"/"write"`、可选 `expiresInDays`（1–365，默认 30） | 201；`id`、`name`、`scope`、`expiresAt`（Unix 秒）和一次性明文 `token` |
| `GET /api/auth/tokens` | 无 | `tokens` 元数据列表，不含令牌或摘要 |
| `DELETE /api/auth/tokens?id=…` | 令牌 ID | `{ "ok": true }`；当前用户下不存在返回 404 |

随后所有 CLI 请求使用 `Authorization: Bearer <token>`。read 令牌允许查询，write 令牌允许查询和写入。Bearer 不允许创建/管理令牌或删除目标明细，也不允许访问会刷新持仓并写历史的 `/api/dashboard`；请使用 `/api/portfolio/summary` 查询总览。

无效、撤销或过期的 Bearer 返回 401，不会回退到同请求的 Cookie。

## 单位与日期

- 写入金额参数使用主货币单位：`amount: 100` 表示 100 元/美元等。
- 资产 `amount`、`investment_amount`、工资/储蓄/奖金及退休目标响应金额均为整数分；新增响应标注 `moneyUnit: "minor"`。期权每份 `strikePrice`、`marketPrice` 在请求和响应中均为所选币种的主单位，详见 [收入到账与退休预测](compensation.md)。
- 总览使用 `totalCnyMinor` / `valueCnyMinor`，单位是人民币分；`share` 为 0–1 的比例。
- `annualRate` 写入值为百分数，`2` 表示 2%；这是预测假设，不是真实个人投资收益。
- 日期筛选采用 `YYYY-MM-DD`，资产历史为 Asia/Shanghai 的业务日期。
- 总览的 `valuationBasis: "stored_assets"` 表示当前已保存市值，不承诺实时行情；`generatedAt` 仅为响应生成时间。行情日期和汇率日期需分别解释。

## 查询

| 方法与路径 | 参数 | 返回 |
|---|---|---|
| `GET /api/assets` | 可选 `name`（不区分大小写包含匹配）、`code`（不区分大小写精确匹配）、`category`、`includeArchived=true` | `assets` 列表，默认排除归档 |
| `GET /api/assets?id=…` | 正整数 ID，可选 `includeArchived=true` | `asset`，默认归档记录视为不存在 |
| `GET /api/portfolio/summary` | 无 | 总额、类别/币种汇总、assetCount、汇率日期与 stale；不会写资产或快照 |
| `GET /api/history` | 可选 `from`、`to`、`limit`（默认 365，最多 3650） | 范围内最近 limit 条，以日期升序返回；范围包含两端 |
| `GET /api/income` | 无 | `income`，包含 version |
| `GET /api/retirement` | 无 | 退休目标汇总和含各自 version 的 items |
| `GET /api/retirement/items` | 无 | `items` 目标明细 |
| `GET /api/market` | 指定 `code`、`category`、`days`；不指定 code 查询当前证券持仓 | 复用现有行情格式，含来源、日期、可能的 stale / errors |
| `GET /api/exchange-rates` | 可选 `refresh=1` | 复用现有 rates、date、stale；此查询可能更新公共汇率缓存 |

总览缺少所需币种汇率时 `complete: false`、`totalCnyMinor: null`、`missingRates` 列出缺失币种。无法完整换算的分组金额与占比为 null；完整总额不可用时所有占比为 null。`rateDates` 保留各币种日期，`rateDate` 是本次涉及外币汇率中最早的日期。仅人民币资产不需要外币汇率。

资产总额变化可能包含入金、出金和人工修改。当前系统没有完整交易流水，不能将历史总额差额直接解释为投资盈亏。

## 写入契约

网页 Cookie 和 Bearer 实际业务写请求必须带 `Idempotency-Key`（非空、最多 128 字节）。同用户、方法、路径、编号和请求内容重放原成功响应，不重复修改；编号相同但内容不同返回 409。请求指纹包含查询参数，因此带 `dryRun=false` 和不带参数被视为不同请求。

修改已有记录必须提交查询取得的整数 `version`；income 尚未建立时返回的 version 是 0。记录发生变化后旧 version 返回 409。网页 Cookie 和 Bearer 业务写请求使用相同的编号与版本要求。退休总目标不单独保存，也没有独立版本；各明细持有自己的 version。

写接口支持 `?dryRun=true`，Bearer 预览可省略编号，但修改预览仍需 version。预览经过参数及版本校验，返回预测的 before/after 和 `dryRun: true`，事务回滚，不改资产、历史、版本、日志或幂等记录。创建预览中的 ID 和版本是暂定值，不能用来定位已保存记录。

旧收入 PUT、单一退休目标 PUT、资产永久删除 DELETE 和 `/api/retirement?id=…` 删除请求均不再提供。资产使用 `POST /api/assets/archive` 移除。

请求必须是单一 JSON 对象；拒绝未知字段、尾随 JSON、null 字段和空修改。非证券资产没有 quantity 时省略此字段。修改请求中未传入的字段保持原值。每次成功实际业务写入都记录服务端操作日志。

| 方法与路径 | 请求字段 | 返回与行为 |
|---|---|---|
| `POST /api/assets` | 必填 name、category、amount；currency 默认 CNY；可选 code、quantity、annualRate、note、investmentStrategy、investmentAmount | 201；asset、before/after、snapshot、dryRun |
| `PATCH /api/assets` | id、version；可选 name、note、amount、quantity、currency、annualRate、investmentStrategy、investmentAmount | 200；before/after，更新后的完整资产在 after 中 |
| `POST /api/assets/archive` | id、version | 200；保留原资产，记录 archived_at；后续当前总览、行情刷新、退休计算及快照排除它 |
| `PATCH /api/income` | version；可选 monthlySalary、monthlySavings、annualBonus、bonusSettings、options | 200；只修改指定字段，零值合法 |
| `POST /api/retirement` | name、amount；可选 category、currency（默认 CNY） | 201；新增目标明细，返回退休汇总和 before/after |
| `PATCH /api/retirement/items` | id、version；可选 name、category、amount、currency | 200；修改指定明细并返回汇总和 before/after |
| `DELETE /api/retirement/items` | JSON 对象：id、version | 200；仅网页会话，删除指定明细；支持编号重放和 dryRun |
| `POST /api/valuations/refresh` | `{}` | 200；updated、errors、complete、snapshotRecorded、before/after、dryRun |
| `POST /api/history` | `{}` | 200；按当前已保存市值记录当天历史，不抓取行情；支持预览及防重复 |

资产类别为 stock、fund、money、deposit、housing、fixed。stock/fund 必须带非空证券代码与大于零的 quantity。金额大于零，至少 0.01；收入字段可为零。支持的币种为 CNY、USD、HKD、EUR、JPY、GBP、SGD、AUD、CAD、CHF。

基金定投策略为 none、daily、weekly、monthly、yearly；非 none 仅适用于 fund，必须具有有效 investmentAmount。关闭策略会清除计划金额。保存定投策略不代表已经扣款或买入。

估值刷新获取行情后再提交事务。如果期间持仓版本改变或归档，返回 version_conflict，不应用那次刷新。部分行情失败时保留失败资产原估值，响应 complete 为 false，并在 errors 逐项说明；成功项目仍可写入。snapshotRecorded 为 false 时不能宣称历史已记录，通常是缺失汇率。重试已成功请求直接返回原响应，不重新获取行情。

每天只有一个资产总额快照：实际资产变更/归档会更新当日快照，过去日期的历史保持原值。只有修改工资或退休计划不会改变资产历史。

## 示例

下例中的令牌由本地环境提供；不要将真实令牌写入文档或日志。

```sh
curl "$FULIBU_BASE_URL/api/assets" \
  -H "Authorization: Bearer $FULIBU_TOKEN"

curl -X PATCH "$FULIBU_BASE_URL/api/assets?dryRun=true" \
  -H "Authorization: Bearer $FULIBU_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":1,"version":3,"note":"长期持有"}'

curl -X PATCH "$FULIBU_BASE_URL/api/assets" \
  -H "Authorization: Bearer $FULIBU_TOKEN" \
  -H 'Idempotency-Key: edit-asset-20261004-001' \
  -H 'Content-Type: application/json' \
  -d '{"id":1,"version":3,"note":"长期持有"}'
```

网络超时不代表写入未成功。保留原编号、原版本和原请求内容重试；若重新获取版本并换编号，可能构成新操作。

## 错误

新增及扩展接口返回 `{ "code": "…", "error": "可读说明" }`。

| HTTP | code | 处理 |
|---|---|---|
| 400 | invalid_request | 修正字段、类型、编号或 version |
| 401 | unauthorized | 重新登录或取得有效令牌 |
| 403 | forbidden | 使用正确权限或网页会话 |
| 404 | not_found | 核对当前用户下的记录 ID |
| 409 | version_conflict | 重新查询，核对差异后再提交 |
| 409 | idempotency_conflict | 不要用已有编号提交不同内容 |
| 405 | method_not_allowed | 检查 HTTP 方法 |
| 500 | internal_error | 服务端失败，不推断写入成功 |

部分既有认证、行情及汇率错误仍采用 `{ "error": "…" }`，客户端需要按 HTTP 状态兜底。
