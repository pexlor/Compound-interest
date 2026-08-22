# 资产星图

前后端分离的本地应用：React/Vite 前端与 Go/SQLite API 独立运行。没有 Next.js、Cloudflare Worker、D1 或 R2 运行时依赖。

```bash
docker compose up --build -d
```

打开 <http://localhost:3000>。数据保存在 Docker 卷 `fulibu-data`；浏览器的 `/api` 请求由 Nginx 转发至 Go 容器。
