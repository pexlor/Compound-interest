# 为现有 ARM64 OpenWrt 服务器构建静态后端和同源前端发布包。
FROM golang:1.24-bookworm AS backend
WORKDIR /src
COPY go-backend/go.mod go-backend/go.sum ./
RUN go mod download
COPY go-backend/ ./
RUN CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -tags netgo,osusergo,sqlite_omit_load_extension -trimpath -ldflags="-s -w -linkmode external -extldflags '-static'" -o /out/fulibu ./cmd/server
RUN CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -tags netgo,osusergo,sqlite_omit_load_extension -trimpath -ldflags="-s -w -linkmode external -extldflags '-static'" -o /out/fulibu-backup-status ./cmd/backup-status
FROM node:22-bookworm-slim AS frontend
WORKDIR /app
COPY frontend/package.json ./
RUN npm install
COPY frontend/ ./
RUN npm test && npm run build
FROM scratch
COPY --from=backend /out/ /bin/
COPY --from=frontend /app/dist/ /static/
