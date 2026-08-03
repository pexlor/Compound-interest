#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_PORT="${TEST_PORT:-32991}"
TEST_STATE_DIR="$(mktemp -d)"
PID_FILE="${TEST_STATE_DIR}/fulibu.pid"
LOG_FILE="${TEST_STATE_DIR}/fulibu.log"
LAUNCHER_PID=""

cleanup() {
  if [[ -n "$LAUNCHER_PID" ]] && kill -0 "$LAUNCHER_PID" 2>/dev/null; then
    kill "$LAUNCHER_PID" 2>/dev/null || true
    wait "$LAUNCHER_PID" 2>/dev/null || true
  fi

  if [[ -f "$PID_FILE" ]]; then
    local service_pid
    service_pid="$(cat "$PID_FILE")"
    kill "$service_pid" 2>/dev/null || true
  fi

  rm -rf "$TEST_STATE_DIR"
}
trap cleanup EXIT

run_start() {
  APP_HOST=127.0.0.1 \
  APP_PORT="$TEST_PORT" \
  DATA_DIR="${TEST_STATE_DIR}/state" \
  PID_FILE="$PID_FILE" \
  LOG_FILE="$LOG_FILE" \
    sh "$PROJECT_DIR/start-linux.sh" >"${TEST_STATE_DIR}/launcher.log" 2>&1 &
  LAUNCHER_PID="$!"

  for _ in {1..60}; do
    if ! kill -0 "$LAUNCHER_PID" 2>/dev/null; then
      wait "$LAUNCHER_PID"
      LAUNCHER_PID=""
      return 0
    fi
    sleep 0.5
  done

  printf 'FAIL: 启动脚本没有返回终端。\n' >&2
  return 1
}

run_start
[[ -f "$PID_FILE" ]] || { printf 'FAIL: 未生成 PID 文件。\n' >&2; exit 1; }

FIRST_PID="$(cat "$PID_FILE")"
kill -0 "$FIRST_PID"
curl -fsS --max-time 5 "http://127.0.0.1:${TEST_PORT}/" | grep -q '复利簿'

run_start
SECOND_PID="$(cat "$PID_FILE")"
[[ "$SECOND_PID" != "$FIRST_PID" ]] || { printf 'FAIL: 重启后 PID 没有变化。\n' >&2; exit 1; }
! kill -0 "$FIRST_PID" 2>/dev/null || { printf 'FAIL: 旧进程仍在运行。\n' >&2; exit 1; }
kill -0 "$SECOND_PID"

printf 'PASS: Linux 后台启动与重启流程正常。\n'
