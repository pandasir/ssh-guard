# Design

## Context

See [proposal.md](./proposal.md) for the problem statement and motivation.

Current codebase architecture:
- Desktop entry is root `main.go` (Wails v2, `ipcTransport` with 16ms/32KB coalescing, `httptest`-based `APIRequest`, `GOGC=50`, `GOMEMLIMIT=96MiB`, Windows `WebviewGpuIsDisabled: true`).
- Web entry is `cmd/web/main.go`, embedding `web/static/` (vanilla HTML/CSS/JS + offline `xterm.js` + `addon-fit`).
- Core packages under `internal/`: `crypto` (Argon2id + AES-256-GCM envelope vault), `store` (pure Go `modernc.org/sqlite`, single connection), `auth` (session tokens with 30m idle / 8h max lifetime), `api` (HTTP/WS handlers + desktop IPC compatibility branches), and `sshproxy` (`Transport` interface, TOFU/MITM `StrictHostKeyCallback`, `CommandGuard`, `CastRecorder`).
- Release CI (`.github/workflows/build-release.yml`) builds 4 pure-Go Web binaries (`darwin/arm64`, `windows/amd64`, `windows/arm64`, `linux/amd64`), `SSH-Guard-macOS-arm64.dmg`, `SSH-Guard-Windows-amd64-installer.exe`, and `SSH-Guard-Windows-amd64-portable.exe`.

Constraints established with the user:
- Desktop UI will be 100% pure Go Native UI (`github.com/egoist/mygo` + `ui` + `plugins/terminal`), with zero WebView.
- Desktop edition explicitly drops the left timestamp/line-number terminal gutter; Web edition keeps it.
- Single root `go.mod` upgraded to Go 1.27+.
- Windows keeps both single-file `installer.exe` (NSIS) and single-file offline `portable.exe`.
- A Phase 0 minimal PoC gate must pass on macOS and Windows before removing Wails v2.

## Goals / Non-Goals

**Goals:**
- Decouple SSH session establishment, security controls, and streaming I/O from WebSocket/Wails JSON framing so Web and Desktop share one authoritative `internal/sshproxy` core.
- Build a zero-WebView native desktop client on `mygo/ui` + `mygo/plugins/terminal` with bounded off-heap scrollback memory and full feature parity (except the intentionally omitted desktop gutter).
- Preserve every zero-trust invariant in desktop mode: just-in-time credential zeroization, strict TOFU/MITM verification with queued prompts, high-risk command blocking, independent idle/lifetime auto-lock, and shutdown cleanup.
- Maintain reproducible zero-CGO builds and single-file Windows `installer.exe` + offline `portable.exe` releases.

**Non-Goals:**
- Rewriting or changing the offline Web edition UI (`web/static/`) or its WebSocket protocol.
- Introducing new product features outside current parity (e.g., SFTP file browser, host folder trees, editable guard rule UI).
- Implementing the left timestamp/line-number gutter in `plugins/terminal`.
- Supporting macOS versions older than 13.0 or Windows versions older than Windows 10 1809 (upstream `libghostty-vt` / ConPTY floor).
- Using hybrid `ui.WebView` windows/tabs inside the native desktop app.

## Decisions

### Decision 1: Split `internal/sshproxy` into `DialSession` + `LiveSession` (`io.ReadWriteCloser` + `Resize`)
- **Choice**: Extract `DialSession(ctx, cfg) (*LiveSession, error)` and `LiveSession` into `internal/sshproxy/session.go`.
  - `DialSession` performs credential decryption + immediate `cleanupCreds()` zeroization right after `ssh.Dial`, strict TOFU/MITM checks (recording `CRITICAL` `MITM_ALERT` audit log on mismatch), PTY request, `session.Shell()`, `CastRecorder` startup, and `CONNECT` audit logging.
  - `LiveSession` pumps both `stdout` and `stderr` (plus locally injected guard warning banners) into a single synchronized internal pipe readable via `Read(p []byte)`, runs `CommandGuard` inside `Write(p []byte)` (sending `0x03`, writing `BLOCKED_CMD` audit log, recording to `.cast`, injecting the red warning banner into the output pipe, and invoking an optional `OnGuardBlock(ruleName, cmd)` hook), exposes `Resize(cols, rows int) error`, and uses `sync.Once` in `Close()` to flush/finish the recording (`store.FinishRecording`), log `DISCONNECT`, and close both `*ssh.Session` and `*ssh.Client`.
  - Existing `ServeTransport` / `ServeSession` in `internal/sshproxy/proxy.go` becomes a thin adapter around `DialSession` + `LiveSession`, translating `HostKeyUntrustedError` / `HostKeyMismatchError` and stream output back into `WSMessage` JSON frames for Web edition.
