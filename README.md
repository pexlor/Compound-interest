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
