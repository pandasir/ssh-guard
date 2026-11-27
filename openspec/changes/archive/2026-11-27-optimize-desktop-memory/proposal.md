# Proposal

## Why

重构为 MyGo + Ghostty (`libghostty-vt`) 原生桌面端后，Windows 端在解锁并打开单个 SSH 标签时的进程私有工作集仍高达约 **56 MB**（高于旧版 Wails 禁用 GPU 时的 ~40 MB 参照值），macOS 端在高分屏下也会长期滞留多张 Metal 帧缓冲。经源码与运行时分析，核心原因在于：Windows 端默认初始化 Direct3D 11 驱动与 DXGI 交换链（常驻 25–35 MB 且无空闲回收）、终端默认开启光标闪烁（每 600 ms 强制重绘，阻断了 Windows 软渲染 2 秒 `VirtualFree` 释放帧缓冲与 macOS Metal 2 秒 `trim()` 释放显存）、`GOMEMLIMIT` 在解锁后仍固定维持 96 MiB、`ghostty-vt` 在锁屏阶段即被预加载并做全量堆读校验，以及每标签存在双层永不缩容的 `bytes.Buffer` 与偏大的 512 KB 默认回滚缓冲。本次变更旨在将 Windows 桌面端内存占用压降至 MyGo + Go 架构的合理区间（活跃约 20–28 MB、空闲 2 秒后约 16–22 MB），同时打通 macOS 空闲显存回收机制。

## What Changes

- **分平台原生渲染策略（Windows 默认 CPU 软渲染 + GDI，macOS 保持 Metal GPU）**：
  - Windows 桌面端在未显式设置 `MYGO_GPU` 时默认启用 `MYGO_GPU=0`（走 MyGo 多核脏矩形 CPU 光栅化 + Win32 GDI `SetDIBitsToDevice`），彻底避免加载 D3D11/DXGI 显卡驱动与多缓冲交换链，同时保留通过 `MYGO_GPU=1` 手动切换回 GPU 渲染的能力。
  - macOS 桌面端继续保留 Metal GPU 渲染（避免 `MYGO_GPU=0` 下每帧 `CFDataCreate` 全屏拷贝 Retina 位图），依托空闲回收机制降低常驻显存。
  - 桌面端顶部状态栏按实际渲染后端动态展示，不再硬编码 `Ghostty GPU`。
- **默认关闭终端光标闪烁并收紧单标签回滚上限**：
  - 会话终端与录像回放终端默认启用 `NoBlink: true`（静止光标），消除 600 ms 持续重绘循环，使 MyGo 窗口在静置 2 秒后能正常触发空闲回收（Windows `VirtualFree` 释放 CPU 帧缓冲、macOS Metal `trim()` 释放三缓冲 Drawable 与纹理、释放排版缓存）。
  - 将桌面端默认 `DefaultScrollbackBytes` 从 `512 KB` 下调至 `256 KB`（并支持通过配置按需调至 `128 KB`），降低多标签堆外内存累积。
- **动态 Go 内存上限与更紧凑的 GC / 回收策略**：
  - 将桌面端稳态默认 GC 调优为更紧凑配置（`GOGC=25`、稳态 `GOMEMLIMIT` 约 24–32 MiB）；仅在 Argon2id 派生 KEK 的短暂解锁窗口内临时放宽上限，并在解锁完成后立即恢复稳态上限与调用 `debug.FreeOSMemory()`。
  - 在关闭录像回放与审计/录像弹窗释放列表后主动回收空闲内存，并复建单实例 `ui.Theme` 消除逐帧主题分配。
- **原生终端库延迟加载与便携版流式哈希校验**：
  - 将便携版 `ghostty-vt.dll` 解压校验与 `terminal.Load()` 推迟至首次建立 SSH 会话或启动录像回放之前执行（`sync.Once` 懒加载），使锁屏页与纯主机管理场景零原生终端库开销。
  - 将便携版缓存 DLL 的 SHA-256 校验改为流式 `io.Copy`，消除启动期一次性 `os.ReadFile` 产生的 2.1 MB Go 堆分配。
- **瘦身 SSH 会话与录像数据通路及存储缓存**：
  - 优化 `internal/sshproxy` 的 `memStream` 与 `internal/desktopui` 的 `TabConn` 输出缓冲，限制峰值容量并在读空后自动缩容（消除高吞吐输出后的永久性 256–512 KB 死内存）；复用 `CastRecorder` 写入缓冲并节流回放进度条 UI 刷新。
  - 收紧 SQLite 本地缓存（`cache_size(-256)`、`mmap_size(0)`）并在锁定保险库时执行 `PRAGMA shrink_memory`。
- **补齐 OS 级物理内存回归验证**：
  - 扩展桌面端内存测试，除 Go `HeapAlloc` 外补充 OS 物理内存/工作集观测，防止后续改动引入堆外内存回退。

## Capabilities

### New Capabilities
<!-- 本次变更不新增独立系统能力，全部属于对既有桌面原生 UI 与打包运行时能力的内存行为修订 -->

### Modified Capabilities
- `desktop-native-ui`: 调整桌面端渲染策略与内存回收契约——支持分平台渲染后端（Windows 默认低内存 CPU+GDI、支持 `MYGO_GPU=1` 启用 GPU；macOS 保持 Metal GPU），终端与回放器默认采用静止光标（`NoBlink`）以启用 2 秒空闲帧缓冲回收，收紧默认每标签回滚上限至 `256 KB` 与稳态内存上限，并将原生终端引擎推迟至首次会话/回放时懒加载。
- `desktop-packaging`: 调整 Windows 离线便携版（`portable.exe`）的原生库提取时机与校验方式——由冷启动立即提取调整为首次初始化终端子系统前按需提取，并采用流式 SHA-256 校验避免全文件读入堆内存。

## Impact

- **受影响代码**：
  - 入口与运行时调优：`main.go`、`main_test.go`、`internal/crypto/vault.go`、`internal/store/store.go`
  - 桌面端 UI 与终端生命周期：`internal/desktopui/app.go`、`internal/desktopui/workspace.go`、`internal/desktopui/session_tab.go`、`internal/desktopui/audit_replay.go`、`internal/desktopui/portable_extract.go`、`internal/desktopui/desktopui_test.go`
  - 共享 SSH 数据通路（保持外部契约与离线 Web 版行为不变）：`internal/sshproxy/session.go`、`internal/sshproxy/recorder.go`
- **兼容性与体验影响**：
  - Windows 端默认 CPU+GDI 软渲染显著降低内存（目标降幅 ~30 MB+）；若用户在 4K 高频全屏 TUI 下需要更高帧率，可通过环境变量 `MYGO_GPU=1` 随时切回 D3D11 硬件渲染。
  - 终端光标默认不闪烁（静止块），空闲 2 秒后 Windows/macOS 均可自动释放帧缓冲；离线 Web 版（`cmd/web`）不受影响。
