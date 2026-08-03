#!/usr/bin/env bash

if [ -z "${BASH_VERSION:-}" ]; then
  command -v bash >/dev/null 2>&1 || {
    printf '启动失败：未找到 Bash，请先安装 bash。\n' >&2
    exit 1
  }
  exec bash "$0" "$@"
fi

if set -o 2>/dev/null | grep -Eq '^posix[[:space:]]+on$'; then
  exec bash "$0" "$@"
fi

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
APP_HOST="${APP_HOST:-0.0.0.0}"
APP_PORT="${APP_PORT:-3000}"
APP_URL="http://localhost:${APP_PORT}/"
DATA_DIR="${DATA_DIR:-${SCRIPT_DIR}/.wrangler/state}"
PID_FILE="${PID_FILE:-${SCRIPT_DIR}/.wrangler/fulibu.pid}"
LOG_FILE="${LOG_FILE:-${SCRIPT_DIR}/.wrangler/fulibu.log}"

cd "$SCRIPT_DIR"

fail() {
  printf '启动失败：%s\n' "$1" >&2
  exit 1
}

is_app_running() {
  local response
  local url

  for url in \
    "$APP_URL" \
    "http://127.0.0.1:${APP_PORT}/" \
    "http://[::1]:${APP_PORT}/"
  do
    if response="$(curl -fsS --max-time 2 "$url" 2>/dev/null)" && [[ "$response" == *"复利簿"* ]]; then
      return 0
    fi
  done

  return 1
}

list_port_pids() {
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -t -iTCP:"$APP_PORT" -sTCP:LISTEN 2>/dev/null || true
  elif command -v fuser >/dev/null 2>&1; then
    fuser -n tcp "$APP_PORT" 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' || true
  elif command -v ss >/dev/null 2>&1; then
    ss -ltnp "sport = :${APP_PORT}" 2>/dev/null \
      | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' \
      | sort -u
  fi
}

stop_pid() {
  local pid="$1"
  local remaining

  [[ "$pid" =~ ^[0-9]+$ ]] || return 0
  kill -0 "$pid" 2>/dev/null || return 0

  printf '正在停止旧进程 %s...\n' "$pid"
  kill "$pid" 2>/dev/null || true

  for remaining in {1..20}; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.25
  done

  printf '旧进程 %s 未及时退出，正在强制停止。\n' "$pid"
  kill -9 "$pid" 2>/dev/null || true
}

stop_previous_app() {
  local pid
  local stopped_pid=""

  if [[ -f "$PID_FILE" ]]; then
    pid="$(cat "$PID_FILE")"
    stop_pid "$pid"
    stopped_pid="$pid"
    rm -f "$PID_FILE"
  fi

  if is_app_running; then
    while IFS= read -r pid; do
      [[ -n "$pid" && "$pid" != "$stopped_pid" ]] || continue
      stop_pid "$pid"
    done < <(list_port_pids)
  fi

  if port_in_use; then
    fail "${APP_PORT} 端口仍被其他程序占用。"
  fi
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
command -v node >/dev/null 2>&1 || fail "未找到 Node.js，请先安装 Node.js 22 或更高版本。"
command -v npm >/dev/null 2>&1 || fail "未找到 npm，请重新安装 Node.js。"

NODE_MAJOR="$(node -p 'Number(process.versions.node.split(".")[0])')"
(( NODE_MAJOR >= 22 )) || fail "当前 Node.js 版本过低，需要 Node.js 22 或更高版本。"

if [[ ! -d node_modules ]]; then
  printf '首次启动，正在安装依赖...\n'
  npm ci
fi

mkdir -p "$DATA_DIR" "$(dirname -- "$PID_FILE")" "$(dirname -- "$LOG_FILE")"
stop_previous_app

printf '正在构建生产版本...\n'
npm run build

printf '正在后台启动复利簿（监听 %s:%s）...\n' "$APP_HOST" "$APP_PORT"
printf '\n[%s] 启动服务\n' "$(date '+%Y-%m-%d %H:%M:%S')" >>"$LOG_FILE"

nohup "$SCRIPT_DIR/node_modules/.bin/wrangler" dev \
  --config "$SCRIPT_DIR/dist/server/wrangler.json" \
  --ip "$APP_HOST" \
  --port "$APP_PORT" \
  --persist-to "$DATA_DIR" \
  --log-level warn \
  --show-interactive-dev-session false \
  </dev/null >>"$LOG_FILE" 2>&1 &

SERVICE_PID="$!"
printf '%s\n' "$SERVICE_PID" >"$PID_FILE"
disown "$SERVICE_PID" 2>/dev/null || true

for _ in {1..30}; do
  if ! kill -0 "$SERVICE_PID" 2>/dev/null; then
    rm -f "$PID_FILE"
    tail -n 20 "$LOG_FILE" >&2 || true
    fail "后台进程已退出，请查看日志：${LOG_FILE}"
  fi

  if is_app_running; then
    printf '复利簿已在后台运行：%s\n' "$APP_URL"
    printf '进程 PID：%s\n' "$SERVICE_PID"
    printf '运行日志：%s\n' "$LOG_FILE"
    exit 0
  fi

  sleep 0.5
done

stop_pid "$SERVICE_PID"
rm -f "$PID_FILE"
fail "启动超时，请查看日志：${LOG_FILE}"
