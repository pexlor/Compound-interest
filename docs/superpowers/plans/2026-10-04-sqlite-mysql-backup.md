# SQLite 主库与 MySQL 备份 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** SQLite 必须作为业务主库，配置 MYSQL_DSN 后自动初始化并持续维护 MySQL 备份，故障恢复后可补写。

**Architecture:** 保持现有 *sql.DB 业务接口，通过 SQLite 触发器将完整行变更写入同事务 outbox。后台单线程将事件按序应用到 MySQL，MySQL 数据与检查点一起提交，再确认 SQLite 队列。没有 MYSQL_DSN 时仅使用 SQLite。

**Tech Stack:** Go 1.24、database/sql、现有 mattn/go-sqlite3、github.com/go-sql-driver/mysql v1.9.3、MySQL 8.4（可选 Docker profile）。

**Spec:** `docs/superpowers/specs/2026-10-04-sqlite-mysql-backup-design.md`

## Global Constraints

- SQLite 必须配置，始终是业务主库；MySQL 可选，只用于保存备份。
- MySQL 暂时不可用不阻止 SQLite 保存；恢复后自动补写。
- MySQL 不作为业务读取来源，也不自动接管 SQLite。
- 现有未提交工作保留；前端和 HTTP 请求契约不变。
- DSN、账户密码、备份行内容不得写入日志。
- 仅支持当前项目既有表结构；不会借备份功能自动升级不兼容的旧业务结构。
- 不安装 MySQL 版本触发器，不改变业务金额单位，不以 float64 解码整数。
- 同一 SQLite 数据目录由一个应用实例负责备份。

## Review Focus

- SQLite AFTER UPDATE 版本触发器的嵌套执行：最新备份版本不能退回，Task 1/3 验证。
- 备份关闭期间写入、重新开启：队列持久保留，Task 1/4 验证。
- 主库整数大于 2^53、NULL、中文和超长文本：无精度损失或静默截断，Task 2/3 验证。
- MySQL 成功提交后应用立即退出：恢复不得重复生成行或递增版本，Task 3 验证。
- 目标库已有陌生数据、来源不同或更换空目标：必须拒绝自动覆盖，Task 2/4 验证。

## 文件职责

- `go-backend/internal/database/backup_tables.go`：应用表清单、列顺序、主键及依赖顺序。
- `go-backend/internal/database/backup_capture.go`：SQLite 队列、触发器、全量初始化、读取与确认。
- `go-backend/internal/database/backup_mysql.go`：MySQL 连接、结构校验、来源检查与事件批次事务。
- `go-backend/internal/database/backup_worker.go`：后台重试、生命周期、状态与协调。
- 对应 `backup_*_test.go`：捕获、恢复及真实 MySQL 集成测试。
- `go-backend/cmd/server/main.go`：可选备份服务启动和停止。
- `go-backend/cmd/backup-status/main.go`：本地备份状态和显式重新初始化入口。
- `go-backend/go.mod`、`go.sum`：固定驱动依赖。
- `docker-compose.yml`、`.env.example`、`README.md`、`go-backend/README.md`：可选部署与使用说明。

### Task 1: SQLite 事务内变更捕获

**Files:** Create `internal/database/backup_tables.go`, `backup_capture.go`, `backup_capture_test.go`（均位于 go-backend）。现有 sqlite.go 仅在兼容所需时做最小修改。

**Interfaces:**
- `EnableBackup(ctx context.Context, db *sql.DB) (string, error)`：返回持久化来源标识，首次原子创建队列并全量播种，重复调用不重复播种。
- `BackupEvent{Sequence int64, Table string, Operation string, Key json.RawMessage, Row json.RawMessage}`。
- `ReadBackupEvents(ctx context.Context, db *sql.DB, limit int) ([]BackupEvent, error)`。
- `AckBackupEvents(ctx context.Context, db *sql.DB, through int64) error`。
- `ReinitializeBackup(ctx context.Context, db *sql.DB) (string, error)`：维护停机时使用，生成新来源标识并在单事务内重新播种。