- **Rationale**: Fixes four flaws in a naive `GuardedStream` wrapper (missing `stderr`, missing inline warning/audit/recording on guard block, leaked `*ssh.Client` and missing `FinishRecording` on close, and split TOFU/MITM logic between Web and Desktop).
- **Alternatives considered**:
  - Keep `Transport` (`WSMessage`) and adapt it to `terminal.Options.Conn`: Rejected because it keeps unnecessary JSON/struct wrapping and extra allocations in the hot terminal output path.
  - Duplicate SSH dial logic in `internal/desktopui`: Rejected because security invariants (credential zeroization, MITM `CRITICAL` logging, guard rules) must have a single source of truth.

### Decision 2: Desktop `TabConn` lazy adapter for async handshake, TOFU/MITM queue, and in-tab reconnect
- **Choice**: Each desktop terminal tab creates a lightweight `TabConn` (`io.ReadWriteCloser` + `Resize(cols, rows int) error`) and immediately passes it to `terminal.New(terminal.Options{Conn: tabConn, Scrollback: 512 * 1024, OnExit: ...})`.
  - Before `DialSession` succeeds, `TabConn` buffers pre-connect status lines (`[SSH-Guard] 正在连接...`) into its output pipe and records the latest `(cols, rows)` reported by `terminal.View`, discarding keystrokes until attached.
  - A background goroutine runs `DialSession` using the measured `(cols, rows)`.
  - If `DialSession` returns `*HostKeyUntrustedError` or `*HostKeyMismatchError`, the goroutine calls `win.Update(...)` to enqueue the item into `app.tofuQueue` and open `ui.Modal`. When the user trusts the key, `store.UpsertKnownHost` + `TOFU_TRUST` audit log run and the **same tab** re-runs `DialSession` without closing/recreating the tab.
  - On success, `tabConn.Attach(liveSession)` pumps `liveSession.Read` into `TabConn`'s output stream and routes `Write`/`Resize`/`Close` directly to `liveSession`.
  - Explicit tab close calls `tabConn.Close()` and `term.Close()` to release `libghostty-vt` memory immediately.
- **Rationale**: `terminal.View` layout determines exact initial `cols`/`rows` before PTY allocation; showing status immediately in the tab avoids blank freezes during slow handshakes and cleanly supports queued TOFU/MITM modals and one-click reconnect.
- **Alternatives considered**:
  - Dial first, then call `terminal.New`: Rejected because `cols`/`rows` are unknown before view mount (causing initial TUI resize glitch) and handshake progress/errors would need a separate non-terminal status widget.

### Decision 3: 100% Native `mygo/ui` with bounded `Scrollback` and `win.Update` concurrency model
- **Choice**: Implement all desktop screens under `internal/desktopui/` using native MyGo widgets (`ui.Form`, `ui.TextInput(...).Password()`, `ui.TextArea`, `ui.RadioGroup`, `ui.Checkbox`, `ui.Sidebar`, `ui.Tabs`, `ui.Table`, `ui.Modal`, `ui.Toast`, `mygo.Dialog.Open`) and zero `ui.WebView`.
  - Explicitly cap `terminal.Options.Scrollback` (default `512 * 1024` bytes per tab, configurable via constant/setting) because `libghostty-vt` allocates scrollback off-heap outside Go's `GOMEMLIMIT=96MiB` budget (and MyGo's default is 10 MB per terminal).
  - All mutations from background goroutines (SSH dial/exit, guard block toast, `mygo.Dialog.Open` file pick, replay ticker, idle auto-lock) synchronize onto the UI thread exclusively via `win.Update(func() { ... })`.
  - Clear bound Go `string` secret fields in unlock and host forms immediately after submit/cancel.
- **Rationale**: Audit logs and recordings are simple read-only tables (`ui.Table` with virtualization is simpler and faster than embedding a WebView2/WKWebView), and avoiding `ui.WebView` eliminates WebView2 Runtime dependency and idle WebView memory retention.
- **Alternatives considered**:
  - Hybrid `ui.WebView` for audit/recordings/host forms: Rejected because it reintroduces browser child process RAM, WebView2 dependency on Windows, and desktop IPC auth glue.

