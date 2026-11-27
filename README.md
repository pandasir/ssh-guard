# SSH-Guard

基于 Go 构建的轻量级零信任 SSH 管理面板，提供 **macOS / Windows 纯 Go 原生桌面版（MyGo + Ghostty 终端，零 WebView）** 与 **100% 离线 Web 单文件版**。

## 核心特性

- **零信任信封加密保险库**：主密码仅在内存中经 **Argon2id** 派生 KEK（永不落盘），每条主机凭据使用独立随机 DEK 与 **AES-256-GCM** 加密存入 SQLite；握手结束、空闲超时（30 分钟）/ 最长会话（8 小时）自动锁定、手动锁定或退出应用时立即清零内存。
- **纯 Go 原生桌面 UI + Ghostty 终端引擎（零 WebView、零本地端口、极致低内存）**：桌面端基于 `github.com/egoist/mygo` 与 `libghostty-vt` 构建原生界面与终端仿真器，完全移除 Wails / WKWebView / WebView2 依赖，不在本机监听任何 HTTP/WebSocket 端口；macOS 使用 Metal GPU 渲染并在聚焦空闲 2 秒后自动回收显存/帧缓冲，Windows 默认采用多核 CPU 光栅化 + Win32 GDI 渲染（`MYGO_GPU=0`）以规避 25–35 MB D3D11/DXGI 驱动开销，同时支持通过 `MYGO_GPU=1` 随时切回 GPU 渲染。
- **100% 离线双端支持**：
  - **桌面端**：macOS 13+ Apple Silicon（内置签名 `libghostty-vt.dylib`）与 Windows 10 1809+ / 11（无需安装 WebView2 Runtime，提供 NSIS 单文件安装包与 100% 离线单文件绿色版 `portable.exe`）。
  - **Web 端**：保留独立纯 Go 单二进制（`cmd/web`），静态资源与 `xterm.js`（含左侧时间戳/行号栏）经 `go:embed` 全量离线内置，默认启用严格 CSP，零 MyGo 依赖。
- **TOFU + 防 MITM 主机指纹校验**：首次连接弹窗核对服务器 SHA-256 公钥指纹；若后续指纹变更则立即阻断连接并记录 `CRITICAL` 级 `MITM_ALERT` 审计告警。
- **高危命令实时拦截守卫**：在终端输入层实时检测并拦截 `rm -rf /`、`mkfs`、`dd of=/dev/...`、`chmod -R 777 /`、Fork 炸弹、`reboot/shutdown`、`iptables -F` 等高危操作，自动发送 Ctrl+C、记录审计日志并弹出非阻塞告警提示。
- **标准会话录像与流式倍速回放**：自动以 `asciinema v2 (.cast)` 格式保存操作录像（跳过无回显密码输入），支持桌面端 Ghostty 流式倍速回放（1x/2x/4x）与 Web 端内置回放。
- **零 CGO 纯 Go 构建**：采用纯 Go SQLite 驱动与纯 Go FFI（purego）加载原生图形/终端库，跨平台构建无需 C 编译器。

---

## 快速开始

### 环境要求

- **Go 1.27+**（单一根 `go.mod` 同时管理桌面端与 Web 端；`mygo` CLI 已通过 `go.mod` `tool` 指令锁定版本，无需单独安装 Wails 或 Node.js）
- 运行目标系统要求：
  - **macOS 桌面端**：macOS 13.0+ (Apple Silicon)
  - **Windows 桌面端**：Windows 10 (1809+) / Windows 11 (x64)，无需 WebView2 Runtime

### 克隆仓库

```bash
git clone https://github.com/pandasir/ssh-guard.git
cd ssh-guard
```

### 运行原生桌面版（开发模式）

```bash
go tool mygo dev
```

### 运行 Web 版

```bash
# 直接运行并自动打开浏览器（默认监听 127.0.0.1:8080）
go run ./cmd/web -addr 127.0.0.1:8080 -data ./data -open
```

