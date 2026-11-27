# Design

## Context

See [proposal.md](./proposal.md) for motivation and problem statement.

Current runtime footprint analysis on `github.com/egoist/mygo v0.10.0` + `libghostty-vt` (measured at ~56 MB Private Working Set on Windows after vault unlock with 1 active SSH tab):
- **Windows D3D11 + DXGI (`~25–35 MB`)**: `mygo/internal/gpu/d3d11` loads the user-mode graphics driver (`D3D11CreateDevice`), compiles/creates shader objects, and allocates 2–3 DXGI flip-sequential backbuffers with no idle trimming mechanism. By contrast, `MYGO_GPU=0` skips `d3d11.dll`/`dxgi.dll` completely, renders via multi-core dirty-rect CPU rasterization (`internal/raster`) to Win32 GDI `SetDIBitsToDevice`, allocates a single `VirtualAlloc` framebuffer outside the Go heap (~4.2 MB at 100% DPI / ~9.4 MB at 150% DPI), and calls `VirtualFree(MEM_RELEASE)` after 2 seconds of idle (`windowHost.idle()`).
- **Cursor blinking blocks 2-second idle reclamation (`~4–9 MB` on Windows CPU mode; `~50–90 MB` on macOS Metal)**: `plugins/terminal/view.go` schedules a redraw every 600 ms when focused unless `NoBlink: true`, preventing `windowHost.idle()` (`frameIdle = 2s`) and `metal.Renderer.trim()` (`trimAfter = 2s`, which releases 3 retained Retina drawables + GPU atlas textures) from ever firing while focused.
- **Runtime, loading, and per-tab buffers (`~6–10 MB` reclaimable)**: `main.go` keeps `GOMEMLIMIT=96MiB` and `GOGC=50` permanently just for the transient 64 MiB Argon2id unlock buffer; `EnsurePortableRuntime()` + `terminal.Load()` run eagerly before unlock and `fileMatchesSHA256` allocates a 2.1 MB heap copy via `os.ReadFile`; each SSH tab carries 512 KB off-heap scrollback plus two non-shrinking 256 KB+ `bytes.Buffer`s (`LiveSession.memStream` and `TabConn.outBuf`); `App.View` allocates a fresh `ui.Theme` every frame; `CastRecorder` allocates per-chunk JSON slices; and replay triggers `runUI` on every `.cast` event.

## Goals / Non-Goals

**Goals:**
- Bring Windows desktop Private Working Set to **~20–28 MB active / ~16–22 MB idle** (unlocked + 1 SSH tab) while keeping offline single-file `portable.exe` and NSIS `installer.exe` workflows intact.
- Enable macOS Metal 2-second idle trimming (`trim()`) during focused terminal usage without degrading active Metal rendering performance.
- Bound per-tab Go heap and off-heap growth across multi-tab and high-throughput (`cat`/logs) sessions, and add OS-level memory observability to regression tests.

**Non-Goals:**
- Forking or patching `github.com/egoist/mygo` upstream (e.g., Windows DirectWrite ClearType 4-BPP `ColorAtlas`, `Atlas.Shrink`, per-terminal `view.shaped` 4096-entry LRU, or `ui.TextInput` 530 ms caret blink while actively editing remain upstream constraints that establish a ~15–20 MB Windows floor).
- Changing Web edition (`cmd/web`) HTTP/WebSocket APIs, `xterm.js` UI, or security/cryptographic parameters (Argon2id remains 64 MiB / 3 iterations / 4 threads per OWASP baseline).
- Removing the user's ability to run Windows with D3D11 GPU rendering when explicitly desired (`MYGO_GPU=1`).

## Decisions

