# Design

## Context

See [proposal.md](./proposal.md) for motivation and scope.

SSH-Guard Desktop is a single-module Go 1.27+ native desktop application built on `github.com/egoist/mygo` (`v0.10.0`), targeting zero-WebView rendering, a 28 MiB steady-state heap budget, and three release artifacts via `.github/workflows/build-release.yml`:
- `SSH-Guard-macOS-arm64.dmg` (containing `SSH-Guard.app` + `libghostty-vt.dylib`)
- `SSH-Guard-Windows-amd64-installer.exe` (NSIS installer deploying `SSH-Guard.exe` + `ghostty-vt.dll`)
- `SSH-Guard-Windows-amd64-portable.exe` (standalone single-file build using `-tags=mygo_nowebview,portable` that embeds `ghostty-vt.dll`)

`github.com/egoist/mygo` v0.10.0 already ships a zero-WebView, Ed25519-signed self-update engine (`mygo.Updater`) with automatic bsdiff delta support, HTTPS enforcement, system proxy awareness (`netproxy`), atomic macOS post-exit bundle swapping (preserving Dock icon and avoiding browser quarantine attributes), and Windows in-place file swapping.

## Goals / Non-Goals

**Goals:**
- Deliver a manually triggered, Ed25519-verified in-app update experience integrated seamlessly into the dark-themed **关于 SSH-Guard** modal (`internal/desktopui/about_modal.go`) and native menu bar (`internal/desktopui/menu.go`).
- Preserve SSH-Guard's zero-trust offline posture (zero automatic background checks) and tight memory footprint (no extra window, no WebView, no persistent `updater.json` outside app data).
- Provide unified version checking across all distribution modes while restricting in-place replacement to writable macOS `.app` and Windows NSIS installations, with clean browser-download fallback for Windows Portable and read-only locations.
- Guarantee clean session teardown and KEK zeroization (`App.Shutdown()`) before `mygo.App.Relaunch()`.
- Keep every update UI state transition and error path deterministic and testable under headless `ui.NewTester` unit tests.

**Non-Goals:**
- Automatic/scheduled background update checks or silent auto-downloads.
- In-place self-replacement of the Windows single-file portable executable (portable checks versions and opens GitHub Releases in the browser).
- Custom CDN/S3/R2 mirrors or prerelease/beta channel toggles in this change (can be layered onto `mygo.Updater.SetSource` / `SetPrereleases` later without breaking the manifest format).
- Linux distribution packaging changes (the current release workflow builds macOS arm64 and Windows amd64).

## Decisions

### 1. Embed custom update UI inside `about_modal.go` using `mygo.Updater` directly (vs `plugins/updater/native`)
- **Decision**: Call `mygo.Updater` directly from a dedicated `internal/desktopui/updater.go` controller and render its state inline inside `renderAboutModal` using `mygo/ui` primitives (`ui.Spinner`, `ui.Progress`, `ui.Scroll`, `ui.Button`, `ui.PrimaryButton`).
- **Rationale**:
  - Visual consistency with SSH-Guard's custom GitHub-Dark palette (`#0d1117` / `#161b22` / `#30363d`) without spawning a second native window.
  - Strictly manual-only by construction (no background timer, no `mygo.Power.OnResume` hook, no extra `updater.json` preferences file).
  - Full control over pre-relaunch security behavior: we can count active SSH/local tabs, show a clear warning, and run `a.Shutdown()` before `mygo.App.Relaunch()`.
- **Alternative Considered**: `github.com/egoist/mygo/plugins/updater/native` with `DisableAutomaticChecks: true`. Rejected because it opens a separate window with MyGo's stock theme, adds extra memory/Markdown-renderer state, and calls `mygo.App.Relaunch()` directly without awareness of active SSH sessions.

### 2. Decouple version checking from in-place install capability
- **Decision**: Compute `canSelfUpdate = !isPortableBuild() && updater.Enabled()`.
  - When `canSelfUpdate` is true, checking uses `mygo.Updater.Check(ctx)` and returns an installable handle (`Update.Install(ctx, progress)`).
  - When `canSelfUpdate` is false (Windows Portable build, read-only DMG mount, non-admin `/Applications`, or local `mygo dev` build), checking still fetches the static platform manifest `https://github.com/pandasir/ssh-guard/releases/latest/download/update-<goos>-<goarch>.json` over HTTPS via `netproxy.Transport()` (respecting OS proxy / `HTTPS_PROXY`), parses `version`, `notes`, and `date`, and compares semver against `appDisplayVersion()`.