Web 版常用启动参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | 监听地址（建议仅绑定回环地址或内网/Tailscale IP） |
| `-data` | `./data` | SQLite 数据库与会话录像存储目录（也可通过 `SSH_GUARD_DATA_DIR` 指定） |
| `-tls-cert` | `""` | TLS 证书路径（与 `-tls-key` 同时指定时启用 HTTPS/WSS） |
| `-tls-key` | `""` | TLS 私钥路径 |
| `-secure-cookie` | `false` | 为会话 Cookie 启用 `Secure` 标记（置于 HTTPS 反向代理后时建议开启） |
| `-open` | `false` | 启动后自动使用默认浏览器打开面板 |

---

## 构建与打包

### macOS (Apple Silicon)

```bash
chmod +x scripts/*.sh

# 一键完成测试、MyGo Vet、Web 版编译、macOS 桌面端 (.app + .dmg) 编译与签名校验
./scripts/build-mac-m4.sh

# 启动桌面版
open dist/mygo/darwin-arm64/SSH-Guard.app

# 或启动 Web 版
./bin/ssh-guard-web-darwin-arm64 -open
```

> 若从其他设备或 GitHub Actions 下载 `.app` 遇到 macOS Gatekeeper 拦截提示，可执行：
> ```bash
> ./scripts/fix-mac-gatekeeper.sh /路径/到/SSH-Guard.app
> ```

### macOS 交叉编译 Windows 桌面端（NSIS 安装包 + 离线绿色单文件）

```bash
# 如需在 macOS 上同时生成 NSIS 安装包，安装 makensis
brew install makensis

# 同时产出 NSIS 单文件安装包与 100% 离线单文件绿色版 portable.exe
make build-desktop-win-nsis
```

产物位置：
- `dist/mygo/windows-amd64/ssh-guard-setup-*-amd64.exe`：带安装向导、快捷方式与卸载程序的 NSIS 安装包（内置 `SSH-Guard.exe` 与 `ghostty-vt.dll`）
- `dist/mygo/windows-amd64/SSH-Guard-Windows-amd64-portable.exe`：100% 离线免安装绿色单文件桌面版（首次启动自动校验并原子释放内置 `ghostty-vt.dll` 至 `%LocalAppData%\SSH-Guard\natives\`，支持 `MYGO_GHOSTTY_VT` 环境变量覆盖）

### Windows (PowerShell)

```powershell
# 一键完成测试、Web 版、Windows NSIS 安装包与离线单文件绿色版编译
.\scripts\build-windows.ps1

# 启动离线绿色版
.\dist\mygo\windows-amd64\SSH-Guard-Windows-amd64-portable.exe

