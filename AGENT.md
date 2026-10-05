# 项目协作规范

本规范适用于本项目中的开发与维护工作。

## 提交与合并

- 每完成一个需求，都必须进行一次 Git commit，不得将多个已完成的需求积攒到同一次提交。
- 提交前检查本次变更，并执行与需求相关的验证，确保需求已完成。
- 每个需求的提交都必须合并到 `main` 分支；合并完成后，该需求才算交付完成。
- 提交说明应清楚描述本次完成的需求。

## 中文注释

- 所有函数和结构体都必须包含中文注释。
- 函数注释应说明用途，并按需说明参数、返回值及关键行为。
- 结构体注释应说明用途，含义不直观的字段也应补充中文注释。
- 新增或修改函数、结构体时，必须同步补充或更新中文注释，确保注释与实现一致。


## 当前服务器部署

以下是现有服务器的实际部署方式，维护此服务时应沿用。与仓库的普通 Docker Compose 部署不同，这台服务器直接运行原生程序，不运行 Nginx 或前后端容器。

### 环境与目录

| 项目 | 当前配置 |
|---|---|
| SSH | `root@192.168.2.1`，使用 SSH 密钥或交互输入密码 |
| 系统 / 架构 | OpenWrt / `aarch64`，构建目标 `linux/arm64` |
| 页面及 API | `http://192.168.2.1:3000`，同一个 Go 进程提供 |
| 服务管理 | `/etc/init.d/fulibu`，OpenWrt `procd` |
| 运行身份 | 用户 `nobody`（UID 65534），进程组 `fulibu`（GID 1001） |
| 当前版本入口 | `/opt/fulibu/current`，指向版本目录的符号链接 |
| 版本目录 | `/opt/fulibu/releases/<release-id>/bin/` 与 `static/` |
| SQLite | `/opt/fulibu/data/fulibu.db`，包含可能存在的 WAL / SHM 文件 |
| 离线备份 | `/opt/fulibu/backups/<release-id>/data/` |

`procd` 启动命令为 `/opt/fulibu/current/bin/fulibu`，环境变量如下：

```text
DATA_DIR=/opt/fulibu/data
STATIC_DIR=/opt/fulibu/current/static
PORT=3000
SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

- 保留现有 init 脚本、环境变量和运行身份，不要用 root 运行应用，也不要将进程组改成 `nogroup`。`fulibu` 组与现有 OpenClash 网络路由配置有关，改组可能造成外部行情和汇率请求失败。数据目录目前由 `nobody` 持有；目录所属组与进程运行组不是同一个概念。
- 当前未启用 MySQL 备份；以后若服务器已有 `MYSQL_DSN` 配置，部署时保留，不在日志、文档或提交中输出其值。
- 不将服务器密码、网页登录密码、Cookie 或 API 令牌写入仓库、脚本或命令行参数。不要重新初始化生产数据库，不要覆盖 `/opt/fulibu/data`。
- 页面、行情及日任务均由服务提供；日任务使用 `Asia/Shanghai`。保持现有开机自启和调度配置。

### 1. 验证并构建

先在仓库根目录完成本次变更所需的测试，提交并合并到 `main`，确认没有遗漏工作区改动。后端常用验证：

```sh
go -C go-backend test ./...
go -C go-backend vet ./...
# 修改并发、缓存或调度时还需运行：
go -C go-backend test -race ./...
```

使用已纳入版本控制的 `deploy/openwrt.Dockerfile` 构建发布包。它会运行前端测试与生产构建，并生成两个静态 ARM64 程序：`fulibu` 和 `fulibu-backup-status`。后端使用 CGO SQLite，不能简单改成 `CGO_ENABLED=0`；使用静态链接避免服务器缺少 glibc。

以下命令在本地仓库根目录执行。Docker 需支持 `linux/arm64`；不同架构的构建机可能需要 Docker 的仿真支持。示例使用 `shasum`（macOS），Linux 可改用 `sha256sum`。

```sh
set -e
FULIBU_RELEASE_ID="$(TZ=Asia/Shanghai date +%Y%m%d-%H%M%S)-$(git rev-parse --short HEAD)"
FULIBU_IMAGE="fulibu-openwrt:$FULIBU_RELEASE_ID"
mkdir -p outputs
# 必须使用新的空目录：docker cp 到已有同名目录可能产生 bin/bin、static/static。
FULIBU_STAGE="$(mktemp -d "$PWD/outputs/fulibu-release.XXXXXX")"
docker build --platform linux/arm64 -f deploy/openwrt.Dockerfile -t "$FULIBU_IMAGE" .
FULIBU_CONTAINER="$(docker create "$FULIBU_IMAGE" /bin/fulibu)"
docker cp "$FULIBU_CONTAINER:/bin" "$FULIBU_STAGE/bin"
docker cp "$FULIBU_CONTAINER:/static" "$FULIBU_STAGE/static"
docker rm "$FULIBU_CONTAINER"
FULIBU_ARCHIVE="$FULIBU_STAGE/$FULIBU_RELEASE_ID.tgz"
# macOS 排除扩展属性，避免 OpenWrt tar 报 LIBARCHIVE.xattr 警告。
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$FULIBU_ARCHIVE" -C "$FULIBU_STAGE" bin static
# Linux tar 不支持 --no-xattrs 时，去掉该选项即可。
FULIBU_ARCHIVE_SHA="$(shasum -a 256 "$FULIBU_ARCHIVE" | awk '{print $1}')"
FULIBU_BINARY_SHA="$(shasum -a 256 "$FULIBU_STAGE/bin/fulibu" | awk '{print $1}')"
```

`outputs/` 只放本地构建产物，不提交程序、数据库和发布压缩包。生成的 scratch Docker 镜像只用于提取文件，服务器直接运行其中的程序。

### 2. 完整上传，再校验和切换

以下命令延续上一步变量。在同一个本地 shell 中按顺序执行，必须等 `scp` 返回成功，才执行远端解包；不能在上传还在运行时启动解包。OpenWrt 的 scp 服务可能不支持 SFTP，因此使用 `scp -O`。

```sh
set -e
scp -O "$FULIBU_ARCHIVE" "root@192.168.2.1:/opt/fulibu/$FULIBU_RELEASE_ID.tgz"
ssh root@192.168.2.1 sh -s -- "$FULIBU_RELEASE_ID" "$FULIBU_ARCHIVE_SHA" "$FULIBU_BINARY_SHA" <<'REMOTE'
set -eu
release_id="$1"
archive_sha="$2"
binary_sha="$3"
case "$release_id" in ''|*[!A-Za-z0-9._-]*) exit 1 ;; esac
base=/opt/fulibu
archive="$base/$release_id.tgz"
release="$base/releases/$release_id"
backup="$base/backups/$release_id"
previous="$(readlink "$base/current")"
test -n "$previous"
test -x "$previous/bin/fulibu"
test ! -e "$release"
test ! -e "$backup"
test "$(sha256sum "$archive" | cut -d ' ' -f 1)" = "$archive_sha"
mkdir -p "$release" "$backup"
tar -xzf "$archive" -C "$release"
chmod 755 "$release/bin/fulibu" "$release/bin/fulibu-backup-status"
test -s "$release/static/index.html"
test "$(sha256sum "$release/bin/fulibu" | cut -d ' ' -f 1)" = "$binary_sha"
printf '%s\n' "$previous" > "$backup/previous-release.txt"

