#!/usr/bin/env bash
set -euo pipefail

# 自动启用多级 Go 模块代理（解决直连超时问题；如已自定义 GOPROXY 则沿用）
export GOPROXY="${GOPROXY:-https://proxy.golang.org,https://goproxy.cn,direct}"

echo "=== [1/5] 准备离线 Web 版内置 xterm.js 资源 ==="
chmod +x scripts/fetch-vendor.sh scripts/fix-mac-gatekeeper.sh
./scripts/fetch-vendor.sh

echo "=== [2/5] 运行单元测试与 MyGo UI 静态检查 ==="
go test -v -race ./...
go tool mygo vet .

echo "=== [3/5] 编译纯离线网页版单二进制 (macOS arm64 + Windows amd64) ==="
mkdir -p bin
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-darwin-arm64 ./cmd/web
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-windows-amd64.exe ./cmd/web

echo "=== [4/5] 编译 macOS Apple Silicon 原生桌面版 (.app + .dmg, macOS 13+) ==="
SIGN_ID="${CODESIGN_IDENTITY:--}"
BUILD_ARGS=(-platform darwin/arm64 -sign "$SIGN_ID")
if [ "${SKIP_DMG:-0}" = "1" ]; then
  BUILD_ARGS+=(-skip-dmg)
fi
go tool mygo build "${BUILD_ARGS[@]}"

APP_PATH="dist/mygo/darwin-arm64/SSH-Guard.app"

echo "=== [5/5] 校验 Info.plist、libghostty-vt.dylib 与严格代码签名 ==="
xattr -cr "$APP_PATH"
MIN_VER="$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$APP_PATH/Contents/Info.plist")"
if [ "$MIN_VER" != "13.0" ]; then
  echo "ERROR: expected LSMinimumSystemVersion=13.0, got ${MIN_VER}" >&2
  exit 1
fi
if [ ! -f "$APP_PATH/Contents/Resources/libghostty-vt.dylib" ]; then
  echo "ERROR: missing bundled libghostty-vt.dylib in $APP_PATH/Contents/Resources/" >&2
  exit 1
fi
codesign --verify --deep --strict --verbose=2 "$APP_PATH"

DMG_SRC="$(find dist/mygo/darwin-arm64 -maxdepth 1 -name '*.dmg' | head -n 1 || true)"
if [ -n "$DMG_SRC" ] && [ "${MAKE_DMG:-1}" = "1" ]; then
  cp -f "$DMG_SRC" bin/SSH-Guard-macOS-arm64.dmg
fi

echo ""
echo "✅ 全部构建完成（原生 MyGo + Ghostty 终端，零 WebView）："
echo "  - macOS ARM64 桌面版 App : ${APP_PATH}"
if [ -n "$DMG_SRC" ]; then
  echo "  - macOS ARM64 安装镜像   : ${DMG_SRC}"
fi
echo "  - macOS ARM64 网页版     : bin/ssh-guard-web-darwin-arm64"
echo "  - Windows amd64 网页版   : bin/ssh-guard-web-windows-amd64.exe"