- [x] 写 `TestBackupCaptureBootstrapAndChanges`：初始用户、资产和 NULL 字段进入队列；INSERT、UPDATE、归档、DELETE 和级联删除均有事件；再次启用不会复制初始队列。
- [x] 写 `TestBackupCaptureRollbackAndVersions`：业务事务回滚后队列数量不变；更新触发版本自增后按事件顺序还原出的最终版本与 SQLite 相同。测试两种触发器创建顺序。
- [x] 写 `TestBackupCapturePersistsWhenWorkerDisabled`：关闭数据库后重开，已有捕获仍覆盖写入；队列序号不复用；Ack 不删除指定序号之后的事件。
- [x] 运行 `cd go-backend && go test ./internal/database -run TestBackupCapture -count=1`，确认新测试因接口缺失失败。
- [x] 实现上述接口。内部表使用 `backup_` 前缀，序号 AUTOINCREMENT；来源使用 crypto/rand。固定清单按 users、sessions、api_tokens、mutation_requests、operation_logs、assets、income_settings、retirement_goal_items、exchange_rates、exchange_rate_history、market_returns、asset_history 排序。
- [x] 触发器生成 JSON 时读取实际现存完整行，DELETE 使用 OLD 主键；不能将版本自增前的 NEW 行放到更晚序号。触发器/全量播种创建与元数据写入必须共用同一事务，游标关闭后再执行下一操作以适应现有单连接 SQLite。
- [x] 重跑以上测试及现有 `go test ./internal/database ./internal/service ./internal/httpapi -count=1`，全部 PASS。
- [x] 仅提交本任务新增文件；如果修改现有脏文件，使用独立补丁暂存本任务差异，禁止整文件提交用户既有工作。

### Task 2: MySQL 结构与来源校验

**Files:** Create `internal/database/backup_mysql.go`, `backup_mysql_test.go`; modify `go.mod`, `go.sum`。

**Interfaces:**
- `OpenBackupMySQL(dsn string) (*sql.DB, error)`：只构造连接，设置有界连接/读写超时，不在服务器启动主路径阻塞 Ping。
- `InitializeBackupTarget(ctx context.Context, target *sql.DB, sourceID string, requireExisting bool) error`。
- `ApplyBackupEvents(ctx context.Context, target *sql.DB, sourceID string, events []BackupEvent) (int64, error)`（Task 3 实现）。

- [x] 写 `TestBackupTargetSourceProtection`：空目标创建全部表；同来源重开成功；不同来源、无元数据但已有业务行均报错且原数据不变；来源已经启用且本地确认过事件时，新空目标报需要显式重新初始化。
- [x] 写 `TestBackupTargetSchemaAndDSN`：12 表字段和约束均存在；MySQL 无版本触发器；错误 DSN 返回脱敏信息；数据表已有不兼容结构时不改业务表。
- [x] 用 `MYSQL_TEST_DSN` 指定专用空测试数据库运行 `go test ./internal/database -run TestBackupTarget -count=1`；未配置时测试明确 Skip，不能据此宣称 MySQL 验证通过。
- [x] 添加固定依赖 `github.com/go-sql-driver/mysql@v1.9.3`，不升级 Go 或 SQLite 驱动。
- [x] 为 12 表逐一提供 MySQL DDL（不要将 SQLite schema 字符串简单替换）。ID/金额/版本使用 signed BIGINT，小数 DOUBLE，完整行时间保留文本值，正文 LONGTEXT，标识使用保留大小写及尾部字符区别的二进制比较类型。
- [x] 根据 SQLite 字段与现有接口实际取值确定索引键宽度；不静默截断超过 MySQL 可表示范围的现有值。使用 InnoDB，所有 SQL 标识仅来自固定清单，字段名包含 trigger 时正确引用。
- [x] 初始化前先检查所有表、来源与数据，拒绝冲突目标；DDL 不假设可以事务回滚。已应用序号初始为 0，首次 MySQL 事务推进检查点时绑定来源标识。
- [x] 运行上述真实 MySQL 测试，全部 PASS；记录实际服务器版本。
- [x] 仅提交本任务新增文件和自身依赖差异。

### Task 3: 顺序应用、幂等重放与确认

