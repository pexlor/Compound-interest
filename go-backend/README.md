# Go 本地后端

这是使用 SQLite（`DATA_DIR/fulibu.db`）的本地 Go API 服务，支持可选 MySQL 备份。SQLite 始终负责业务读写，无法打开时服务不会启动。

```bash
cd go-backend
go run ./cmd/server
curl http://localhost:8080/healthz
```

数据库仅支持当前结构：首次启动创建完整表结构，已有数据库必须包含当前字段，启动时不会补字段或导入旧库。服务不再提供 D1 导入命令。

网页和 CLI 的业务写请求统一要求 `Idempotency-Key`，修改已有记录必须提交 `version`。收入使用 PATCH；退休目标由明细合计；资产移除使用归档。前端开发服务器将 `/api` 代理到 `http://localhost:8080`。

## CLI 用 HTTP 接口

后端支持可撤销的用户级 Bearer 令牌、资产与规划的部分修改、预览、版本检查、防重复提交和归档。CLI 始终通过 HTTP 访问服务，数据库由后端管理。

完整请求契约与示例见 [CLI 接口文档](docs/cli-api.md)。CLI 程序与 Skill 将单独实现。

验证：`go test ./...`、`go test -race ./...`、`go vet ./...`。

年终奖领取日期、分期归属期权、到账现金流与退休预测的计算口径见 [收入到账与退休预测](docs/compensation.md)。

## 可选 MySQL 备份

不设置 `MYSQL_DSN` 时只使用 SQLite；`DATA_DIR` 默认是 `./data`，容器内为 `/data`。设置 `MYSQL_DSN` 后启用备份：

```bash
export DATA_DIR=./data
# 专用的空 MySQL 数据库；账户需要建表、索引、查询和增删改权限。
export MYSQL_DSN='backup_user:你的密码@tcp(127.0.0.1:3306)/fulibu_backup'
go run ./cmd/server
```

MySQL 8.4 是 Compose 提供的测试版本。备份账户应仅能访问自己的备份数据库。连接已有远程数据库时可在 DSN 中配置 TLS；DSN 使用 go-sql-driver/mysql 格式。应用固定 UTF-8 和严格 SQL 模式，禁止静默截断备份值。

首次启用时，在 SQLite 同一事务中安装变更捕获并将全部现有数据放入队列；之后同步全部 12 张应用表的新增、修改、删除、归档、版本号与定时任务变更。事务回滚和 `dryRun` 不会写入备份。两个库保留相同的 ID、金额、NULL、时间和版本。

业务成功以 SQLite 提交为准，MySQL 通常在后台很快追上。MySQL 连接/配置/写入失败时，服务仍正常使用 SQLite，队列保留并退避重试；恢复和应用重启后自动补写。目标端数据和检查点一起提交，因此中断后的重复发送不会再次执行业务或递增版本。

查询状态（使用与服务器相同的 DATA_DIR 和 MYSQL_DSN）：

```bash
go run ./cmd/backup-status
# Docker 镜像已包含此命令：
docker compose -p fulibu exec api fulibu-backup-status
```

返回 `enabled`（当前命令是否配置 MySQL）、`pending`（待同步事件数量）、`last_success` 和 `last_error`（UTC 时间和脱敏错误）。后两项是 SQLite 中保存的最近状态；`pending=0` 且没有错误表示已经追上。未开启过备份时，两项时间/错误为空。

临时清空 `MYSQL_DSN` 并重启可以停止发送；启用过备份的 SQLite 仍记录变更，恢复同一目标后补写。积压会占用 SQLite 磁盘，需留意 `pending` 和数据目录空间。备份会同步删除；保留历史版本仍需另外做离线备份。MySQL 不能自动接管主库，外部程序也不能同时向备份表写入。

目标必须是空的专用数据库，或来源标识匹配的现有备份。程序拒绝覆盖未归属的表、不同来源或不兼容结构。MySQL 索引字段有容量限制（如邮箱 2048 字节、币种 64 字节）；超限数据保留在队列中并明确报错，不能静默截断。

Docker 示例使用项目名 `fulibu`；已有部署务必使用原项目名（`docker compose ls` 可查看），避免切换 SQLite 数据卷。

### 更换备份目标

已经同步过数据后，更换新空目标必须显式重新全量初始化：先停止服务，再将 `MYSQL_DSN` 指向新空数据库，在同一 SQLite 数据目录执行：

```bash
go run ./cmd/backup-status --reinitialize
go run ./cmd/server
```

Docker 部署在项目根目录执行（先修改 `.env` 指向新的空目标）：

```bash
docker compose -p fulibu stop api
docker compose -p fulibu run --rm --no-deps --entrypoint fulibu-backup-status api --reinitialize
docker compose -p fulibu up -d api
```

命令先验证 MySQL 目标为空，再重建 SQLite 的备份快照与来源标识；不会修改 SQLite 业务数据或覆盖旧 MySQL 备份。数据目录锁会拒绝在服务仍运行时执行维护。同一 SQLite 目录只能由一个服务实例负责。

### 备份测试

普通 `go test ./...` 覆盖 SQLite 捕获、回滚与配置关闭等行为。真实 MySQL 测试需要额外设置：

```bash
MYSQL_TEST_DSN='test_user:测试密码@tcp(127.0.0.1:3306)/' go test ./... -count=1
MYSQL_TEST_DSN='test_user:测试密码@tcp(127.0.0.1:3306)/' go test -race ./... -count=1
go vet ./...
```

测试账户需要创建/删除临时测试数据库的权限；每个测试创建唯一的 `fulibu_backup_test_*` 数据库，结束时仅删除自己创建的库。未设置 `MYSQL_TEST_DSN` 时集成测试会明确跳过，不表示 MySQL 验证通过。
