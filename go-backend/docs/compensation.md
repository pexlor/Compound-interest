# 收入到账与退休预测

年终奖与期权作为收入计划保存，不会修改当前资产余额或资产历史。预测的业务日期为 Asia/Shanghai；截至当日的已到账收入应由当前资产余额体现，未来计划只计入次日起的到账事件。

## 年终奖

`annualBonus` 写入单位为人民币元，`income.annual_bonus` 响应单位为分。该金额表示完整年度税前奖金；金额大于零时必须提供 `bonusSettings`：

```json
{
  "version": 0,
  "monthlySalary": 20000,
  "monthlySavings": 5000,
  "annualBonus": 100000,
  "bonusSettings": {
    "workStartDate": "2026-07-01",
    "payMonth": 2,
    "payDay": 28,
    "yearOffset": 1
  }
}
```

`yearOffset: 1` 表示领取上一年度奖金，`0` 表示领取当年度奖金。预测假设持续在职，按所属年度入职日（含）至年末的在职天数 / 当年总天数计算工作比例，满年度为 1；再乘完整金额及固定 90%，四舍五入到分。允许设置 2 月 29 日，平年在 2 月 28 日发放。设金额为 0 可停止预测奖金。

奖金预测只使用完整日期设置生成的到账事件，不再按预测起点每满一年生成默认奖金。

## 期权

`options` 是完整授予列表，提交 `[]` 可移除全部期权计划。每份含名称、币种、授予总数量、每份行权价、每份预估股价、税率百分数及归属批次：

```json
{
  "options": [{
    "name": "2026 年授予",
    "currency": "USD",
    "quantity": 1000,
    "strikePrice": 10,
    "marketPrice": 30,
    "taxRate": 20,
    "batches": [
      { "vestDate": "2027-01-01", "quantity": 250, "cashMode": "immediate", "cashDate": "" },
      { "vestDate": "2028-01-01", "quantity": 250, "cashMode": "date", "cashDate": "2028-04-15" },
      { "vestDate": "2029-01-01", "quantity": 250, "cashMode": "hold", "cashDate": "" }
    ]
  }]
}
```

数量可为小数；`strikePrice` 和 `marketPrice` 在请求及响应中均使用所选币种的主单位（元、美元等），区别于其他收入响应的整数分。每批预计净额为数量 × max（预估股价 − 行权价，0）×（1 − 税率 / 100），四舍五入到分。

`immediate` 在归属日到账；`date` 在指定日到账且不能早于归属日；`hold` 不计入现金预测。未安排的授予数量保留但不计收入；归属批次数量合计不得超过授予总数量。未达价内时按净收益为零处理，价格及税率为用户提供的情景假设。

网页可按年、季度或月生成明细，首期支持集中归属，其余数量均分到后续各期；月末日期在短月份取该月最后一天。生成会替换当前这一份授予的明细，可继续手动调整。

## 接口与预测

收入接口使用 `GET /api/income` 和 `PATCH /api/income`，请求字段包含 `bonusSettings`、`options`。PATCH 未提供字段保持原值。网页与 Bearer 写请求统一使用 version、Idempotency-Key 和 dryRun 契约。

响应 `income` 新增 `bonus_settings`、`options`、`forecast_as_of` 和未来 100 年的 `cashflows`。cashflow 的 `amount` 是所选币种整数分，`date` 为到账日期，`kind` 为 bonus 或 option，奖金事件另含 `earning_year`、`work_ratio`。

退休计算每月最后一天计入预计储蓄，奖金和期权在各自到账日计入，新增资金从到账后参与复利。返回 `projected_date`（达成日期）及 `projected_years`；100 年内未达成时年份为 null。现金流币种缺少汇率时不输出不完整的退休日期，而返回 `missing_currencies`。资产曲线同样按到账日期计入资金，外币按当前汇率不变估算。

保存收入计划后网页重新获取退休预测。预测不会自动将实际已发奖金或已变现期权新增成资产，需在资产余额中维护实际资金。

## 验证

```sh
# go-backend 目录
 go test ./...
# frontend 目录，Node 22.6+，测试无需第三方测试框架
 npm test
 npm run build
```