# 或启动 Web 版
.\bin\ssh-guard-web-windows-amd64.exe -open
```

### Makefile 快捷命令

```bash
make test                    # 运行全量竞态单元测试 (go test -v -race ./...)
make vet                     # 运行 MyGo 原生 UI 静态检查 (go tool mygo vet .)
make dev-desktop             # 启动 MyGo 原生桌面版热开发模式
make run-web                 # 启动本地离线 Web 版
make build-web-all           # 交叉编译 macOS / Windows / Linux 离线 Web 版
make build-desktop-mac-m4    # 编译并校验 macOS ARM64 原生桌面端 (.app + .dmg)
make build-desktop-win-nsis  # 编译 Windows NSIS 安装包与离线单文件绿色版
make clean                   # 清理构建产物与临时缓存
```

### GitHub Actions 自动构建与发布 Release

仓库内置 [.github/workflows/build-release.yml](.github/workflows/build-release.yml)，推送 `v*` 标签后会自动在 Go 1.27 环境下构建并创建 GitHub Release：

```bash
git tag v0.4.0
git push origin v0.4.0
```

自动发布的产物包括：
- `SSH-Guard-macOS-arm64.dmg`：macOS 13+ Apple Silicon 原生桌面版安装镜像
- `SSH-Guard-Windows-amd64-installer.exe`：Windows NSIS 标准安装向导程序（无需 WebView2）
- `SSH-Guard-Windows-amd64-portable.exe`：Windows 100% 离线免安装单文件桌面版
- `ssh-guard-web-*`：macOS / Windows / Linux 多平台离线 Web 单二进制
- `SHA256SUMS.txt`：全部发布文件的 SHA-256 校验清单

也可在 GitHub **Actions** 页面手动触发工作流，仅构建并下载构建产物。

---

## 项目结构

```text
.
├── main.go                     # MyGo 纯 Go 原生桌面端入口（零 WebView、零本地端口）
├── mygo.json                   # MyGo 跨平台应用与打包配置（macOS 13+ / Windows NSIS）
├── resources/icon.png          # 跨平台应用图标
├── cmd/web/main.go             # 独立离线 Web 单二进制服务端入口（纯 Go、零 MyGo 依赖）
├── internal/
│   ├── desktopui/              # MyGo 原生桌面 UI（解锁、多标签 Ghostty 工作区、TOFU、主机 CRUD、审计、流式回放、离线 portable 提取）
│   ├── sshproxy/               # 共享 SSH 核心会话（DialSession/LiveSession）、TOFU 校验、高危命令守卫与流式 .cast 读取器
│   ├── crypto/                 # Argon2id KEK + AES-256-GCM 信封加密保险库
│   ├── store/                  # 纯 Go SQLite 持久化层
│   ├── api/                    # Web 版 HTTP REST API、CSP 与安全中间件
│   └── auth/                   # Web 版会话 Cookie 认证管理
├── web/
│   ├── embed.go                # Web 版静态资源嵌入（go:embed）
│   └── static/                 # Web 版前端页面与离线内置 xterm.js（含时间/行号侧栏）
└── scripts/                    # 跨平台构建、离线资源拉取与 macOS 签名校验脚本
```

---

## 数据目录、渲染模式与安全建议

- **桌面端默认数据目录**：系统用户配置目录下的 `SSH-Guard/`（macOS 为 `~/Library/Application Support/SSH-Guard`，Windows 为 `%AppData%\SSH-Guard`），可通过环境变量 `SSH_GUARD_DATA_DIR` 覆盖。
- **Windows 低内存渲染与 GPU 切换**：Windows 桌面端未显式设置 `MYGO_GPU` 时默认启用 `MYGO_GPU=0`（多核 CPU 光栅化 + GDI 输出，静止 2 秒后自动释放帧缓冲），并在终端中默认关闭光标闪烁以允许空闲内存回收；若在高分辨率屏幕上更偏好 D3D11 GPU 硬件渲染，可显式设置环境变量启用：
  ```powershell
  $env:MYGO_GPU = "1"
  .\SSH-Guard.exe
  ```
- **桌面端 GC 内存预算与延迟加载**：默认设置 `GOGC=25` 与稳态 `GOMEMLIMIT=28 MiB`（解锁时 Argon2id 会临时抬升至 `96 MiB` 并在派生完成后立即归还操作系统），且 `libghostty-vt` 仅在首次建立 SSH 会话或回放录像时按需加载；高级用户仍可通过 `GOGC` / `GOMEMLIMIT` 环境变量自行覆盖。
- **Windows 绿色版运行时缓存**：`SSH-Guard-Windows-amd64-portable.exe` 首次连接 SSH 或回放录像时会将内嵌的 `ghostty-vt.dll` 经流式 SHA-256 校验并原子释放到 `%LocalAppData%\SSH-Guard\natives\ghostty-vt-<sha256前12位>.dll`；高级用户可通过环境变量 `MYGO_GHOSTTY_VT` 指定自定义路径。
- **Web 端默认数据目录**：`./data`（可通过 `-data` 或 `SSH_GUARD_DATA_DIR` 修改）。
- **部署建议**：使用 Web 版时建议仅绑定 `127.0.0.1` 或配合 Tailscale / WireGuard / Nginx HTTPS 反向代理使用，避免明文暴露在公网。

---

## License

本项目采用 [MIT License](LICENSE) 开源。