**Files:** Modify `internal/database/backup_mysql.go`; create `backup_replication_test.go`。

**Interfaces:** Consumes Task 1/2；实现 `ApplyBackupEvents`，返回目标已提交的检查点。队列确认始终由调用者在目标提交后执行。

- [x] 写 `TestBackupReplicationAllTables`：填充全部 12 表，备份后按列比较原始值；含 9007199254740993、NULL、空串、中文、大 JSON 正文、case-distinct 文本键；记录全部表实际对比结果。
- [x] 写 `TestBackupReplicationReplayAndFailure`：同批重复应用最终不变；模拟提交成功后未 Ack 重启，按检查点继续；批次中间失败目标数据及检查点一起回滚；不能越过失败序号。
- [x] 写 `TestBackupReplicationForeignKeysAndVersions`：包含父子新增、级联删除、资产与收入版本自增及幂等/审计行；两库最终完全一致且 ID 不重新生成。
- [x] 运行 `go test ./internal/database -run TestBackupReplication -count=1`，确认失败原因是批次应用未实现。
- [x] 实现事件解析：json.Decoder.UseNumber，按列类型将整数转为 int64；允许表/操作/键必须通过清单校验。
- [x] 目标事务锁定唯一来源检查点行，再读取检查点；忽略已提交序号；有序对主键执行完整行 UPSERT 或 DELETE；严格检查其它唯一键冲突，不能因 ON DUPLICATE KEY 更新了错误主键而推进检查点。
- [x] 备份不再次执行应用业务逻辑，不重新生成时间、ID 或版本；更新数据和检查点一起 Commit，只有成功 Commit 后返回新的序号。
- [x] 运行所有真实 MySQL 复制测试与 SQLite 捕获测试，全部 PASS。
- [x] 仅提交本任务差异。

### Task 4: 工作线程、状态及服务接入

**Files:** Create `internal/database/backup_worker.go`, `backup_worker_test.go`, `cmd/backup-status/main.go`; modify `cmd/server/main.go`。

**Interfaces:**
- `BackupStatus{Enabled bool, Pending int64, LastSuccess string, LastError string}`，不包含业务行或凭据。
- `StartMySQLBackup(ctx context.Context, primary *sql.DB, dsn string) (*Backup, error)`；SQLite 捕获初始化失败为启动错误，MySQL 故障进入后台重试。
- `(*Backup).Close() error`、`(*Backup).Status(ctx context.Context) (BackupStatus, error)`。
- `RunBackupBatch(ctx context.Context, primary, target *sql.DB, sourceID string) error`：批次读取、应用、确认，用于工作线程与恢复测试。

- [x] 写 `TestBackupWorkerDisabledAndUnavailable`：空 DSN 不创建队列、不连接目标；错误 DSN/断网不影响 SQLite 写入；日志及状态不泄漏测试密码；SQLite 自身故障返回错误。
- [x] 写 `TestBackupWorkerRecoveryAndShutdown`：目标失联期间队列增加，恢复后收敛；进程重启继续；取消 context 后有界停止；移除 DSN 后队列持续捕获，重新加入 DSN 补齐。
- [x] 写 `TestBackupReinitializeNewEmptyTarget`：旧目标已确认过事件后拒绝直接切空目标；显式重新初始化生成新来源与全量队列，新空目标成功接收，拒绝覆盖非空旧目标。
- [x] 运行 `go test ./internal/database -run 'TestBackupWorker|TestBackupReinitialize' -count=1`，确认缺失实现失败。
- [x] 实现一个工作线程：每批最多 100 条，空队列 1 秒轮询，失败 1 秒起指数退避至最多 30 秒；所有等待可被 context 取消，连接与单批操作超时有界；备份错误日志只记录脱敏分类。
- [x] SQLite outbox 状态写入持久化元数据（包括曾确认的最高序号），新空目标来源保护由此判定；新目标重新初始化时必须先停止服务，命令验证空目标后调用 ReinitializeBackup，错误不改 SQLite 队列。
- [x] 服务入口先 Open SQLite，再按 MYSQL_DSN 启用备份，然后启用原 API/定时任务；停止备份后再关闭 SQLite，不更改 httpapi.New 或业务读写依赖。
- [x] 提供本地命令 `go run ./cmd/backup-status` 输出持久化队列状态；`--reinitialize` 明确验证服务停机/SQLite 写锁与空 MySQL 目标后全量重建；状态缺少运行时故障记录时说明其为上次持久化状态。
- [x] 重跑上述测试并运行 `go test -race ./... -count=1`、`go vet ./...`，全部通过。
- [x] 仅提交本任务文件和入口自身差异。

