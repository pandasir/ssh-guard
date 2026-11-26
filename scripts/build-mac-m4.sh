#!/usr/bin/env bash
set -euo pipefail

# 自动启用多级 Go 模块代理（解决直连超时问题；如已自定义 GOPROXY 则沿用）
export GOPROXY="${GOPROXY:-https://proxy.golang.org,https://goproxy.cn,direct}"
export PATH="$(go env GOPATH)/bin:$PATH"

echo "=== [1/5] 准备离线内置 xterm.js 前端资源 ==="
chmod +x scripts/fetch-vendor.sh scripts/fix-mac-gatekeeper.sh
./scripts/fetch-vendor.sh

echo "=== [2/5] 同步 Go 依赖并运行单元测试 (GOPROXY=${GOPROXY}) ==="
go mod tidy
go test -v ./internal/...

echo "=== [3/5] 编译纯离线网页版单二进制 (macOS M4 arm64 + Windows amd64) ==="
mkdir -p bin
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-darwin-arm64 ./cmd/web
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-windows-amd64.exe ./cmd/web

echo "=== [4/5] 检查并编译 macOS Apple Silicon (M4) 原生桌面版 .app ==="
if ! command -v wails >/dev/null 2>&1; then
  echo "未检测到 wails，正在自动安装 Wails v2 CLI..."
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
fi
wails build -skipbindings -clean -platform darwin/arm64

echo "=== [5/5] 执行 macOS Ad-hoc 签名并清除 Quarantine 隔离标记 ==="
./scripts/fix-mac-gatekeeper.sh "build/bin/SSH-Guard.app"

if [ "${MAKE_DMG:-0}" = "1" ]; then
  echo "=== [附加] 打包 macOS .dmg 安装镜像 ==="
  rm -f bin/SSH-Guard-macOS-M4.dmg
  hdiutil create -volname "SSH-Guard" -srcfolder "build/bin/SSH-Guard.app" -ov -format UDZO "bin/SSH-Guard-macOS-M4.dmg"
fi

echo ""
echo "✅ 全部构建完成（100% 离线内置终端，无外部 CDN 依赖）："
echo "  - macOS M4 桌面版 App : build/bin/SSH-Guard.app"
echo "  - macOS M4 网页版     : bin/ssh-guard-web-darwin-arm64"
echo "  - Windows  网页版     : bin/ssh-guard-web-windows-amd64.exe"
