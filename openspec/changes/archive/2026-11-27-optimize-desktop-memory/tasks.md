# Tasks

## 1. Go Runtime Memory Budget, Vault Headroom & SQLite Tuning

- [x] 1.1 Update `main.go` and `main_test.go` to default `MYGO_GPU=0` on Windows when unset (preserving macOS/Linux defaults and respecting explicit `MYGO_GPU` overrides), set default `GOGC=25` and steady-state `GOMEMLIMIT=28 MiB`, remove eager `desktopui.EnsurePortableRuntime()` from startup, and verify rendering/GC default selection and env-override precedence with `go test -v .`.
- [x] 1.2 Update `DeriveKEK()` in `internal/crypto/vault.go` to temporarily raise a tight finite `GOMEMLIMIT` (< 96 MiB) to `96 MiB` during Argon2id execution (with concurrency safety), restore the prior limit on exit, and call `debug.FreeOSMemory()`; add unit tests in `internal/crypto/vault_test.go` verifying limit restoration and memory reclamation with `go test -v -race ./internal/crypto/...`.
- [x] 1.3 Tighten SQLite pragmas in `internal/store/store.go` to `cache_size(-256)` and `mmap_size(0)` and add `Store.ShrinkMemory()` executing `PRAGMA shrink_memory`; verify store open, CRUD, and `ShrinkMemory()` with `go test -v ./internal/...`.

## 2. SSH Session Stream & Recorder Buffer Slimming (`internal/sshproxy`)

- [x] 2.1 Update `memStream` in `internal/sshproxy/session.go` to cap buffered bytes at `64 * 1024` and release oversized backing slices when drained (`Len() == 0 && Cap() > 16*1024`); add burst-and-drain unit tests in `internal/sshproxy/session_test.go` verifying capacity shrinks back after draining via `go test -v -race ./internal/sshproxy/...`.
- [x] 2.2 Refactor `CastRecorder.RecordOutput` in `internal/sshproxy/recorder.go` to reuse a bounded line formatting buffer (shrinking when oversized) while preserving exact asciinema v2 JSON output; verify compatibility and allocation stability with `go test -v -race ./internal/sshproxy/...`.

## 3. Native Desktop UI Rendering, Lazy Terminal Runtime & Idle Reclamation (`internal/desktopui`)

- [x] 3.1 Refactor `fileMatchesSHA256` in `internal/desktopui/portable_extract.go` to verify cached DLL digests via streaming `os.Open` + `io.Copy(sha256.New(), f)` instead of `os.ReadFile`, and add lazy `App.ensureTerminalRuntime()` (`EnsurePortableRuntime()` + `terminal.Load()`) in `internal/desktopui/app.go` invoked on first `connectHost` or `startReplay` (removing eager `terminal.Load()` from `App.Run()` while keeping `Options.SkipLoadVT`); verify lazy loading and portable extraction tests pass via `go test -v -race ./internal/desktopui/...`.
- [x] 3.2 Update `internal/desktopui/app.go`, `workspace.go`, `session_tab.go`, and `audit_replay.go` to cache `App.theme` once in `NewApp()`, tighten `DefaultScrollbackBytes` to `256 * 1024`, enable `NoBlink: true` on both session and replay terminals, cap and shrink `TabConn.outBuf` (`64 * 1024` max, reset when drained above `16 * 1024`), throttle replay progress `runUI` refreshes to `200 ms`, invoke `store.ShrinkMemory()` on `LockVault()` and `debug.FreeOSMemory()` on `stopReplay()` and audit/recording modal dismissal, and display the active rendering backend (`Ghostty CPU` vs `Ghostty GPU`) in the workspace header; verify with `go test -v -race ./internal/desktopui/...` and `go tool mygo vet .`.
- [x] 3.3 Add platform-specific OS physical memory / private working set inspection helpers in `internal/desktopui` tests (macOS `ri_phys_footprint`, Windows `PrivateWorkingSetSize`), extend `TestTenGhosttyTerminalsBoundedScrollbackMemory` to report both Go `HeapAlloc` and OS physical footprint, and update `README.md` to document Windows low-memory CPU default rendering and `MYGO_GPU=1` opt-in; verify via `go test -v -run TestTenGhosttyTerminalsBoundedScrollbackMemory ./internal/desktopui/`.

## 4. Cross-Platform Integration & Memory Verification

- [x] 4.1 Run full test and build verification across all targets (`go test -v -race ./...`, `go tool mygo vet .`, `make build-web-all`, `make build-desktop-mac-m4`, and `make build-desktop-win-nsis`), verifying zero Web edition regressions, valid macOS `codesign --verify --deep --strict`, and clean Windows NSIS + offline `portable.exe` outputs.
- [x] 4.2 Verify empirical runtime memory and interactivity on Windows (`SSH-Guard.exe` and `portable.exe` unlocked + 1 SSH tab targeting ~20–28 MB active / ~16–22 MB after 3s idle with `MYGO_GPU=0`, smooth `vim`/`htop`/`cat`, and `MYGO_GPU=1` override) and on macOS (`SSH-Guard.app` confirming Metal `trim()` releases idle drawables after >2s focused idle in Activity Monitor).