### Decision 1: Platform-adaptive default rendering (`MYGO_GPU=0` on Windows, Metal retained on macOS)
- **Choice**: In `main.go`, before calling `app.Run()`, if `runtime.GOOS == "windows"` and `os.Getenv("MYGO_GPU") == ""`, call `os.Setenv("MYGO_GPU", "0")`. Leave `MYGO_GPU` untouched on macOS (retaining Metal) and Linux, and always respect any caller-supplied `MYGO_GPU` value (`"0"` or `"1"`). Update `workspace.go` status label to inspect `os.Getenv("MYGO_GPU") == "0"` and display `原生桌面端 · Ghostty CPU` vs `原生桌面端 · Ghostty GPU`.
- **Rationale**: Verified in `mygo/ui/window.go:647` that `MYGO_GPU` is checked lazily in `windowHost.makeGPU()` on first frame (not in any package `init()`). On Windows, our window is opaque (`BackgroundColor: "#0d1117"`, no vibrancy/WebView), so `surface.viaComposition()` is `false` and `MYGO_GPU=0` uses pure GDI `SetDIBitsToDevice` with zero D3D11/DXGI/DirectComposition overhead (-25–35 MB) and frees its `VirtualAlloc` framebuffer after 2s idle. On macOS, `darwin/surface.go:625` `PresentPixels` copies the entire Retina bitmap into a new `CFData`/`CGImage` every frame under `MYGO_GPU=0`, whereas Metal delivers superior active performance and already frees drawables via `trim()` once blinking stops.
- **Alternatives considered**:
  - `MYGO_GPU=0` on all platforms: Rejected because macOS `PresentPixels` does full-frame `CFData` copies and Metal already trims when idle.
  - Keep D3D11 on Windows and call `SetProcessWorkingSetSize(-1, -1, -1)`: Rejected because trimming Windows working set only pages active driver/VRAM mappings to standby/pagefile rather than eliminating the 25–35 MB driver allocation, and pages immediately fault back on input.

### Decision 2: Default `NoBlink: true`, non-autofocus lock screen, and `DefaultScrollbackBytes = 256 * 1024`
- **Choice**: Set `NoBlink: true` in both `connectHost` (`internal/desktopui/session_tab.go`) and `startReplay` (`internal/desktopui/audit_replay.go`), omit `.AutoFocus()` on the lock-screen password `ui.TextInput` (`internal/desktopui/unlock_view.go`), and lower `DefaultScrollbackBytes` in `internal/desktopui/app.go` from `512 * 1024` to `256 * 1024` (still configurable via `Options.ScrollbackBytes`).
- **Rationale**: `NoBlink: true` eliminates the 600 ms periodic redraw loop in `mygo/plugins/terminal/view.go:379`, and omitting lock-screen `.AutoFocus()` avoids triggering `mygo/ui/editor_layout.go`'s 30-second 530 ms caret-blink redraw loop immediately upon app launch. Together they allow `windowHost.idle()` (2s) on Windows and `metal.trim()` (2s) on macOS to fire immediately on idle—saving ~4–9 MB on Windows CPU mode and ~85 MiB on macOS Retina right after startup. Reducing scrollback from 512 KB to 256 KB cuts off-heap per-tab scrollback ceiling in half while still holding ~2,500–3,000 lines of typical terminal output (5× the Web edition's 500-line `xterm.js` scrollback).
- **Alternatives considered**:
  - Keep cursor blinking and only pause when unfocused: Rejected because users frequently read terminal logs or leave a focused SSH tab open while working in another pane or thinking, during which 50–90 MB (macOS) or 4–9 MB (Windows) would remain pinned forever.
  - Lower scrollback to 64 KB: Rejected because long build/log outputs would truncate too aggressively; 256 KB balances usability and multi-tab RSS.

### Decision 3: Dynamic `GOMEMLIMIT` around Argon2id + tighter steady-state `GOGC`
- **Choice**:
  - In `main.go`, default `GOGC` to `25` (when `GOGC` env is unset) and steady-state `GOMEMLIMIT` to `28 << 20` (28 MiB, when `GOMEMLIMIT` env is unset).
  - In `internal/crypto/vault.go` `DeriveKEK()`, introduce an internal mutex-serialized helper that temporarily raises `GOMEMLIMIT` to `max(current, 96<<20)` (only if a positive finite limit `< 96 MiB` is active) for the duration of `argon2.IDKey`, restores the previous limit immediately in a `defer`, and calls `debug.FreeOSMemory()`.
  - Also invoke `debug.FreeOSMemory()` when `stopReplay()` finishes and when dismissing audit/recording modals after clearing slices.
