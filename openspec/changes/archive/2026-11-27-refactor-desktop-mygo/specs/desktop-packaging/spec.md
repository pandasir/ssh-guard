# Spec Delta

## Purpose

Defines the zero-CGO Go 1.27+ build, signing, and multi-platform release packaging contract for the native desktop edition and offline Web edition across macOS and Windows.

## ADDED Requirements

### Requirement: Unified Go 1.27+ zero-CGO build pipeline
The repository SHALL build both the native desktop edition and all offline Web edition binaries using a single root Go module targeting Go 1.27+ without requiring a C compiler toolchain (`CGO_ENABLED=0`).

#### Scenario: Web edition remains pure Go and decoupled from desktop UI dependencies
- **WHEN** building the offline Web edition for `darwin/arm64`, `windows/amd64`, `windows/arm64`, and `linux/amd64` with `CGO_ENABLED=0`
- **THEN** all four Web binaries compile cleanly without importing desktop UI packages or native terminal dynamic libraries

#### Scenario: Desktop build pins explicit framework version and removes Wails toolchain
- **WHEN** running desktop build commands locally or in CI after migration cutover
- **THEN** builds use the pinned MyGo version via the Go toolchain (`go tool mygo build`) with zero dependency on the Wails CLI or `wails.json`

### Requirement: macOS 13+ Apple Silicon desktop app and DMG packaging with native dylib signing
The macOS desktop build SHALL target macOS 13.0 or later, bundle and sign the required native terminal dynamic library (`libghostty-vt.dylib`) inside `SSH-Guard.app`, and produce a distributable `.dmg` installer image.

#### Scenario: macOS bundle declares minimum version 13.0 and passes strict signature verification
- **WHEN** building and signing `SSH-Guard.app` on macOS Apple Silicon
- **THEN** the app bundle metadata sets `LSMinimumSystemVersion` to `13.0`, bundles `libghostty-vt.dylib`, passes `codesign --verify --deep --strict`, and packages into `SSH-Guard-macOS-arm64.dmg` with an `/Applications` drag-and-drop symlink

### Requirement: Windows NSIS single-file installer packaging
The Windows desktop build SHALL produce a single-file NSIS installer executable (`SSH-Guard-Windows-amd64-installer.exe`) containing the desktop executable and `ghostty-vt.dll`, without requiring WebView2 Runtime on target Windows 10 (1809+) or Windows 11 machines.

#### Scenario: Windows NSIS installer bundles runtime library and installs cleanly
- **WHEN** a user runs `SSH-Guard-Windows-amd64-installer.exe` on Windows 10 (1809+) or Windows 11 without WebView2 Runtime installed
- **THEN** the installer deploys both `ssh-guard.exe` and `ghostty-vt.dll` to the installation directory, creates shortcuts and uninstaller entries, and the installed app launches natively

### Requirement: Windows 100% offline single-file portable executable
The Windows release pipeline SHALL also produce a standalone, zero-install single-file portable executable (`SSH-Guard-Windows-amd64-portable.exe`) that operates 100% offline with no accompanying external DLL files required beside the executable.

#### Scenario: First offline launch extracts verified native library atomically
- **WHEN** a user copies only `SSH-Guard-Windows-amd64-portable.exe` to an offline Windows 10 (1809+) / 11 machine and launches it without `MYGO_GHOSTTY_VT` preset
- **THEN** before initializing the terminal subsystem, the executable verifies or atomically writes the embedded `ghostty-vt.dll` (temp file + rename) to `%LocalAppData%\SSH-Guard\natives\ghostty-vt-<sha256-prefix>.dll`, sets `MYGO_GHOSTTY_VT` to that path, and starts the desktop UI cleanly

#### Scenario: Subsequent launches reuse cached library without rewriting
- **WHEN** `SSH-Guard-Windows-amd64-portable.exe` is launched again and the target cached DLL already exists with a matching SHA-256 digest
- **THEN** the executable skips extraction and loads the cached DLL directly

#### Scenario: Explicit environment override takes precedence
- **WHEN** `MYGO_GHOSTTY_VT` is already set in the environment prior to launch
- **THEN** the portable executable respects the user-supplied path and does not overwrite it

### Requirement: Release workflow publishes complete checksummed artifacts
The GitHub Actions release workflow SHALL run unit tests and publish macOS DMG, Windows NSIS installer, Windows single-file portable executable, four-platform offline Web binaries, and `SHA256SUMS.txt` on version tag pushes.

#### Scenario: Tag release produces verified artifact set
- **WHEN** a `v*` git tag is pushed or the release workflow is manually dispatched
- **THEN** all build jobs execute on Go 1.27, generate the full matrix of desktop and Web artifacts, and compute `SHA256SUMS.txt` covering every published file
