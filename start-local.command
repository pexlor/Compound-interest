#!/bin/zsh

set -u

SCRIPT_DIR="${0:A:h}"
APP_URL="http://localhost:3000/"
cd "$SCRIPT_DIR" || exit 1

pause_on_error() {
  printf "\n按回车键关闭窗口..."
  read -r
}

fail() {
  printf "\n启动失败：%s\n" "$1"
  pause_on_error
  exit 1
}

is_app_running() {
  curl -fsS --max-time 2 "$APP_URL" 2>/dev/null | grep -q "复利簿"
}

if is_app_running; then
  printf "复利簿已经在运行：%s\n" "$APP_URL"
  open "$APP_URL"
  exit 0
fi

if lsof -nP -iTCP:3000 -sTCP:LISTEN >/dev/null 2>&1; then
  fail "3000 端口已被其他程序占用。"
fi

command -v node >/dev/null 2>&1 || fail "未找到 Node.js，请先安装 Node.js 22 或更高版本。"
command -v npm >/dev/null 2>&1 || fail "未找到 npm，请重新安装 Node.js。"

NODE_MAJOR="$(node -p 'Number(process.versions.node.split(".")[0])')"
if (( NODE_MAJOR < 22 )); then
  fail "当前 Node.js 版本过低，需要 Node.js 22 或更高版本。"
fi

if [[ ! -d node_modules ]]; then
  printf "首次启动，正在安装依赖...\n"
  npm install || fail "依赖安装失败，请检查网络连接。"
fi

printf "正在启动复利簿...\n"
npm run dev &
SERVER_PID=$!

cleanup() {
  if kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    kill "$SERVER_PID" >/dev/null 2>&1
    wait "$SERVER_PID" >/dev/null 2>&1
  fi
}
trap cleanup INT TERM EXIT

for _ in {1..60}; do
  if is_app_running; then
    printf "\n启动成功：%s\n" "$APP_URL"
    open "$APP_URL"
    wait "$SERVER_PID"
    exit $?
  fi
  if ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    wait "$SERVER_PID"
    fail "本地服务进程提前退出，请查看上方日志。"
  fi
  sleep 0.5
done

fail "等待本地服务启动超时，请查看上方日志。"
