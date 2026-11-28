# Changelog

All notable changes to SSH-Guard Desktop are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and each release heading (`## <version>`) is automatically extracted into the
signed in-app update manifest (`update-<target>.json`) during `go tool mygo build`.

## 0.8.0

- 全新深海暗夜原生桌面端视觉重构：升级午夜蓝主画布、高光渐变按钮、细描边层次与静态 SVG 矢量图标体系
- 重构主机侧边栏卡片（默认 22 端口精简展示、指纹矢量图标、认证方式徽章）与底部 4 栏纵向图标导航栏（录像、审计、数据目录、锁定）
- 重绘空态工作区 Hero 视图（径向深蓝光晕背景、毛玻璃质感 `>_` 终端徽标与发光主操作按钮），并统一解锁页及全部原生弹窗视觉风格

## 0.7.0

- 新增「关于 SSH-Guard」内嵌手动检查更新、Ed25519 签名校验与 bsdiff 增量在线更新（macOS `.app` 与 Windows NSIS 安装版支持一键下载验签并安全重启）
- 新增原生菜单栏「检查更新...」快捷入口（锁屏与解锁态均可使用，零后台静默联网，自动走系统代理 / `HTTPS_PROXY`）
- Windows 单文件便携版及只读安装目录自动降级为检查版本提示与更新日志，并支持一键跳转 GitHub Releases 下载
- 重启更新前自动提示活跃终端会话数量，并先执行安全关闭会话、清零内存 KEK 与关闭数据库流程

## 0.6.0

- 新增跨平台原生应用菜单栏（macOS 应用/会话/编辑/终端/视图/安全/窗口/帮助，Windows/Linux 暗色原生菜单与无冲突快捷键）
- 新增菜单驱动的清屏、清除回滚历史、终端字号缩放（10pt–28pt）、侧边栏显隐切换与原生「关于 SSH-Guard」弹窗

## 0.5.0

- 新增本地登录 Shell 多标签终端，支持独立编号、Shell 标题同步与退出后原地重启
- 优化无头测试与退出阶段 `uiMu` 同步，修复关闭竞态

## 0.4.0

- 迁移至纯 Go 原生桌面架构（MyGo + Ghostty `libghostty-vt`，零 WebView、零本地监听端口）
- 引入 28 MiB 稳态内存预算、懒加载终端引擎与 Windows 离线单文件绿色版

## 0.3.0

- 优化多标签 SSH 生命周期与终端行号/时间戳稳定性

## 0.2.0

- 新增多标签 SSH 会话支持

## 0.1.0

- 首个跨平台发布版本，支持零信任信封加密保险库、TOFU/MITM 校验、高危命令守卫与 `.cast` 会话录像
