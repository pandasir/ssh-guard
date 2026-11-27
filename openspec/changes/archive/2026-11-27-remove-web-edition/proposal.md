# Proposal

## Why

SSH-Guard currently maintains two completely separate user interfaces—a pure-Go native desktop UI (`internal/desktopui` on MyGo + `libghostty-vt`) and an embedded browser UI (`web/static` on HTML/CSS/JS + `xterm.js` served via `cmd/web`)—which doubles UI maintenance effort and causes feature drift between editions. Focusing the repository exclusively on the native desktop edition eliminates dual-UI overhead, dead HTTP/WebSocket code, and unnecessary third-party dependencies.

## What Changes

- **BREAKING**: Remove the offline Web edition entrypoint (`cmd/web/`), embedded static assets and bundled `xterm.js` (`web/`), HTTP/WebSocket API server (`internal/api/`), and Web cookie session manager (`internal/auth/`).
- **BREAKING**: Remove the WebSocket transport bridge (`WSMessage`, `Transport`, `WSTransport`, `ServeSession`, `ServeTransport`) from `internal/sshproxy/proxy.go` and its WebSocket-protocol test in `internal/sshproxy/session_test.go`, retaining `BridgeConfig` and `buildAuthMethods` for desktop `DialSession`.
- Remove Web-only third-party dependency `github.com/gorilla/websocket` from `go.mod` / `go.sum` via `go mod tidy`.
- Remove Web vendor fetch scripts (`scripts/fetch-vendor.sh`, `scripts/fetch-vendor.ps1`) and clean up any leftover local `bin/ssh-guard-web-*` artifacts.
- Update build and release tooling (`Makefile`, `scripts/build-mac-m4.sh`, `scripts/build-windows.ps1`, `.github/workflows/build-release.yml`) to build, test, and publish only the native desktop artifacts (macOS ARM64 `.dmg`, Windows x64 NSIS installer, Windows x64 offline portable `.exe`, and `SHA256SUMS.txt`).
- Update `README.md` to position SSH-Guard strictly as a native desktop application, removing Web edition sections, dual-edition comparison tables, Web CLI flags, Web build/run commands, and Web directory entries.
- Update OpenSpec capabilities (`ssh-session-core` and `desktop-packaging`) to drop Web edition compatibility, build, and release requirements.

## Capabilities

### New Capabilities
<!-- None: this change retires the Web edition rather than introducing a new capability. -->

### Modified Capabilities
- `ssh-session-core`: Remove the `Offline Web edition compatibility` requirement and narrow the session core contract to direct in-process native desktop consumption without WebSocket transport bridging.
- `desktop-packaging`: Remove offline Web edition build targets, `cmd/web` dependency-isolation checks, and four-platform Web release binaries from the build and GitHub Actions release contract.

## Impact

- **Removed directories & files**: `cmd/web/`, `web/`, `internal/api/`, `internal/auth/`, `scripts/fetch-vendor.sh`, `scripts/fetch-vendor.ps1`.
- **Modified code & tests**: `internal/sshproxy/proxy.go`, `internal/sshproxy/session_test.go`, `internal/desktopui/idle_guard.go` (comment referencing `internal/auth`).
- **Dependencies**: Removes `github.com/gorilla/websocket` from `go.mod` and `go.sum`.
- **Build, CI & docs**: `Makefile`, `scripts/build-mac-m4.sh`, `scripts/build-windows.ps1`, `.github/workflows/build-release.yml`, `README.md`.
- **Unchanged core & desktop runtime**: `main.go`, `mygo.json`, `internal/crypto/`, `internal/store/`, `internal/desktopui/` runtime behavior, and `internal/sshproxy` (`DialSession`, `LiveSession`, TOFU/MITM verification, `CommandGuard`, `.cast` recorder and streaming `CastReader`).
