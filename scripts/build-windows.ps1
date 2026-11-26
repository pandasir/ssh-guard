$ErrorActionPreference = "Stop"

# 自动启用多级 Go 模块代理（如已设置 GOPROXY 则沿用）
if (-not $env:GOPROXY) {
    $env:GOPROXY = "https://proxy.golang.org,https://goproxy.cn,direct"
}
$GoBin = Join-Path (go env GOPATH) "bin"
if ($env:PATH -notlike "*$GoBin*") {
    $env:PATH = "$GoBin;$env:PATH"
}

Write-Host "=== [1/5] 准备离线内置 xterm.js 前端资源 ===" -ForegroundColor Cyan
& "$PSScriptRoot\fetch-vendor.ps1"

Write-Host "=== [2/5] 同步 Go 依赖并运行单元测试 (GOPROXY=$env:GOPROXY) ===" -ForegroundColor Cyan
go mod tidy
go test -v ./internal/...

Write-Host "=== [3/5] 编译纯离线网页版单二进制 (Windows amd64 + macOS M4 arm64) ===" -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path bin | Out-Null
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-windows-amd64.exe ./cmd/web

$env:GOOS = "darwin"
$env:GOARCH = "arm64"
go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-darwin-arm64 ./cmd/web
Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

Write-Host "=== [4/5] 检查 Wails CLI ===" -ForegroundColor Cyan
if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
    Write-Host "未检测到 wails，正在安装 Wails v2 CLI..." -ForegroundColor Yellow
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
}

Write-Host "=== [5/5] 编译 Windows 原生桌面版 .exe (WebView2) ===" -ForegroundColor Cyan
wails build -skipbindings -clean -platform windows/amd64

Write-Host ""
Write-Host "全部构建完成（100% 离线内置终端，无外部 CDN 依赖）:" -ForegroundColor Green
Write-Host "  - Windows 桌面版 Exe : build\bin\ssh-guard.exe"
Write-Host "  - Windows 网页版     : bin\ssh-guard-web-windows-amd64.exe"
Write-Host "  - macOS M4 网页版    : bin\ssh-guard-web-darwin-arm64"
