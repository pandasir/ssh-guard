# desktop-packaging Specification

## Purpose

Defines the zero-CGO Go 1.27+ build, signing, and multi-platform release packaging contract for the native desktop edition and offline Web edition across macOS and Windows.

## Requirements

### Requirement: Desktop-only Go 1.27+ zero-CGO build pipeline
The repository SHALL build the native desktop edition using a single root Go module targeting Go 1.27+ without requiring a C compiler toolchain (`CGO_ENABLED=0`) or any Web frontend or WebSocket server dependencies.

#### Scenario: Desktop build pins explicit framework version without Wails or Web toolchains
- **WHEN** running desktop build commands locally or in CI
- **THEN** builds use the pinned MyGo version via the Go toolchain (`go tool mygo build`) with zero dependency on the Wails CLI, `wails.json`, Web frontend toolchains, or a C compiler

#### Scenario: Root Go module excludes Web-only packages and WebSocket dependencies
- **WHEN** resolving module dependencies and running tests across `./...`
- **THEN** the module contains no Web edition entrypoint (`cmd/web`), no embedded Web static assets (`web/`), and no WebSocket server dependency (`github.com/gorilla/websocket`)

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
The Windows release pipeline SHALL also produce a standalone, zero-install single-file portable executable (`SSH-Guard-Windows-amd64-portable.exe`) that operates 100% offline with no accompanying external DLL files required beside the executable, extracting and verifying its embedded native terminal library lazily with streaming hashing only when the terminal subsystem is first initialized.

#### Scenario: First offline launch extracts verified native library atomically
- **WHEN** a user copies only `SSH-Guard-Windows-amd64-portable.exe` to an offline Windows 10 (1809+) / 11 machine, launches it without `MYGO_GHOSTTY_VT` preset, and initializes the terminal subsystem for the first time (by opening an SSH session or starting a recording replay)
- **THEN** before loading the terminal subsystem (and without extracting prematurely on the vault lock screen), the executable verifies or atomically writes the embedded `ghostty-vt.dll` (temp file + rename) to `%LocalAppData%\SSH-Guard\natives\ghostty-vt-<sha256-prefix>.dll`, sets `MYGO_GHOSTTY_VT` to that path, and initializes the terminal cleanly

#### Scenario: Subsequent launches reuse cached library without rewriting
- **WHEN** `SSH-Guard-Windows-amd64-portable.exe` initializes the terminal subsystem again and the target cached DLL already exists with a matching file size and SHA-256 digest
- **THEN** the executable verifies the cached file's digest using streaming I/O without allocating a full-file heap buffer, skips extraction, and loads the cached DLL directly

#### Scenario: Explicit environment override takes precedence
- **WHEN** `MYGO_GHOSTTY_VT` is already set in the environment prior to launch
- **THEN** the portable executable respects the user-supplied path and does not overwrite it

### Requirement: Release workflow publishes complete checksummed artifacts
The GitHub Actions release workflow SHALL run unit tests and publish macOS DMG, Windows NSIS installer, Windows single-file portable executable, signed in-app update archives, available delta patches, platform update manifests, and `SHA256SUMS.txt` on version tag pushes.

#### Scenario: Tag release produces verified artifact set
- **WHEN** a `v*` git tag is pushed or the release workflow is manually dispatched
- **THEN** all desktop build jobs execute on Go 1.27, generate `SSH-Guard-macOS-arm64.dmg`, `SSH-Guard-Windows-amd64-installer.exe`, `SSH-Guard-Windows-amd64-portable.exe`, signed platform update archives (`ssh-guard-*-darwin-arm64.tar.gz` and `ssh-guard-*-windows-amd64.tar.gz`), applicable `.delta` patches from prior signed releases, and platform manifests (`update-darwin-arm64.json` and `update-windows-amd64.json`) without building Web binaries, and compute `SHA256SUMS.txt` covering every published file

### Requirement: Ed25519-signed update feed configuration and portable build isolation
The desktop build configuration SHALL embed the Ed25519 update verification public key and GitHub Releases feed reference, source release notes from a root `CHANGELOG.md`, sign update artifacts only via out-of-repo signing credentials, and isolate the Windows single-file portable build from overwriting Windows NSIS update manifests.

#### Scenario: Build metadata embeds public key and extracts release notes from CHANGELOG.md
- **WHEN** building release desktop artifacts with the repository configuration and signing key supplied via `MYGO_UPDATER_PRIVATE_KEY`
- **THEN** the built macOS `.app` and Windows executables embed the configured Ed25519 public key and `pandasir/ssh-guard` GitHub Releases feed, the private signing key is never committed to the repository, and generated `update-*.json` manifests include the matching version section from `CHANGELOG.md`

#### Scenario: Windows portable build does not overwrite or collide with NSIS update manifests
- **WHEN** the Windows release pipeline builds both the NSIS installer and the single-file portable executable (`-tags=mygo_nowebview,portable`)
- **THEN** only the Windows NSIS build generates and signs `ssh-guard-*-windows-amd64.tar.gz`, Windows `.delta` patches, and `update-windows-amd64.json`, while the portable build runs without signing-key emission so it neither collides with the NSIS manifest nor triggers directory-based self-replacement
