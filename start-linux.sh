#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
APP_HOST="${APP_HOST:-0.0.0.0}"
APP_PORT="${APP_PORT:-3000}"
APP_URL="http://localhost:${APP_PORT}/"
DATA_DIR="${DATA_DIR:-${SCRIPT_DIR}/.wrangler/state}"

cd "$SCRIPT_DIR"

fail() {
  printf '启动失败：%s\n' "$1" >&2
  exit 1
}

is_app_running() {
  local url

  for url in \
    "$APP_URL" \
    "http://127.0.0.1:${APP_PORT}/" \
    "http://[::1]:${APP_PORT}/"
  do
    if curl -fsS --max-time 2 "$url" 2>/dev/null | grep -q "复利簿"; then
      return 0
    fi
  done

  return 1
}

port_in_use() {
  if command -v ss >/dev/null 2>&1; then
    ss -ltnH | awk '{ print $4 }' | grep -Eq "[:.]${APP_PORT}$"
  elif command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$APP_PORT" -sTCP:LISTEN >/dev/null 2>&1
  else
    return 1
  fi
}

[[ "$APP_PORT" =~ ^[0-9]+$ ]] || fail "APP_PORT 必须是有效端口号。"
(( APP_PORT >= 1 && APP_PORT <= 65535 )) || fail "APP_PORT 必须在 1 到 65535 之间。"

command -v curl >/dev/null 2>&1 || fail "未找到 curl，请先安装 curl。"

if is_app_running; then
  printf '复利簿已经在运行：%s\n' "$APP_URL"
  exit 0
fi

if port_in_use; then
  fail "${APP_PORT} 端口已被其他程序占用。"
fi

command -v node >/dev/null 2>&1 || fail "未找到 Node.js，请先安装 Node.js 22 或更高版本。"
command -v npm >/dev/null 2>&1 || fail "未找到 npm，请重新安装 Node.js。"

NODE_MAJOR="$(node -p 'Number(process.versions.node.split(".")[0])')"
(( NODE_MAJOR >= 22 )) || fail "当前 Node.js 版本过低，需要 Node.js 22 或更高版本。"

if [[ ! -d node_modules ]]; then
  printf '首次启动，正在安装依赖...\n'
  npm ci
fi

printf '正在构建生产版本...\n'
npm run build
mkdir -p "$DATA_DIR"

printf '正在启动复利簿：%s（监听 %s:%s）\n' "$APP_URL" "$APP_HOST" "$APP_PORT"
printf '按 Ctrl+C 停止服务。\n'

exec "$SCRIPT_DIR/node_modules/.bin/wrangler" dev \
  --config "$SCRIPT_DIR/dist/server/wrangler.json" \
  --ip "$APP_HOST" \
  --port "$APP_PORT" \
  --persist-to "$DATA_DIR" \
  --log-level warn \
  --show-interactive-dev-session false
