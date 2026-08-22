# Go 本地后端

这是替代 Cloudflare Worker/D1 的本地 Go API 服务。它使用 SQLite（`/data/fulibu.db`），不需要 Wrangler、D1 或 R2。

```bash
cd go-backend
docker compose up --build -d
curl http://localhost:8080/healthz
```

API 使用与现有前端相同的 Cookie、路径和 JSON 字段：认证、资产、收入、历史、总览和汇率均已迁移。数据库表结构与旧 D1 兼容；导入旧库时请在服务停止后将 SQLite 文件复制为数据目录内的 `fulibu.db`。

前端切换前，将开发服务器的 `/api` 请求代理到 `http://localhost:8080`。行情收益计算仍应作为下一批迁移工作：它目前依赖 TypeScript 中的多数据源复权算法，不能以简化版本替代，否则会改变收益结果。
