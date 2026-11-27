# Spec Delta

## ADDED Requirements

### Requirement: Desktop-only Go 1.27+ zero-CGO build pipeline
The repository SHALL build the native desktop edition using a single root Go module targeting Go 1.27+ without requiring a C compiler toolchain (`CGO_ENABLED=0`) or any Web frontend or WebSocket server dependencies.

#### Scenario: Desktop build pins explicit framework version without Wails or Web toolchains
- **WHEN** running desktop build commands locally or in CI
- **THEN** builds use the pinned MyGo version via the Go toolchain (`go tool mygo build`) with zero dependency on the Wails CLI, `wails.json`, Web frontend toolchains, or a C compiler

#### Scenario: Root Go module excludes Web-only packages and WebSocket dependencies
- **WHEN** resolving module dependencies and running tests across `./...`
- **THEN** the module contains no Web edition entrypoint (`cmd/web`), no embedded Web static assets (`web/`), and no WebSocket server dependency (`github.com/gorilla/websocket`)

## MODIFIED Requirements

### Requirement: Release workflow publishes complete checksummed artifacts
The GitHub Actions release workflow SHALL run unit tests and publish macOS DMG, Windows NSIS installer, Windows single-file portable executable, and `SHA256SUMS.txt` on version tag pushes.

#### Scenario: Tag release produces verified artifact set
- **WHEN** a `v*` git tag is pushed or the release workflow is manually dispatched
- **THEN** all desktop build jobs execute on Go 1.27, generate `SSH-Guard-macOS-arm64.dmg`, `SSH-Guard-Windows-amd64-installer.exe`, and `SSH-Guard-Windows-amd64-portable.exe` without building Web binaries, and compute `SHA256SUMS.txt` covering every published file

## REMOVED Requirements

### Requirement: Unified Go 1.27+ zero-CGO build pipeline
**Reason**: The offline Web edition (`cmd/web`) and its four-platform Web binary build targets are retired so the repository builds only the native desktop edition.
**Migration**: Use the `Desktop-only Go 1.27+ zero-CGO build pipeline` requirement for all local and CI desktop builds.