### Decision 4: Independent Desktop `IdleGuard` and Dual-Hook Shutdown Cleanup
- **Choice**: Add `IdleGuard` in `internal/desktopui/` tracking `unlockedAt` and `lastActiveAt` (refreshed on UI interaction and terminal `Write` activity), with a background ticker enforcing the same 30-minute idle timeout and 8-hour max lifetime as `internal/auth/session.go`.
  - Triggering lock (manual or automatic) executes on `win.Update`: close all tabs (`tabConn.Close()` + `term.Close()`), clear host/audit/recording slices from UI state, call `vault.Lock()`, record `VAULT_LOCK` audit log, and switch back to `unlock_view`.
  - Wire shutdown cleanup in both `win.OnClosed` and a `defer app.Shutdown()` after `mygo.App.Run()` returns (guarded by `sync.Once`) to close sessions, terminals, vault, and SQLite store.
- **Rationale**: Bypassing `internal/api` + `internal/auth` in desktop mode would otherwise silently drop session expiration protection.

### Decision 5: Streaming `.cast` Reader + Native `term.Feed()` Replay Player
- **Choice**: Add `CastReader` in `internal/sshproxy/recorder.go` using `bufio.Scanner` (with enlarged max token buffer, e.g., 1 MiB, for wide terminal frames) to read the JSON header and stream `[time, "o", data]` events incrementally.
  - Desktop replay creates a read-only `terminal.New(terminal.Options{Conn: noopBlockedConn, Cols: hdr.Width, Rows: hdr.Height, Scrollback: 512 * 1024})` and feeds chunks via `term.Feed([]byte(data))` paced by a cancellable goroutine supporting pause/resume and `1x`/`2x`/`4x` speeds (capping inter-event sleep at 1.5s like current `app.js`).
  - Closing the replay modal cancels the playback context, closes the file, and calls `term.Close()`.
- **Rationale**: Eliminates full-file string buffering and JSON IPC transfer for large recordings while preserving exact TUI replay geometry.

### Decision 6: Single Root `go.mod` (Go 1.27), `mygo.json`, and Offline Single-File Windows `portable.exe`
- **Choice**:
  - Upgrade root `go.mod` to `go 1.27` and pin `github.com/egoist/mygo` to a tested release tag (verified in Phase 0) plus a `tool` directive for `go tool mygo`. Keep `cmd/web` strictly isolated from `mygo` imports.
  - Place `mygo.json` at repo root with `macos.minimumSystemVersion: "13.0"`, icon at `resources/icon.png` (copied from `build/appicon.png`), and output directory `dist/mygo` (avoiding collision with Wails `build/` during transition and cleanly replacing `build/` in Phase 4).
  - **Windows `portable.exe` packaging**:
    1. Standard build runs `go tool mygo build --platform windows/amd64`, which produces `installer.exe` (NSIS) and stages the SHA-256-verified `ghostty-vt.dll`.
    2. Build script / CI copies that exact `ghostty-vt.dll` to a gitignored embed staging path (e.g., `internal/desktopui/nativeembed/ghostty-vt.dll`).
    3. Build `portable.exe` with `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags portable -trimpath -ldflags="-s -w -H=windowsgui" -o ... .` (with a build-tagged `//go:build windows && portable` init file that embeds the DLL).
    4. At startup before `mygo.App.Run()` / `terminal.New`, if `MYGO_GHOSTTY_VT` is empty, `ensurePortableGhosttyDLL()` computes the embedded SHA-256, checks `%LocalAppData%\SSH-Guard\natives\ghostty-vt-<sha256[:12]>.dll` (verifying hash if present), atomically writes via temp file + `os.Rename` on cache miss, and calls `os.Setenv("MYGO_GHOSTTY_VT", dstPath)`.
- **Rationale**: Delivers 100% offline true single-file `portable.exe` without modifying MyGo upstream or committing binary DLLs to Git, while keeping `installer.exe` on MyGo's native NSIS pipeline.
- **Alternatives considered**:
  - Separate `desktop/go.mod`: Rejected due to multi-module `replace`/`go.work` friction for a single-repo project when CI already builds on latest Go.
  - Distributing portable as `.zip` (`exe + dll`): Rejected per user preference to preserve single-file `.exe` distribution.

