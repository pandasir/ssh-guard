# Design

## Context

See `proposal.md` for motivation and scope. Currently, the repository maintains two separate UIs and delivery targets in one Go module:

- **Desktop edition**: `main.go` -> `internal/desktopui` (MyGo + `libghostty-vt`) -> direct Go calls to `internal/sshproxy.DialSession`, `internal/store`, and `internal/crypto`.
- **Web edition**: `cmd/web/main.go` -> `internal/api` + `internal/auth` + `web/` (`go:embed` HTML/CSS/JS + `xterm.js`) -> `internal/sshproxy.ServeSession` (`github.com/gorilla/websocket`) -> `internal/sshproxy.DialSession`.

Removing the Web edition touches Go packages, `go.mod`, build scripts, GitHub Actions CI, and `README.md`, while leaving the desktop runtime and local storage format completely intact.

## Goals / Non-Goals

**Goals:**
- Remove all Web-only source directories, static assets, vendor download scripts, HTTP/WebSocket transport adapters, and unused Go dependencies.
- Keep `internal/sshproxy` focused exclusively on direct in-process SSH session execution (`DialSession`, `LiveSession`, TOFU/MITM verification, `CommandGuard`, and `.cast` recording/replay).
- Simplify local build tooling (`Makefile`, `scripts/build-mac-m4.sh`, `scripts/build-windows.ps1`), CI release workflow (`.github/workflows/build-release.yml`), and `README.md` to reflect a Desktop-only repository.
- Ensure all remaining unit tests (`go test -v -race ./...`) and `go tool mygo vet .` pass cleanly.

**Non-Goals:**
- Changing desktop UI features, visual layout, or memory-budget settings (`GOGC`, `GOMEMLIMIT`, `MYGO_GPU`).
- Modifying the SQLite database schema, Argon2id/AES-256-GCM vault format, or `.cast` recording format.
- Adding Linux desktop packaging or modifying previously archived OpenSpec changes.

## Decisions

### 1. Strip WebSocket transport from `internal/sshproxy` while preserving `BridgeConfig` and `buildAuthMethods`
- **Decision**: Remove `WSMessage`, `Transport`, `WSTransport`, `NewWSTransport`, `ServeSession`, and `ServeTransport` (along with `TestServeTransportBridgesWebProtocolOverLiveSession` and `chanTransport` in `internal/sshproxy/session_test.go`), consolidate `BridgeConfig` and `buildAuthMethods` into `internal/sshproxy/session.go` (or a dedicated auth helper file without `gorilla/websocket`), and delete `internal/sshproxy/proxy.go`.
- **Rationale**: `internal/desktopui/session_tab.go` and `internal/sshproxy/session.go` only depend on `BridgeConfig`, `DialSession`, `LiveSession`, and `buildAuthMethods`. Removing the WebSocket adapter eliminates the sole remaining import of `github.com/gorilla/websocket` outside `internal/api`.
- **Alternative considered**: Retaining the abstract `Transport` interface and `ServeTransport` for potential future remote protocols. Rejected because there is no active consumer after removing `cmd/web`, leaving dead code and tests to maintain.

### 2. Remove `cmd/web/`, `web/`, `internal/api/`, `internal/auth/`, and `scripts/fetch-vendor.*`
- **Decision**: Delete these Web-exclusive packages and scripts entirely, update the stale doc comment in `internal/desktopui/idle_guard.go` that references `internal/auth`, and run `go mod tidy` to remove `github.com/gorilla/websocket` from `go.mod` and `go.sum`.
- **Rationale**: Desktop inactivity and max-lifetime locking is already self-contained in `internal/desktopui/idle_guard.go`. No desktop package imports `cmd/web`, `web`, `internal/api`, or `internal/auth`.

### 3. Streamline local build scripts and GitHub Actions release workflow to Desktop-only
- **Decision**:
  - `Makefile`: Remove `vendor`, `run-web`, and `build-web-*` targets; keep `tidy`, `test`, `vet`, `dev-desktop`, `build-desktop-mac-m4`, `fix-mac-sign`, `build-desktop-win`, `build-desktop-win-nsis`, and `clean`.
  - `scripts/build-mac-m4.sh`: Remove `fetch-vendor.sh` and `cmd/web` cross-compilation steps, renumber steps cleanly, ensure `mkdir -p bin` remains before copying `bin/SSH-Guard-macOS-arm64.dmg`, and update final summary output.
  - `scripts/build-windows.ps1`: Remove `fetch-vendor.ps1` and `cmd/web` build steps, renumber steps cleanly, and update final summary output.
  - `.github/workflows/build-release.yml`: Rename workflow to `Build & Release (macOS DMG + Windows EXE)`, remove the `build-web` job, and update `release.needs` to `[build-desktop-mac, build-desktop-windows]`.
- **Rationale**: Eliminates dead CI execution time and broken steps referencing deleted files while keeping macOS ARM64 DMG and Windows x64 NSIS + portable builds unchanged.

### 4. Rewrite `README.md` for Desktop-only positioning
- **Decision**: Update `README.md` top-to-bottom to remove Web edition branding, the dual-edition comparison table, the Web branch in the Mermaid architecture diagram, Web release/CLI flag/deployment sections, Web Makefile/script examples, Web environment variable notes, and deleted directories in the project tree.
- **Rationale**: Keeps user documentation truthful to the repository and avoids confusion about unsupported Web commands or release binaries.

## Risks / Trade-offs

- **Existing Web edition users lose future updates** → Mitigation: Mark the removal as **BREAKING** in the proposal and release notes; earlier Git tags remain available for anyone needing the legacy Web binary.
- **Accidental regression in `scripts/build-mac-m4.sh` if `bin/` is not created before copying DMG** → Mitigation: Explicitly preserve `mkdir -p bin` before `cp -f "$DMG_SRC" bin/SSH-Guard-macOS-arm64.dmg` and syntax-check scripts (`bash -n`).
- **Residual references in `openspec/specs/` main specs** → Mitigation: Sync delta specs for `ssh-session-core` and `desktop-packaging`, and update main spec `Purpose` lines when syncing/archiving so specs remain consistent with the Desktop-only codebase.

## Migration Plan

1. Delete Web packages, static assets, vendor scripts, and any leftover untracked `bin/ssh-guard-web-*` binaries.
2. Refactor `internal/sshproxy` to drop WebSocket bridging while keeping `BridgeConfig` and `buildAuthMethods`, then run `go mod tidy`.
3. Update `Makefile`, `scripts/build-mac-m4.sh`, `scripts/build-windows.ps1`, `.github/workflows/build-release.yml`, and `README.md`.
4. Verify with `go test -v -race ./...`, `go tool mygo vet .`, `bash -n scripts/build-mac-m4.sh`, and grep checks for leftover Web references.
5. No user data migration is required: existing desktop SQLite databases (`ssh_guard.db`) and `.cast` recordings continue to work unchanged.
