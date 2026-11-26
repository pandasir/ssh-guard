# SSH-Guard v4（纯进程内 IPC 零端口暴露 · 纯离线内置终端 · Web + Mac M4 / Windows）

> **v4 核心修复（解决 macOS WKWebView `Load failed` 与 Wails `embed` 绑定报错）**：
> - 桌面端全面升级为**纯进程内 Wails IPC 通信**（`APIRequest` + `OpenSSHSession/EventsEmit`），不再在桌面端监听本地 HTTP/WebSocket 端口，彻底杜绝 macOS WKWebView `Origin: null` CORS 拦截、ATS 限制与本地代理（Clash）干扰。
> - 构建脚本默认启用 `wails build -skipbindings`，兼容各版本 Go 编译器与 Wails v2 CLI。

---

# SSH-Guard v3（纯离线内置终端 · 网页版 + macOS M4 / Windows 原生桌面版）

基于 Go 构建的轻量级零信任 SSH 管理面板。v3 在双端架构基础上重点完成了**纯断网离线化、macOS M4 签名与原生快捷键体验、以及受限网络构建容错**。

## v3 重点升级内容

1. **100% 离线运行，彻底移除外部 CDN 依赖**
   - `index.html` 所有样式与脚本全部改为本地 `/vendor/*` 并由 `go:embed static` 编译进二进制，Web 端同步开启严格 `Content-Security-Policy`（拦截第三方脚本）。
   - 内置**双重保障**：
     - 构建脚本自动通过多镜像（jsDelivr / unpkg / npmmirror）拉取官方 `@xterm/xterm@5.5.0` 与 `@xterm/addon-fit@0.10.0` 嵌入二进制（完整支持 `vim`、`htop`、`tmux` 全屏 TUI）。
     - 即使在完全断网环境直接编译，项目已预置**零依赖纯原生 ANSI 终端引擎兜底**，解压即可直接编译运行，绝不白屏。
2. **macOS M4 签名与 Gatekeeper 自动化 + 原生编辑菜单**
   - `scripts/build-mac-m4.sh` 编译完成后自动执行 `xattr -cr` 清除隔离属性并完成 Ad-hoc 本地签名（也支持 `CODESIGN_IDENTITY="..."` 正式证书签名）。
   - 提供独立工具 `scripts/fix-mac-gatekeeper.sh`，从 CI 或其他设备拷入 `.app` 后一行命令即可解决“无法验证开发者/已损坏”提示。
   - `main.go` 为 macOS 注入原生 `AppMenu / EditMenu / WindowMenu`，彻底修复 Wails 在 macOS WebView 中 `Cmd+C / Cmd+V / Cmd+A` 快捷键失效的问题。
3. **Go 依赖拉取多级代理容错**
   - 构建脚本与 `Makefile` 默认启用 `GOPROXY=https://proxy.golang.org,https://goproxy.cn,direct`，避免直连拉取超时。

---

## 一键构建与运行

### macOS (Apple Silicon M4)

```bash
unzip ssh-guard-v3.zip
cd ssh-guard-v3

# 一键完成：离线 xterm.js 拉取 -> 测试 -> 网页版双平台编译 -> Mac M4 桌面端编译 -> 自动签名去隔离
chmod +x scripts/*.sh
./scripts/build-mac-m4.sh

# 如需同时生成 .dmg 安装包：
MAKE_DMG=1 ./scripts/build-mac-m4.sh

# 启动桌面版
open build/bin/SSH-Guard.app

# 或启动网页版（自动打开浏览器）
./bin/ssh-guard-web-darwin-arm64 -open
```

> 若从 GitHub Actions 下载了 `SSH-Guard-Desktop-macOS-arm64.zip` 解压后首次打开遇到 macOS 安全拦截提示，执行：
> ```bash
> ./scripts/fix-mac-gatekeeper.sh /路径/到/SSH-Guard.app
> ```

### 在 macOS 上交叉编译 Windows 安装包 (NSIS `.exe`)

项目为纯 Go 无 CGO 依赖，可直接在 macOS 上交叉编译 Windows 桌面端及带安装向导的 NSIS 安装包：

```bash
# 首次打包需安装 NSIS 工具
brew install makensis

# 确保 Wails CLI 在 PATH 中（或直接使用 $(go env GOPATH)/bin/wails）
export PATH="$(go env GOPATH)/bin:$PATH"

# 编译 Windows 桌面端并生成带安装向导的 NSIS 安装包（不加 -clean 可保留已有的 macOS .app）
wails build -skipbindings -platform windows/amd64 -nsis
```

编译完成后产物位于 `build/bin/` 目录：
- `build/bin/SSH-Guard-amd64-installer.exe`：带安装向导、快捷方式与卸载程序的标准安装包
- `build/bin/ssh-guard.exe`：免安装绿色单文件桌面版

### Windows (PowerShell)

```powershell
Expand-Archive ssh-guard-v3.zip -DestinationPath .
cd ssh-guard-v3

# 一键完成：离线 xterm.js 拉取 -> 测试 -> 网页版双平台编译 -> Windows 原生桌面端 (.exe) 编译
.\scripts\build-windows.ps1

# 启动桌面版
.\build\bin\ssh-guard.exe

# 或启动网页版
.\bin\ssh-guard-web-windows-amd64.exe -open
```

### GitHub Actions 云端同时打包双平台

推送到私有 GitHub 仓库后，在 Actions 页面运行 `Build Offline Web & Desktop (Mac M4 + Windows)`，云端会自动下载官方 `xterm.js` 并打包进二进制，一次性产出 Mac M4 `.app`、Windows `.exe` 与全平台网页版。

---

## 安全特性回顾

- **信封加密凭证保险库**：主密码仅在内存中经 **Argon2id** 派生 KEK（永不落盘），每条凭据用独立随机 DEK + **AES-256-GCM** 加密存储；握手结束、手动锁定或关闭应用立即 `Zeroize` 清零内存。
- **桌面端本地环回隔离**：绑定 `127.0.0.1:0` 随机端口 + 每次启动随机 256-bit `DesktopSecret`（仅通过 Wails IPC 下发）。
- **TOFU + 严格防 MITM**：首次连接核对 SHA-256 指纹，后续指纹变更立即阻断并记录 `CRITICAL` 级告警。
- **高危命令拦截守卫**：实时拦截 `rm -rf /`、`mkfs`、`dd of=/dev/...`、`chmod -R 777 /`、`reboot/shutdown`、`iptables -F`。
- **标准会话录像与回放**：自动保存 `asciinema v2 (.cast)` 录像（不录无回显密码），支持内置倍速回放。