/etc/init.d/fulibu stop
# procd 停服是异步的。SQLite WAL / SHM 仍在变化时不能直接 cp。
for stop_wait in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if ! pidof fulibu >/dev/null; then break; fi
    sleep 1
done
if pidof fulibu >/dev/null; then
    /etc/init.d/fulibu start
    printf '%s\n' '进程未退出，停止部署，不复制数据库。' >&2
    exit 1
fi
if ! cp -a "$base/data" "$backup/data"; then
    /etc/init.d/fulibu start
    printf '%s\n' '备份失败，恢复原版本服务。' >&2
    exit 1
fi

# 链接切换失败时恢复当前入口的服务；发布目录与数据目录保持分离。
trap '/etc/init.d/fulibu start' EXIT
ln -s "$release" "$base/current.next"
mv -Tf "$base/current.next" "$base/current"
/etc/init.d/fulibu start
trap - EXIT
readlink "$base/current"
REMOTE
```

版本目录不要重复使用，也不要覆盖正在运行的版本。若存在上次失败留下的 `current.next`，先检查它的目标再处理，不能跳过备份直接切换。以上切换完成后仍需执行验收；不能仅凭 init 命令返回成功就宣布部署成功。

### 3. 验收

```sh
curl -fsS --max-time 10 http://192.168.2.1:3000/healthz
ssh root@192.168.2.1 'readlink /opt/fulibu/current; pidof fulibu; logread -e fulibu | tail -n 30'
```

- 若服务刚启动，短暂等待后重试健康检查；确认运行中的实际版本和进程 UID / GID。查看日志时勿输出密码或完整凭据。
- 用既有账号登录，确认 `/api/dashboard` 能返回资产、金额、份额和已缓存年化；必要时核对 `/api/market/cache-status` 的覆盖日期、错误和任务状态。不得把用户余额、认证信息打印到公开日志。
- 按变更验证页面流程；导出功能还需下载并解析 CSV / ZIP。不要只检查 HTML 能打开。
- 前端需刷新页面以载入新资源。已有缓存应跨服务重启保留；停服备份、旧发布目录和前一版本路径留作回滚依据。
- OpenWrt 通常没有 `rg`、`sqlite3`，远端查询使用已存在的工具，不依赖本机工具直接可用。

### 4. 回滚

先读取本次备份中的 `previous-release.txt`，确认要恢复的目录。以下命令在服务器上执行，将 `<release-id>` 替换为本次部署编号：

```sh
set -e
base=/opt/fulibu
previous="$(cat "$base/backups/<release-id>/previous-release.txt")"
test -x "$previous/bin/fulibu"
/etc/init.d/fulibu stop
for stop_wait in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if ! pidof fulibu >/dev/null; then break; fi
    sleep 1
done
if pidof fulibu >/dev/null; then /etc/init.d/fulibu start; exit 1; fi
trap '/etc/init.d/fulibu start' EXIT
ln -s "$previous" "$base/current.rollback"
mv -Tf "$base/current.rollback" "$base/current"
/etc/init.d/fulibu start
trap - EXIT
```

仅切换程序和静态文件不会恢复数据库。若涉及与旧程序不兼容的数据迁移，先检查迁移及部署后的新写入，保存当前数据的一致性备份，再制定数据库恢复步骤；不要直接用旧备份覆盖当前账本，否则会丢失部署后的资产修改。回滚后重新验证健康检查、页面和认证接口。
