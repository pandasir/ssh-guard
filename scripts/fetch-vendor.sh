#!/usr/bin/env bash
set -euo pipefail

VENDOR_DIR="web/static/vendor"
mkdir -p "$VENDOR_DIR"

XTERM_VER="5.5.0"
FIT_VER="0.10.0"

download_with_mirrors() {
  local target="$1"
  shift
  local tmp="${target}.tmp"
  for url in "$@"; do
    echo "  -> 下载 ${url}"
    if curl -fsSL --connect-timeout 8 --max-time 30 "$url" -o "$tmp" && [ -s "$tmp" ]; then
      mv "$tmp" "$target"
      return 0
    fi
    rm -f "$tmp"
  done
  return 1
}

echo "=== 拉取官方离线 xterm.js (${XTERM_VER}) 与 addon-fit (${FIT_VER}) ==="

ok=1
download_with_mirrors "$VENDOR_DIR/xterm.min.css" \
  "https://cdn.jsdelivr.net/npm/@xterm/xterm@${XTERM_VER}/css/xterm.min.css" \
  "https://unpkg.com/@xterm/xterm@${XTERM_VER}/css/xterm.min.css" \
  "https://registry.npmmirror.com/@xterm/xterm/${XTERM_VER}/files/css/xterm.min.css" || ok=0

download_with_mirrors "$VENDOR_DIR/xterm.min.js" \
  "https://cdn.jsdelivr.net/npm/@xterm/xterm@${XTERM_VER}/lib/xterm.min.js" \
  "https://unpkg.com/@xterm/xterm@${XTERM_VER}/lib/xterm.min.js" \
  "https://registry.npmmirror.com/@xterm/xterm/${XTERM_VER}/files/lib/xterm.min.js" || ok=0

download_with_mirrors "$VENDOR_DIR/addon-fit.min.js" \
  "https://cdn.jsdelivr.net/npm/@xterm/addon-fit@${FIT_VER}/lib/addon-fit.min.js" \
  "https://unpkg.com/@xterm/addon-fit@${FIT_VER}/lib/addon-fit.min.js" \
  "https://registry.npmmirror.com/@xterm/addon-fit/${FIT_VER}/files/lib/addon-fit.min.js" || ok=0

if [ "$ok" -eq 1 ]; then
  echo "✅ 官方 xterm.js 离线资源已就绪：${VENDOR_DIR}/"
else
  echo "⚠️  网络受限未能下载官方 xterm.js，已保留内置零依赖离线终端引擎，仍可正常编译和断网使用。"
fi