### Task 5: 可选部署、端到端验证和文档

**Files:** Modify `docker-compose.yml`, `README.md`, `go-backend/README.md`; create `.env.example`。Dockerfile 保留 SQLite CGO 和持久数据卷。

- [x] 在 api 环境添加 `MYSQL_DSN: ${MYSQL_DSN:-}`；添加 `mysql-backup` profile、mysql:8.4、独立持久卷、健康检查和必填环境密码，不给已有默认启动增加 MySQL 依赖。
- [x] `.env.example` 分别说明 SQLite 单库、已有 MySQL、Compose MySQL；示例不要成为生产默认密码。文档给出 `docker compose --profile mysql-backup up --build -d`、状态查询及停机后更换空备份目标的步骤。
- [x] 文档明确首次全量、异步延迟、失败补写、关闭后积压、源库持续可用要求、删除同步、队列磁盘增长及不支持自动接管；解释 MySQL 不是历史离线备份。
- [x] 运行 `docker compose config` 和 `docker compose --profile mysql-backup config`，未配置 profile 时不会运行 MySQL；不在输出中泄漏真实环境密码。
- [x] 启动专用测试 MySQL，运行 `MYSQL_TEST_DSN=... go test ./... -count=1`；真实服务注册用户后创建/修改资产和计划，核对 MySQL 对应 ID、版本、时间、幂等及审计行。
- [x] 停止 MySQL，继续完成业务写入，重启应用再恢复 MySQL，核对队列清空和 12 张表最终一致；删除/归档与 dryRun 分别核对。
- [x] 最终运行 `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、`git diff --check`；检查差异仅包含本次工作。
- [x] 若无法启动真实 MySQL，交付保留测试与运行指令并明确列出未验证项目，不将 Skip 当作成功。
- [x] 仅提交部署/文档自身差异；最终报告修改、验证和限制，不提交用户既有未提交工作。

## 当前基线与执行方式审核

2026-10-04 已运行 `go test ./...`，现有测试通过。Docker 守护进程当前未运行，真实 MySQL 集成测试尚不能执行；实施时可启动本机 Docker 再验证。
推荐在当前会话直接实施：各步骤共享捕获事件与检查点接口，顺序实现容易持续核对既有脏工作。
用户审核本计划并选择执行方式后进入实施；尚未修改产品代码。

## 实施记录（2026-10-04）

代码已在当前工作目录完成，保留原有未提交修改，未合并、推送或提交用户的既有工作。采用 SQLite 触发器 outbox + MySQL 事务检查点方案，另增加 data_lock.go、backup_replication.go、backup_mysql_schema.go 以分离职责。

完整 Go 测试、真实 MySQL 8.4.11 集成测试、竞态检查和 vet 均通过。真实 HTTP 验证涵盖注册/令牌/资产/收入/退休目标、dryRun、停止 MySQL 期间写入、幂等重试、重启服务、恢复后自动补写、删除/归档，以及全部 12 张表逐列一致；显式 CLI 更换目标及维护锁也通过。

独立审查提出的检查点倒退、初始化中断恢复、结构校验、Compose 项目名问题均已修复并验证。保留 Compose 的原命名行为，中文目录示例显式指定 -p，已有部署使用原项目名以保留 SQLite 卷。

Compose 配置验证通过；默认关闭 MySQL profile。容器镜像构建未完成：本机配置的镜像源下载 Debian 层返回 HTTP 403，容器内 proxy.golang.org 下载返回 EOF；主机二进制编译和实际 MySQL 验证已完成。未修改用户 Docker 镜像源设置。

各任务的提交步骤以保留当前未提交工作为准；已完成代码、验证与文档，尚未发布。