- **Rationale**:
  - `mygo.Updater.Enabled()` probes write permission via `os.MkdirTemp` beside the executable and returns `ErrUpdatesDisabled` on read-only paths or dev builds. Without a manifest-only fallback, portable users in read-only directories (e.g., USB drives) or macOS users running from a DMG could not even discover a new version.
  - Fetching `/releases/latest/download/update-<target>.json` uses GitHub's static release redirect rather than `api.github.com`, avoiding the unauthenticated 60 req/hr IP rate limit.
  - A build-tag helper (`isPortableBuild() bool` in `portable_windows.go` = `true`, `portable_default.go` = `false`) cleanly distinguishes portable from NSIS installs without fragile filename heuristics.
- **Alternative Considered**: Using GitHub REST API `/repos/pandasir/ssh-guard/releases/latest` for portable checks. Rejected due to strict unauthenticated rate limits (especially behind corporate NAT/proxies) and format duplication with `update-*.json`.

### 3. Update state machine & UI concurrency model
- **Decision**: Introduce an `updateState` struct embedded on `App` (mutated strictly on the UI thread via `a.runUI(...)`):
  ```text
  idle --> checking --> up_to_date
                    --> available (canInstall=true | false, fallbackReason)
                    --> error

  available (canInstall=true) -- click "立即更新" --> downloading (progress, cancel)
                                                         |
                              +--------------------------+--------------------------+
                              v                          v                          v
                          cancelled                  error                       ready
                      (back to available)       (retry available)    (warn if active tabs > 0)
                                                                                    |
                                                                     click "立即重启并更新"
                                                                                    v
                                                                     a.Shutdown() -> relaunchFn()
  ```
- **Details**:
  - Background check and download goroutines hold a cancellable `context.Context` (`updateCancel`). Closing the About modal during `checking` cancels the check; during `downloading`, the backdrop/close button either cancels cleanly or keeps progress cancelable via an explicit「取消下载」button (to prevent accidental half-cancelled updates, closing the dialog while `downloading` cancels the active context and resets to `available`, or we allow dismissing only via「取消下载」—specifically: dismissing the modal while `checking` cancels the check; while `downloading`, dismissing cancels the download cleanly so no orphaned background download continues silently after the user leaves the modal, staying faithful to zero-background-networking).
  - Download progress updates are throttled (e.g., at most every 100ms or >= 1% delta, plus final completion) before calling `a.runUI(...)` to keep UI frame cost negligible.
  - Because `up.Install` can retry from full archive if a delta fails, progress resets cleanly when `downloaded` drops or `total` changes.
  - Browser fallback opens `https://github.com/pandasir/ssh-guard/releases/tag/v<version>` (or `/releases/latest` if tag is empty) via `mygo.Shell.OpenExternal` (injected via `Options.OpenURLFn` for tests).

### 4. Menu bar integration (`internal/desktopui/menu.go`)
- **Decision**: Add `menuIDAppCheckUpdates` (macOS app menu, immediately after `menuIDAppAbout`) and `menuIDHelpCheckUpdates` (Windows/Linux Help menu, immediately after `menuIDHelpAbout`), labeled `"检查更新..."`, with no keyboard accelerator (matching macOS/Windows conventions and avoiding any terminal shortcut conflicts).
- Clicking either item calls `a.runUI(func() { a.openAboutAndCheckUpdates() })`, which sets `a.aboutOpen = true` and starts a check if one is not already in progress (`checking` or `downloading`).
- Both items remain enabled in locked and unlocked states (excluded from vault-lock disabling in `syncAppMenu`).

### 5. Dependency injection for headless tests
- **Decision**: Extend `desktopui.Options` with optional test hooks (nil = production defaults):
  - `UpdateBackend updateBackend` (interface wrapping `CanSelfUpdate() (bool, string)`, `Check(ctx) (*updateOffer, error)`, where `updateOffer` holds `Version`, `Notes`, `Date`, `CanInstall`, `FallbackReason`, and `Install(ctx, progress) error`)
  - `OpenURLFn func(rawURL string) error` (defaults to `mygo.Shell.OpenExternal`)
  - `RelaunchFn func()` (defaults to `mygo.App.Relaunch`)
- **Rationale**: Follows the exact pattern of `DialConn`, `PickKeyFn`, and `OpenDirFn` in `internal/desktopui/app.go`, allowing full `go test -v -race ./...` coverage of every UI state, cancellation, error retry, active-session warning, shutdown-before-relaunch ordering, and portable fallback without touching the network or disk.

