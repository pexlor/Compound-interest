FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /fulibu ./cmd/server

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 fulibu && mkdir -p /data && chown fulibu:fulibu /data
COPY --from=build /fulibu /usr/local/bin/fulibu
USER fulibu
ENV DATA_DIR=/data PORT=8080
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/fulibu"]
