# Tasks

## 1. Updater Backend Abstraction & Build-Mode Detection

- [x] 1.1 Expose a build-tagged `isPortableBuild() bool` helper in `internal/desktopui/portable_windows.go` (`true`) and `internal/desktopui/portable_default.go` (`false`), and verify with `go test ./internal/desktopui/...` (plus `GOOS=windows GOARCH=amd64 go test -c -tags=mygo_nowebview,portable ./internal/desktopui`) that both standard and portable build tags compile cleanly.
- [x] 1.2 Create `internal/desktopui/updater.go` defining the `updateBackend` / `updateOffer` abstraction, the default production backend wrapping `mygo.Updater` (when `!isPortableBuild() && mygo.Updater.Enabled()`) plus the HTTPS manifest-only fallback (`update-<goos>-<goarch>.json` via `mygo/netproxy.Transport()` and semver comparison when self-update is unavailable), and unit tests covering semver comparison, manifest parsing, HTTPS/proxy fallback, and zero-network-on-init guarantees (`go test -v -race -run TestUpdater ./internal/desktopui`).
- [x] 1.3 Wire optional `UpdateBackend`, `OpenURLFn` (defaulting to `mygo.Shell.OpenExternal`), and `RelaunchFn` (defaulting to `mygo.App.Relaunch`) hooks into `Options` and `App` in `internal/desktopui/app.go`, ensuring no background update goroutine or timer starts during `NewApp` or `Run`, and verify `go test -v -race ./internal/desktopui` passes.

## 2. About Modal Inline Update UI & Relaunch Safety

- [x] 2.1 Implement the `App` update state machine (`idle`, `checking`, `up_to_date`, `available`, `downloading`, `ready`, `error`) with throttled progress updates, cancellable contexts on cancel/modal dismiss, active-session count warning before restart, and clean `a.Shutdown()` before `relaunchFn()` in `internal/desktopui/updater.go`, and add headless unit tests covering all transitions, download cancellation, error retry, portable/read-only browser-download fallback, and shutdown-before-relaunch ordering (`go test -v -race -run TestAppUpdate ./internal/desktopui`).
- [x] 2.2 Update `renderAboutModal` in `internal/desktopui/about_modal.go` to embed the "检查更新" button, inline status/release-notes card (scrollable notes, `ui.Spinner` while checking, `ui.Progress` bar and byte counters while downloading, active-session warning when tabs are open, and context-appropriate action buttons for "立即更新" / "取消下载" / "立即重启并更新" / "前往 GitHub 下载" / "重试"), and verify headless `ui.NewTester` rendering tests in `internal/desktopui/desktopui_test.go` cover locked and unlocked states (`go test -v -race -run TestAboutModal ./internal/desktopui`).

## 3. Native Menu Bar Integration

- [x] 3.1 Add `menuIDAppCheckUpdates` (macOS app menu, below `menuIDAppAbout`) and `menuIDHelpCheckUpdates` (Windows/Linux Help menu, below `menuIDHelpAbout`) labeled `"检查更新..."` in `internal/desktopui/menu.go`, invoking `openAboutAndCheckUpdates()` while remaining enabled in both locked and unlocked vault states without adding conflicting keyboard shortcuts.
- [x] 3.2 Extend `TestAppMenuStructure` and `TestAppMenuStateSync` in `internal/desktopui/desktopui_test.go` to assert placement, labels, no forbidden accelerators, enabled state across locked/unlocked transitions, and click-to-open-and-check behavior, verifying `go test -v -race -run TestAppMenu ./internal/desktopui` passes.

## 4. Packaging, Signing Metadata & Release Pipeline

- [x] 4.1 Create root `CHANGELOG.md` following Keep a Changelog (`## <version>`) with entries matching the current version in `mygo.json`, and configure the `"updates"` block (`"github": "pandasir/ssh-guard"` and the Ed25519 `"publicKey"` generated via `go tool mygo keygen`, keeping the private key out of git) in `mygo.json`; verify `go tool mygo vet .` accepts the configuration.
- [x] 4.2 Update `.github/workflows/build-release.yml` so `build-desktop-mac` and the Windows NSIS build step pass `MYGO_UPDATER_PRIVATE_KEY: ${{ secrets.MYGO_UPDATER_PRIVATE_KEY }}` and upload generated `ssh-guard-*-<target>.tar.gz`, `*.delta` (when present), and `update-<target>.json` alongside existing DMG/EXE artifacts, while the Windows portable build step explicitly clears `MYGO_UPDATER_PRIVATE_KEY` to prevent manifest collisions, and tag builds assert that platform manifests exist before publishing `SHA256SUMS.txt`; verify workflow YAML validity and dry-run build steps cleanly.
- [x] 4.3 Document the Ed25519 update signing key setup (`go tool mygo keygen`, `MYGO_UPDATER_PRIVATE_KEY` GitHub secret) and release version-bump checklist (`mygo.json`, `about_modal.go`, `CHANGELOG.md`) in `README.md`, verifying instructions match the exact file paths and commands.

## 5. End-to-End Integration Verification

- [x] 5.1 Run full test suite (`go test -v -race ./...`), MyGo UI vet (`go tool mygo vet .`), and cross-compile checks (`GOOS=windows GOARCH=amd64 go test -c -tags=mygo_nowebview ./internal/desktopui` and `-tags=mygo_nowebview,portable`) to confirm zero regressions, zero WebView dependencies, and intact memory-budget tests.

## Workflow follow-up

- Maintainer configures the generated `mygo-update.key` as GitHub repository secret `MYGO_UPDATER_PRIVATE_KEY` (and backs it up offline) prior to pushing the next `v*` release tag.
- After archiving the change and tagging the first signed release (e.g., `v0.7.0`), verify GitHub Release assets include `update-darwin-arm64.json`, `update-windows-amd64.json`, `.tar.gz` archives, and `SHA256SUMS.txt`, and verify in-app upgrade and delta generation on the subsequent release.