### 6. CI signing & Windows Portable manifest isolation
- **Decision**:
  - Add `"updates": { "publicKey": "<ed25519-pub>", "github": "pandasir/ssh-guard" }` to `mygo.json`.
  - Add root `CHANGELOG.md` with `## <version>` sections (starting with current version and upcoming release template).
  - In `.github/workflows/build-release.yml`:
    - `build-desktop-mac`: Pass `MYGO_UPDATER_PRIVATE_KEY: ${{ secrets.MYGO_UPDATER_PRIVATE_KEY }}` into the build step. Copy `dist/mygo/darwin-arm64/ssh-guard-*-darwin-arm64.tar.gz`, `dist/mygo/darwin-arm64/*.delta` (if present), and `dist/mygo/darwin-arm64/update-darwin-arm64.json` into `release-mac/`.
    - `build-desktop-windows`:
      - Pass `MYGO_UPDATER_PRIVATE_KEY` to the first `go tool mygo build -platform windows/amd64` (NSIS build) and copy `ssh-guard-*-windows-amd64.tar.gz`, `*.delta` (if present), and `update-windows-amd64.json` into `release-win/`.
      - Explicitly clear `$env:MYGO_UPDATER_PRIVATE_KEY = ""` before the second `go tool mygo build -platform windows/amd64 -skip-installer -o dist/mygo-portable` (`-tags=mygo_nowebview,portable`) so no second manifest or archive is generated.
    - Keep `release` job uploading `release-assets/*` and generating `SHA256SUMS.txt` over all artifacts.
    - Note on optional `workflow_dispatch` without secret: if `MYGO_UPDATER_PRIVATE_KEY` is empty (e.g., fork PR / manual test dispatch), `mygo build` skips signing with a warning rather than failing the build; tag releases require the secret to be configured so update manifests are produced (add a check when `startsWith(github.ref, 'refs/tags/v')` to fail fast if `update-*.json` is missing on a tag build).

## Risks / Trade-offs

- **[Ed25519 private key loss strands existing installations]** → Store `mygo-update.key` in both GitHub Actions repository secrets (`MYGO_UPDATER_PRIVATE_KEY`) and an offline password manager backup; never commit it to git.
- **[First signed release has no prior signed manifest for deltas]** → Expected behavior: `mygo build` logs that no prior signed version exists and publishes the full signed `.tar.gz` + `update-*.json`; bsdiff `.delta` patches begin automatically on the subsequent release.
- **[Mainland China users without proxy may experience slow/failed GitHub Releases downloads]** → `mygo.Updater` and the fallback checker both use `netproxy.Transport()`, honoring system proxy and `HTTPS_PROXY`; failures surface a clear inline retry action plus a browser-download option if needed. Future changes can add a custom `UpdateSource` mirror without altering the signature format.
- **[`CHANGELOG.md` missing section for a bumped version causes `mygo build` to fail when signing]** → Document the release checklist (`mygo.json` version, `appFallbackVersion` in `about_modal.go`, and matching `## <version>` heading in `CHANGELOG.md`) and validate in CI.
- **[macOS `.app` inside read-only DMG or non-admin `/Applications` cannot write beside bundle]** → `mygo.Updater.Enabled()` detects non-writability up front and the UI cleanly falls back to showing the release notes with a「前往 GitHub 下载」button and an explanation.

## Migration Plan

1. **One-time key generation (maintainer action before first release)**:
   - Run `go tool mygo keygen` locally.
   - Paste `mygo-update.pub` contents into `mygo.json` (`updates.publicKey`).
   - Add `mygo-update.key` contents as GitHub repository secret `MYGO_UPDATER_PRIVATE_KEY` and back up offline.
2. **Code & CI rollout**:
   - Implement updater controller, About modal UI, menu bar entries, `CHANGELOG.md`, `mygo.json`, CI updates, and unit tests.
   - Bump version (e.g., `0.7.0`) in `mygo.json`, `appFallbackVersion`, and `CHANGELOG.md`, then tag `v0.7.0`.
3. **Verification**:
   - Confirm `v0.7.0` GitHub Release contains DMG, NSIS EXE, Portable EXE, `.tar.gz` archives, `update-darwin-arm64.json`, `update-windows-amd64.json`, and `SHA256SUMS.txt`.
   - Test end-to-end upgrade on `v0.7.0` -> `v0.7.1` (or `v0.8.0`) on macOS arm64 (verify `.delta` generation and Dock-preserving relaunch), Windows NSIS (verify in-place swap), and Windows Portable (verify version detection + browser redirect without writing extra files).
4. **Rollback**:
   - Reverting the commit disables update feed embedding and restores the previous About modal and CI workflow; existing releases remain downloadable manually from GitHub Releases.
