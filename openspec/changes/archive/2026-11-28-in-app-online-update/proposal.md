# Proposal

## Why

SSH-Guard Desktop users currently have to check GitHub Releases manually, download new installers or disk images, and overwrite existing installations by hand whenever a new version ships. Adding a manually triggered, cryptographically signed in-app update flow inside the About dialog (and native menu bar) keeps users current with minimal friction while honoring SSH-Guard's zero-trust offline posture—no silent background network calls, mandatory Ed25519 signature verification before touching disk, and clean vault/session shutdown before relaunching.

## What Changes

- Add a manual **检查更新** (Check for Updates) workflow directly inside the native **关于 SSH-Guard** dialog (`internal/desktopui/about_modal.go`), displaying check status, available version, release notes, download progress, cancellation/retry actions, and active-session safety warnings before restart.
- Add a **检查更新...** menu item under **关于 SSH-Guard** in the cross-platform native menu bar (`internal/desktopui/menu.go` — application menu on macOS, Help menu on Windows/Linux), available in both locked and unlocked vault states, that opens the About dialog and immediately starts a check.
- Integrate `mygo.Updater` (Ed25519-signed archives + bsdiff delta updates hosted on GitHub Releases `pandasir/ssh-guard`) for writable installations on macOS (`.app`) and Windows (NSIS installer), executing `App.Shutdown()` (closing active SSH/local sessions and zeroizing the vault KEK) before invoking `mygo.App.Relaunch()`.
- Support graceful fallback on Windows single-file portable builds (`-tags=portable`) and non-writable installations (such as read-only DMG mounts or non-admin directories): still check the static release manifest (`update-<goos>-<goarch>.json`) over HTTPS (respecting system proxy / `HTTPS_PROXY`), display version and release notes, explain why in-place replacement is unavailable, and offer a button to open the GitHub Releases page in the system browser.
- Add root `CHANGELOG.md` (Keep a Changelog / `## <version>` format) so `mygo build` embeds release notes into generated update manifests.
- Configure `updates` (`publicKey`, `github: "pandasir/ssh-guard"`) in `mygo.json` and update `.github/workflows/build-release.yml` to sign and upload platform `.tar.gz` archives, `.delta` patches, and `update-<target>.json` manifests via the `MYGO_UPDATER_PRIVATE_KEY` secret while ensuring the Windows portable build clears the signing key so it does not overwrite the NSIS manifest.

## Capabilities

### New Capabilities

<!-- None: all behavioral changes extend existing desktop UI and packaging capabilities. -->

### Modified Capabilities

- `desktop-native-ui`: Add manual in-app update checking, release notes display, signed download/install progress, active-session restart confirmation, portable/read-only browser-download fallback inside the About dialog, and a matching "检查更新..." native menu item—strictly on-demand with zero background update polling.
- `desktop-packaging`: Configure Ed25519-signed MyGo update feeds targeting GitHub Releases in `mygo.json`, source release notes from `CHANGELOG.md`, publish signed update archives/deltas/manifests alongside existing macOS DMG and Windows NSIS/portable artifacts in CI, and keep the Windows portable build isolated from overwriting Windows NSIS update manifests.

## Impact

- **UI & Application Lifecycle**: `internal/desktopui/about_modal.go`, `internal/desktopui/menu.go`, `internal/desktopui/app.go`, `internal/desktopui/portable_windows.go`, `internal/desktopui/portable_default.go`, plus a new updater integration file (e.g., `internal/desktopui/updater.go`) and headless unit tests in `internal/desktopui/desktopui_test.go`.
- **Packaging & Release Infrastructure**: `mygo.json`, `CHANGELOG.md`, `.github/workflows/build-release.yml`, and `Makefile` (optional local verification targets/docs).
- **Operational Prerequisite**: One-time Ed25519 key pair generation via `go tool mygo keygen`, storing the public key in `mygo.json` and the private key in GitHub Actions secret `MYGO_UPDATER_PRIVATE_KEY` (plus offline backup).
- **Dependencies**: Zero new Go module dependencies; reuses `github.com/egoist/mygo` (`mygo.Updater`, `mygo.Shell`, `mygo/netproxy`, `mygo/ui`) already pinned in `go.mod`.
