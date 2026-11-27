$ErrorActionPreference = "Stop"

# 自动启用多级 Go 模块代理（如已设置 GOPROXY 则沿用）
if (-not $env:GOPROXY) {
    $env:GOPROXY = "https://proxy.golang.org,https://goproxy.cn,direct"
}

Write-Host "=== [1/5] 准备离线 Web 版内置 xterm.js 资源 ===" -ForegroundColor Cyan
& "$PSScriptRoot\fetch-vendor.ps1"

Write-Host "=== [2/5] 运行单元测试与 MyGo UI 静态检查 ===" -ForegroundColor Cyan
go test -v ./internal/...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go tool mygo vet .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "=== [3/5] 编译纯离线网页版单二进制 (Windows amd64 + macOS arm64) ===" -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path bin | Out-Null
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-windows-amd64.exe ./cmd/web
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$env:GOOS = "darwin"
$env:GOARCH = "arm64"
go build -trimpath -ldflags="-s -w" -o bin/ssh-guard-web-darwin-arm64 ./cmd/web
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

Write-Host "=== [4/5] 编译 Windows 原生桌面版与 NSIS 单文件安装包 (零 WebView2) ===" -ForegroundColor Cyan
$prevGoFlags = $env:GOFLAGS
try {
    $env:GOFLAGS = "-tags=mygo_nowebview"
    go tool mygo build -platform windows/amd64
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    $dllSrc = "dist\mygo\windows-amd64\ghostty-vt.dll"
    if (-not (Test-Path $dllSrc)) {
        throw "未找到 MyGo 构建产出的 $dllSrc"
    }

    Write-Host "=== [5/5] 编译 100% 离线单文件绿色版 (portable.exe) ===" -ForegroundColor Cyan
    $embedDir = "internal\desktopui\nativeembed"
    $embedDll = Join-Path $embedDir "ghostty-vt.dll"
    New-Item -ItemType Directory -Force -Path $embedDir | Out-Null
    try {
        Copy-Item -Force $dllSrc $embedDll
        $env:GOFLAGS = "-tags=mygo_nowebview,portable"
        go tool mygo build -platform windows/amd64 -skip-installer -o dist/mygo-portable
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

        Copy-Item -Force "dist\mygo-portable\windows-amd64\SSH-Guard.exe" "dist\mygo\windows-amd64\SSH-Guard-Windows-amd64-portable.exe"
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $embedDll
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue "dist\mygo-portable"
    }
} finally {
    if ($null -eq $prevGoFlags) {
        Remove-Item Env:GOFLAGS -ErrorAction SilentlyContinue
    } else {
        $env:GOFLAGS = $prevGoFlags
    }
}

$setupExe = Get-ChildItem -Path "dist\mygo\windows-amd64" -Filter "ssh-guard-setup-*-amd64.exe" | Select-Object -First 1

Write-Host ""
Write-Host "全部构建完成（原生 MyGo + Ghostty 终端，无需 WebView2）:" -ForegroundColor Green
if ($setupExe) {
    Write-Host "  - Windows NSIS 安装包 : $($setupExe.FullName)"
}
Write-Host "  - Windows 离线绿色版  : dist\mygo\windows-amd64\SSH-Guard-Windows-amd64-portable.exe"
Write-Host "  - Windows 网页版      : bin\ssh-guard-web-windows-amd64.exe"
Write-Host "  - macOS ARM64 网页版  : bin\ssh-guard-web-darwin-arm64"
