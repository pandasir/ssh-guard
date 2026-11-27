# Tasks

## 1. Remove Web Edition Packages and WebSocket Bridge

- [x] 1.1 Delete Web-only packages and static assets (`cmd/web/`, `web/`, `internal/api/`, `internal/auth/`) along with any leftover local `bin/ssh-guard-web-*` binaries, and verify those paths no longer exist in the workspace
- [x] 1.2 Move `BridgeConfig` and `buildAuthMethods` from `internal/sshproxy/proxy.go` into `internal/sshproxy/session.go`, delete `internal/sshproxy/proxy.go`, remove `chanTransport` and `TestServeTransportBridgesWebProtocolOverLiveSession` from `internal/sshproxy/session_test.go`, and verify `go test -v -race ./internal/sshproxy/...` passes
- [x] 1.3 Update the stale `internal/auth` comment in `internal/desktopui/idle_guard.go` and run `go mod tidy` to remove `github.com/gorilla/websocket` from `go.mod` and `go.sum`, verifying `go list -m all` excludes `github.com/gorilla/websocket` and `go test -v -race ./internal/desktopui/...` passes

## 2. Update Build Scripts, CI Workflow, and Documentation

- [x] 2.1 Delete `scripts/fetch-vendor.sh` and `scripts/fetch-vendor.ps1`, remove `vendor`, `run-web`, and `build-web-*` targets from `Makefile`, and verify `make test` and `make vet` succeed
- [x] 2.2 Remove `fetch-vendor` and `cmd/web` build steps from `scripts/build-mac-m4.sh` (preserving `mkdir -p bin` before copying the DMG) and `scripts/build-windows.ps1`, renumber steps cleanly, and verify syntax via `bash -n scripts/build-mac-m4.sh` and PowerShell script review
- [x] 2.3 Remove the `build-web` job from `.github/workflows/build-release.yml`, update the workflow name to `Build & Release (macOS DMG + Windows EXE)` and `release.needs` to `[build-desktop-mac, build-desktop-windows]`, and verify no Web build or `dist-web` references remain in the workflow
- [x] 2.4 Update `README.md` to position SSH-Guard strictly as a pure-Go native desktop application (removing Web branding, dual-edition comparison table, Web Mermaid branch, Web release row, Web CLI flags, Web run/build commands, Web env notes, and deleted directories from the project tree), and verify every documented command and path matches the repository

## 3. End-to-End Verification

- [x] 3.1 Run `go test -v -race ./...`, `go tool mygo vet .`, and a codebase-wide search for `cmd/web`, `internal/api`, `internal/auth`, `fetch-vendor`, `gorilla/websocket`, and `ssh-guard-web` (excluding `openspec/changes/archive`) to confirm all tests pass and no stale Web references remain
