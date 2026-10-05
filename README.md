# 资产星图

前后端分离的本地应用：React/Vite 前端与 Go/SQLite API 独立运行。没有 Next.js、Cloudflare Worker、D1 或 R2 运行时依赖。

新部署使用项目名 `fulibu`（中文目录需要显式项目名）。已有部署升级时，以下命令必须将 `fulibu` 替换为原项目名，继续使用原来的 SQLite 数据卷；可用 `docker compose ls` 查看现有名称。

```bash
docker compose -p fulibu up --build -d
```

打开 <http://localhost:3000>。数据保存在 Docker 卷 `fulibu-data`；浏览器的 `/api` 请求由 Nginx 转发至 Go 容器。

SQLite 始终是必需的主库。可配置 `MYSQL_DSN` 将全部应用数据异步备份到 MySQL：首次启用复制现有数据，之后同步增删改；MySQL 不可用时 SQLite 正常保存，恢复后自动补写。

可选本地 MySQL：复制 `.env.example` 为 `.env`，填写两个不同的随机密码，并设置与 `MYSQL_BACKUP_PASSWORD` 一致的连接串：

```dotenv
MYSQL_DSN=fulibu_backup:你的备份密码@tcp(mysql:3306)/fulibu_backup
```

```bash
docker compose -p fulibu --profile mysql-backup up --build -d
docker compose -p fulibu exec api fulibu-backup-status
```

仅使用 SQLite 时保留 `MYSQL_DSN` 为空，运行原来的启动命令即可。连接已有 MySQL、同步状态、目标库更换和测试说明见 [后端文档](go-backend/README.md#可选-mysql-备份)。

### 稳健资产与退休预测

预测由 Go 后端的同一引擎生成，首页金额、年度 P10/P50/P90 范围、单项本币预测和退休首次达标中位日期使用同一情景。历史年化只供查看，不再直接作为未来固定收益。现有资产逐项计算收益；未来储蓄、奖金和期权按到账日期只计本金。

- 股票按市场选择宽基 ETF；普通基金、债券基金和 ETF 可在“资产预测基准”中明确选择大陆股票、美股、港股或人民币国债基准。无法判断的基金不自动分类。
- 月收益使用本币含分红再投资总收益，基准至少60个完整月；资产不足36个月时采用基准收益及波动。长历史超额月对数收益按 `min(0.5,n/(n+60))` 折减，参数为尚未校准的初始设定。
- 采用5000条路径、连续3个月共同历史片段和固定随机种子，资产与汇率联合抽样。外汇月对数收益中心设为0，不作方向性汇率判断。共同历史至少有连续36个月及12个有效三月片段；不跨数据缺口拼接。稀缺历史会显示不可用原因。
- 通胀默认2%，只是可调整的情景假设；退休目标金额按今日人民币购买力输入。固定资产始终不计退休可用资产，公积金可选择是否计入。退休输出用于积累目标，不包含退休后的提款可持续性。
- 存款、公积金按输入利率保持不变（当前没有到期与续存字段）；货币基金采用近90天每万份收益平均值，少于30天时明确提示暂用输入利率。
- 当前余额使用账本中保存的估值。读取预测不会改写资产，历史行情及基准通过已有缓存后台补齐。情景选择按用户保存在本机浏览器。

读取接口：`GET /api/forecast?years=10&inflation=2&includeRestricted=false&benchmarks={"12":"cn_bond"}`。`benchmarks` 必须 URL 编码；键是当前用户资产 ID，值为 `cn_equity/us_equity/hk_equity/cn_bond`。金额响应为分，期限允许1–30年。`GET /api/retirement` 支持相同情景参数；目标写接口返回当前进度，完整模拟在下一次预测读取中计算。

页面“运行历史回测”或接口添加 `backtest=1`，可查看1/3/5年回测误差及区间覆盖率。回测固定当前持仓金额权重，排除收入现金流，训练只用历史时点之前的样本。窗口重叠、幸存者偏差、供应商历史修订和参数估计不确定性仍存在，不能把模拟区间解释为收益保证。缓存不足时不报告误差百分比。含货币基金的组合暂不生成回测误差，因为当前缓存无法核验历史期间实际收益；存款、公积金利率在回测中仍属于固定输入假设。

也可从已有本地数据库只读运行，不访问网络：

```sh
go -C go-backend run ./cmd/forecast-backtest -db /absolute/path/fulibu.db -user 1 -benchmarks '{"12":"cn_bond"}'
```

本功能不自动部署到服务器。上线后首次补齐基准可能需要等待；范围、误差和覆盖率必须根据实际回测样本解释，不能仅凭测试通过宣称预测精度提高。
