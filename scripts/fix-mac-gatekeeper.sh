#!/usr/bin/env bash
set -euo pipefail

APP_PATH="${1:-build/bin/SSH-Guard.app}"

if [ ! -d "$APP_PATH" ]; then
  echo "未找到应用包: $APP_PATH"
  echo "用法: ./scripts/fix-mac-gatekeeper.sh /路径/到/SSH-Guard.app"
  exit 1
fi

echo "=== 正在清除 macOS Quarantine 隔离属性 ==="
xattr -cr "$APP_PATH"

SIGN_ID="${CODESIGN_IDENTITY:--}"
echo "=== 正在进行本地签名 (Identity: ${SIGN_ID}) ==="
codesign --force --deep --sign "$SIGN_ID" "$APP_PATH"

echo "=== 校验签名完整性 ==="
codesign --verify --deep --strict --verbose=2 "$APP_PATH"

echo "✅ 处理完成！现在可直接双击打开或执行: open \"$APP_PATH\""