### Decision 7: Phase 0 Gate PoC Before Wails Removal
- **Choice**: Build a minimal, isolated PoC entry (e.g., under a temporary branch or `cmd/desktop-poc/` deleted before Phase 4) that exercises `mygo/ui` + `plugins/terminal` + SSH dial on both macOS Apple Silicon and Windows amd64 before modifying root `main.go` or deleting Wails.
- **Gate criteria**:
  1. Smooth rendering under `cat` large output, `htop`, and `vim` on macOS and Windows.
  2. Acceptable Windows Chinese IME composition, CJK alignment, and Ctrl/Cmd copy-paste shortcuts.
  3. Bounded RSS under 5–10 active SSH sessions with `Scrollback` capped at 512 KB.
  4. Verified macOS `codesign --verify --deep --strict` with `libghostty-vt.dylib` and verified offline Windows `portable.exe` extraction + load via `MYGO_GHOSTTY_VT`.

## Risks / Trade-offs

- **[Upstream FFI crash in `libghostty-vt` terminates whole process]** -> Pin a verified `egoist/mygo` version tag after Phase 0 soak testing on macOS and Windows; keep session/vault cleanup safe on abnormal exit (SQLite WAL mode protects DB integrity, recordings flush periodically via buffered writer).
- **[Windows Defender / AV heuristic scan on unsigned `portable.exe` extracting a DLL]** -> Use deterministic SHA-256 content-addressed filename under `%LocalAppData%\SSH-Guard\natives\` (atomic rename, written only once on first launch), keep `installer.exe` (NSIS) as the primary recommended Windows download alongside `portable.exe`, and publish `SHA256SUMS.txt`.
- **[Dual UI maintenance cost between native desktop and Web edition]** -> Keep all business logic, cryptography, persistence, TOFU/MITM, guard rules, and recording format strictly in `internal/` so UI layers stay thin presentation shells.
- **[MyGo v0.x API churn]** -> Lock exact version in `go.mod` and isolate all MyGo imports inside root `main.go` and `internal/desktopui/` (zero MyGo imports in `internal/{crypto,store,sshproxy,auth,api}`).
- **[CJK punctuation width quirk on macOS (`egoist/mygo#1083`) or Windows IME edge cases]** -> Explicitly test in Phase 0 PoC on both platforms; select crisp monospace + CJK fallback font configuration in `terminal.Options` and track upstream fixes.
- **[Build-time network fetch of `libghostty-vt` by `mygo build`]** -> Document cache path and SHA-256 verification behavior in build scripts; ensure CI caches Go modules and MyGo native artifacts where helpful while keeping runtime 100% offline.

## Migration Plan

1. **Phase 0 (PoC Gate)**: Add Go 1.27 + pinned `mygo` dependency in a spike/PoC, validate macOS + Windows terminal UX, IME, memory, macOS signing, and Windows `portable.exe` self-extracting DLL. *Rollback*: If PoC fails a blocking criterion (e.g., severe Windows IME/crash blocker), abort migration with zero impact to existing Wails `main.go`.
2. **Phase 1 (Shared Core Refactor)**: Introduce `internal/sshproxy/session.go` (`DialSession` + `LiveSession`) and `CastReader`, switch `ServeTransport` / `ServeSession` to wrap it, and verify all unit tests + Web edition manually.
3. **Phase 2 & 3 (Native Desktop Implementation)**: Build `internal/desktopui/` (unlock, idle guard, sidebar, multi-tab Ghostty workspace, queued TOFU/MITM modal, host CRUD + native file picker, audit/recording tables, streaming replay) wired behind the new desktop entry.
4. **Phase 4 (Cutover & Cleanup)**: Replace root `main.go` and `main_test.go`, add `mygo.json` and `resources/icon.png`, remove `wails.json`, `web/static/wailsjs/`, Wails dependencies, and obsolete `DesktopMode` / `DesktopSecret` bridge branches from `internal/api`, `internal/auth`, and `web/static/app.js`; update `Makefile`, `scripts/`, `.github/workflows/build-release.yml`, and `README.md`.
5. **Rollback after Phase 4**: Previous Wails desktop release remains tagged on git (`v0.3.0`) and reproducible from git history if a critical regression is discovered post-cutover.

## Open Questions

- Exact `github.com/egoist/mygo` version tag to pin in `go.mod` (will be selected from the latest stable release during Phase 0 PoC after verifying macOS + Windows smoke tests).
- Exact default `Scrollback` byte cap (`256 KB` vs `512 KB`) to tune based on Phase 0 10-tab RSS measurements.