- **Rationale**: Keeping `GOMEMLIMIT=96 MiB` permanently leaves Go free to retain tens of megabytes of dead spans on a ~6–10 MiB live heap. Raising the limit strictly during the ~200 ms Argon2id derivation avoids GC thrashing while preserving a tight steady-state envelope, and Windows `sysUnusedOS` immediately decommits reclaimed spans via `VirtualFree(..., MEM_DECOMMIT)`.
- **Alternatives considered**:
  - Lowering Argon2id memory parameter below 64 MiB: Rejected to preserve OWASP-recommended brute-force resistance for offline master password protection.
  - Relying solely on `GOGC=25` without lowering `GOMEMLIMIT`: Rejected because bursts (e.g., opening large audit/recording views or `cat` bursts) can still transiently grow heap headroom unless bounded.

### Decision 4: Lazy `sync.Once` native terminal library initialization + streaming SHA-256 verification
- **Choice**:
  - Remove eager `desktopui.EnsurePortableRuntime()` from `main.go` and eager `terminal.Load()` from `App.Run()`.
  - Add an `App.ensureTerminalRuntime() error` method (guarded by `sync.Mutex` so a transient error is retryable while success is cached, respecting `Options.SkipLoadVT` for headless tests) that calls `EnsurePortableRuntime()` followed by `terminal.Load()`. Invoke it inside `connectHost()` and `startReplay()` before calling `terminal.New(...)`.
  - Refactor `fileMatchesSHA256()` in `internal/desktopui/portable_extract.go` to open the cached DLL and stream into `sha256.New()` via `io.Copy` (using a small 32 KB buffer) instead of `os.ReadFile(path)`.
- **Rationale**: Avoids loading the ~2 MB `libghostty-vt` library or paging in the embedded portable DLL during lock-screen / host-management-only workflows, and eliminates a 2.1 MB transient Go heap allocation during portable cache verification.
- **Alternatives considered**:
  - Keeping eager loading in `main.go` so missing-DLL errors fail fast at launch: Rejected because the NSIS installer and self-extracting `portable.exe` bundle/embed the verified DLL, and surfacing an actionable error toast on first connect/replay keeps cold startup and lock-screen memory leaner.

### Decision 5: Self-shrinking bounded SSH output streams, recorder buffer reuse, cached theme, and SQLite tuning
- **Choice**:
  - **Self-shrinking `memStream` (`internal/sshproxy/session.go`) and `TabConn.outBuf` (`internal/desktopui/session_tab.go`)**: Reduce backpressure high-water mark from `256 * 1024` to `64 * 1024`, and whenever `Read()` drains the buffer (`buf.Len() == 0`) while `buf.Cap() > 16*1024`, replace the buffer with a zero `bytes.Buffer{}` so the oversized backing array becomes immediately collectable instead of staying pinned per tab.
  - **`CastRecorder` (`internal/sshproxy/recorder.go`)**: Keep a reusable `lineBuf bytes.Buffer` on `CastRecorder` (shrinking if `Cap() > 32*1024`), formatting `[<elapsed>,"o",<json-string>]\n` with `strconv.AppendFloat` + `json.Marshal` of `string` or custom JSON string encoding to reduce per-chunk allocations during heavy output.
  - **Replay UI throttling (`internal/desktopui/audit_replay.go`)**: Throttle the `a.runUI(func() {})` progress-label refresh inside `runReplayLoop` to at most once every `200 ms` (always firing immediately on EOF/finish), letting `rc.term.Feed()` handle terminal redraws directly without rebuilding the entire MyGo widget tree on every `.cast` frame.
  - **Cached `App.theme` (`internal/desktopui/app.go`)**: Build the customized `*ui.Theme` once during `NewApp()` and pass `a.theme` in `View(c)` instead of copying `*ui.DarkTheme().WithAccent(...)` on every frame.
  - **SQLite (`internal/store/store.go`)**: Tighten DSN pragmas to `_pragma=cache_size(-256)&_pragma=mmap_size(0)` and expose `Store.ShrinkMemory()` running `PRAGMA shrink_memory`, invoked from `App.LockVault()`.
