# 从项目根目录构建 Go 后端镜像，并使用普通用户运行服务。

FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go-backend/go.mod go-backend/go.sum* ./
RUN go mod download
COPY go-backend/ .
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /fulibu ./cmd/server
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /fulibu-backup-status ./cmd/backup-status

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 fulibu && mkdir -p /data && chown fulibu:fulibu /data
COPY --from=build /fulibu /usr/local/bin/fulibu
COPY --from=build /fulibu-backup-status /usr/local/bin/fulibu-backup-status
USER fulibu
ENV DATA_DIR=/data PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fulibu"]
