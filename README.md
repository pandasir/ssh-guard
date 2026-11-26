# SSH-Guard

基于 Go 构建的轻量级零信任 SSH 管理面板，提供 **Web 单文件版** 与 **macOS / Windows 原生桌面版**，支持 100% 离线运行。

## 核心特性

- **零信任信封加密保险库**：主密码仅在内存中经 **Argon2id** 派生 KEK（永不落盘），每条主机凭据使用独立随机 DEK 与 **AES-256-GCM** 加密存入 SQLite；握手结束、手动锁定或退出应用时立即清零内存。
- **纯进程内 IPC（桌面端零端口暴露）**：基于 Wails v2 原生桌面端通过进程内 IPC 与原生事件通道驱动 API 与 SSH 会话，不在本机监听任何 HTTP/WebSocket 端口，规避本地端口暴露、CORS 与代理软件干扰。
- **100% 离线内置终端**：前端资源与 `xterm.js` 全部经 `go:embed` 静态嵌入二进制，无外部 CDN 依赖，支持 `vim`、`htop`、`tmux` 等全屏 TUI；Web 端默认启用严格 CSP 策略。
- **TOFU + 防 MITM 主机指纹校验**：首次连接记录并核对服务器 SHA-256 公钥指纹，后续若指纹发生变化则立即阻断连接并记录 `CRITICAL` 级安全告警。
- **高危命令实时拦截守卫**：在终端输入层实时检测并拦截 `rm -rf /`、`mkfs`、`dd of=/dev/...`、`chmod -R 777 /`、Fork 炸弹、`reboot/shutdown`、`iptables -F` 等高危操作。
- **标准会话录像与回放**：自动以 `asciinema v2 (.cast)` 格式保存操作录像（跳过无回显密码输入），支持面板内直接倍速回放。
- **零 CGO 纯 Go 构建**：采用纯 Go SQLite 驱动，支持跨平台交叉编译与低内存占用运行。

---

## 快速开始

### 环境要求

- Go 1.22+
- 如需编译桌面版，需要 [Wails v2 CLI](https://wails.io/)（构建脚本会自动检测并安装）

### 克隆仓库

```bash
git clone https://github.com/pandasir/ssh-guard.git
cd ssh-guard
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

# 一键完成测试、Web 版编译、macOS 桌面端 (.app) 编译与 Ad-hoc 签名
./scripts/build-mac-m4.sh

# 如需同时生成 .dmg 安装镜像
MAKE_DMG=1 ./scripts/build-mac-m4.sh

# 启动桌面版
open build/bin/SSH-Guard.app

# 或启动 Web 版
./bin/ssh-guard-web-darwin-arm64 -open
```

> 若从其他设备或 GitHub Actions 下载 `.app` 遇到 macOS Gatekeeper 拦截提示，可执行：
> ```bash
> ./scripts/fix-mac-gatekeeper.sh /路径/到/SSH-Guard.app
> ```

### macOS 交叉编译 Windows 桌面端与 NSIS 安装包

```bash
# 安装 NSIS 打包工具
brew install makensis

export PATH="$(go env GOPATH)/bin:$PATH"

# 生成绿色单文件与带安装向导的 NSIS 安装包
wails build -skipbindings -platform windows/amd64 -nsis
```

产物位置：
- `build/bin/SSH-Guard-amd64-installer.exe`：带安装向导、快捷方式与卸载程序的安装包
- `build/bin/ssh-guard.exe`：免安装绿色单文件桌面版

### Windows (PowerShell)

```powershell
# 一键完成测试、Web 版与 Windows 原生桌面版 (.exe) 编译
.\scripts\build-windows.ps1

# 启动桌面版
.\build\bin\ssh-guard.exe

# 或启动 Web 版
.\bin\ssh-guard-web-windows-amd64.exe -open
```

### Makefile 快捷命令

```bash
make test                    # 运行单元测试
make run-web                 # 启动本地 Web 版
make build-web-all           # 交叉编译 macOS / Windows / Linux Web 版
make build-desktop-mac-m4    # 编译 macOS ARM64 桌面端
make build-desktop-win-nsis  # 编译 Windows 桌面端与 NSIS 安装包
make clean                   # 清理构建产物
```

### GitHub Actions 云端构建

仓库内置 [.github/workflows/build-release.yml](.github/workflows/build-release.yml)，支持在 GitHub **Actions** 页面手动触发，或在推送 `v*` 标签时自动构建 macOS 桌面版、Windows 桌面版与多平台 Web 版产物。

---

## 项目结构

```text
.
├── main.go                     # Wails 原生桌面端入口（进程内 IPC 与事件桥接）
├── cmd/web/main.go             # Web 单二进制服务端入口
├── internal/
│   ├── api/                    # HTTP REST API、CSP 与安全中间件
│   ├── auth/                   # 会话令牌与认证管理
│   ├── crypto/                 # Argon2id KEK + AES-256-GCM 信封加密保险库
│   ├── sshproxy/               # SSH 代理、TOFU 指纹校验、高危命令守卫与会话录像
│   └── store/                  # 纯 Go SQLite 持久化层
├── web/
│   ├── embed.go                # 静态资源嵌入（go:embed）
│   └── static/                 # 前端页面与离线内置 xterm.js
├── scripts/                    # 跨平台构建、离线资源拉取与签名脚本
└── build/                      # Wails 应用图标、清单与安装包模板
```

---

## 数据目录与安全建议

- **桌面端默认数据目录**：系统用户配置目录下的 `SSH-Guard/`（macOS 为 `~/Library/Application Support/SSH-Guard`，Windows 为 `%AppData%\SSH-Guard`），可通过环境变量 `SSH_GUARD_DATA_DIR` 覆盖。
- **Web 端默认数据目录**：`./data`（可通过 `-data` 或 `SSH_GUARD_DATA_DIR` 修改）。
- **部署建议**：使用 Web 版时建议仅绑定 `127.0.0.1` 或配合 Tailscale / WireGuard / Nginx HTTPS 反向代理使用，避免明文暴露在公网。

---

## License

本项目采用 [MIT License](LICENSE) 开源。