- **Rationale**: Eliminates cumulative hidden memory leaks/retention under sustained multi-tab output and high-frequency `.cast` playback without altering any external SSH or WebSocket contracts.

### Decision 6: OS-level physical memory / working set test instrumentation
- **Choice**: Add platform-specific read-only helpers in `internal/desktopui` tests (macOS `proc_pid_rusage` `ri_phys_footprint` via `purego`, Windows `K32GetProcessMemoryInfo` `PrivateWorkingSetSize` via `syscall`) to log real OS physical memory alongside `runtime.MemStats.HeapAlloc` in `TestTenGhosttyTerminalsBoundedScrollbackMemory` (and a new buffer-shrink + dynamic-GOMEMLIMIT test suite).
- **Rationale**: Prevents the blind spot where `HeapAlloc` reads ~3 MiB while off-heap C/Zig/GPU memory grows unnoticed.

## Risks / Trade-offs

- **[Windows CPU soft-rendering throughput on 4K or rapid full-screen TUI (`htop`, large `cat`)]** -> `internal/raster` uses up to 8 CPU workers with dirty-rect clipping, which handles typical interactive SSH and `vim` smoothly; document `MYGO_GPU=1` as an instant runtime escape hatch for users who prefer D3D11 GPU rendering at the cost of +25–35 MB RAM.
- **[Static non-blinking cursor may look unfamiliar to users expecting a blinking block]** -> Static block cursors are standard across many developer terminals (and remote TUI apps can still control cursor style/visibility); the 4–9 MB (Windows) and 50–90 MB (macOS) idle savings justify making `NoBlink: true` the default.
- **[Deferred `terminal.Load()` surfaces corrupt/missing DLL errors on first connect instead of launch]** -> Return clear toast error messages on load failure and ensure `EnsurePortableRuntime()` remains covered by unit tests for first-extract, cache-hit, corruption-rewrite, and `MYGO_GHOSTTY_VT` override.
- **[Lower `GOGC=25` + `GOMEMLIMIT=28 MiB` increases GC frequency under heavy log streaming]** -> Combined with `CastRecorder` buffer reuse, `App.theme` caching, and replay throttling, allocation rate drops significantly; users can still override `GOGC` / `GOMEMLIMIT` via environment variables.
- **[Upstream MyGo ~15–20 MB floor]** -> Clearly communicate in docs/comments that DirectWrite ClearType 4-BPP `ColorAtlas`, `modernc.org/sqlite`, and Go runtime form a fixed baseline below which further reduction would require changing UI frameworks or upstream MyGo.

## Migration Plan

1. Implement core runtime/buffer/storage memory optimizations (`internal/crypto`, `internal/store`, `internal/sshproxy`, `internal/desktopui`, `main.go`) in backward-compatible commits; offline Web edition (`cmd/web`) requires zero protocol or UI changes.
2. Verify all unit tests (`go test -v -race ./...`), `go tool mygo vet .`, and new memory/shrink/lazy-load tests pass on macOS and Windows.
3. Build and smoke-test Windows (`SSH-Guard.exe` + `portable.exe`) and macOS (`SSH-Guard.app`) artifacts, recording Task Manager Private Working Set (active vs 3s idle, default vs `MYGO_GPU=1`) and Activity Monitor physical footprint (focused idle >3s to confirm Metal `trim()` fires).
4. **Rollback**: Any user experiencing Windows CPU rendering lag can immediately revert rendering mode via `MYGO_GPU=1` without rebuilding; code changes are localized and reversible per component.
