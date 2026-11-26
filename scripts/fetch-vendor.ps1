$ErrorActionPreference = "Stop"

$VendorDir = "web\static\vendor"
New-Item -ItemType Directory -Force -Path $VendorDir | Out-Null

$XtermVer = "5.5.0"
$FitVer = "0.10.0"

function Download-WithMirrors {
    param(
        [string]$Target,
        [string[]]$Urls
    )
    $Tmp = "$Target.tmp"
    foreach ($url in $Urls) {
        Write-Host "  -> 下载 $url" -ForegroundColor DarkGray
        try {
            Invoke-WebRequest -Uri $url -OutFile $Tmp -TimeoutSec 20 -UseBasicParsing
            if ((Test-Path $Tmp) -and ((Get-Item $Tmp).Length -gt 0)) {
                Move-Item -Force $Tmp $Target
                return $true
            }
        } catch {
            if (Test-Path $Tmp) { Remove-Item -Force $Tmp }
        }
    }
    return $false
}

Write-Host "=== 拉取官方离线 xterm.js ($XtermVer) 与 addon-fit ($FitVer) ===" -ForegroundColor Cyan

$ok1 = Download-WithMirrors "$VendorDir\xterm.min.css" @(
    "https://cdn.jsdelivr.net/npm/@xterm/xterm@$XtermVer/css/xterm.min.css",
    "https://unpkg.com/@xterm/xterm@$XtermVer/css/xterm.min.css",
    "https://registry.npmmirror.com/@xterm/xterm/$XtermVer/files/css/xterm.min.css"
)
$ok2 = Download-WithMirrors "$VendorDir\xterm.min.js" @(
    "https://cdn.jsdelivr.net/npm/@xterm/xterm@$XtermVer/lib/xterm.min.js",
    "https://unpkg.com/@xterm/xterm@$XtermVer/lib/xterm.min.js",
    "https://registry.npmmirror.com/@xterm/xterm/$XtermVer/files/lib/xterm.min.js"
)
$ok3 = Download-WithMirrors "$VendorDir\addon-fit.min.js" @(
    "https://cdn.jsdelivr.net/npm/@xterm/addon-fit@$FitVer/lib/addon-fit.min.js",
    "https://unpkg.com/@xterm/addon-fit@$FitVer/lib/addon-fit.min.js",
    "https://registry.npmmirror.com/@xterm/addon-fit/$FitVer/files/lib/addon-fit.min.js"
)

if ($ok1 -and $ok2 -and $ok3) {
    Write-Host "官方 xterm.js 离线资源已就绪: $VendorDir" -ForegroundColor Green
} else {
    Write-Host "网络受限未能下载官方 xterm.js，已保留内置零依赖离线终端引擎，仍可正常编译和断网使用。" -ForegroundColor Yellow
}
