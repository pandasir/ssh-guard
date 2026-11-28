# Spec Delta: desktop-native-ui

## MODIFIED Requirements

### Requirement: Vault- and session-aware menu state synchronization
The desktop application SHALL keep native menu item enabled and checked states synchronized with vault lock status, active tab state, and workspace layout so privileged or inapplicable actions cannot be invoked out of context.

#### Scenario: Vault lock disables privileged workspace and session menu items
- **WHEN** the vault is locked (on cold launch, manual lock, or automatic idle/lifetime lock)
- **THEN** session creation, host creation, tab reconnect/close, terminal clear/zoom, sidebar toggle, audit log, recording list, data directory, and vault lock menu actions are disabled, while standard edit, fullscreen, About, Check for Updates, and Quit actions remain available

#### Scenario: Active tab state controls session and terminal menu availability
- **WHEN** the vault is unlocked and the user opens, switches, disconnects, or closes SSH or local terminal tabs
- **THEN** tab-scoped menu actions (close active tab, close all sessions, clear screen, clear scrollback) are enabled only when tabs exist, and reconnect/restart is enabled only when the active tab is disconnected or in an error state

## ADDED Requirements

### Requirement: Manual signed in-app update checking and installation
The desktop application SHALL allow users to manually check for newer releases from the native About dialog and native menu bar, verify downloaded updates cryptographically before replacing writable desktop installations, warn and cleanly shut down active sessions and the vault before relaunching, and degrade gracefully to browser-based downloads on Windows portable builds or non-writable installations. The application SHALL NOT perform automatic or periodic background update network requests.

#### Scenario: No background update network requests occur automatically
- **WHEN** the desktop application launches, unlocks the vault, or remains running idle
- **THEN** the application initiates zero outbound update-check network requests until the user explicitly triggers "检查更新"

#### Scenario: Triggering Check for Updates from the About dialog or menu bar works in locked and unlocked states
- **WHEN** the user activates "检查更新..." from the macOS application menu or the Windows/Linux Help menu (below "关于 SSH-Guard"), or clicks "检查更新" inside the About dialog in either locked or unlocked vault state
- **THEN** the About dialog opens (if not already open), displays an active checking indicator, and queries the latest release manifest for the current platform over HTTPS while honoring system proxy settings

#### Scenario: Up-to-date and failed checks provide clear inline feedback
- **WHEN** a manual update check finds no version newer than the running version
- **THEN** the About dialog displays an inline up-to-date status message and allows re-checking
- **WHEN** a manual update check fails due to network, proxy, or manifest errors
- **THEN** the About dialog displays a concise error summary and a retry action without disrupting open terminal tabs or vault state

#### Scenario: Writable desktop installation downloads, verifies, and applies update in place
- **WHEN** an update check finds a newer version on a writable macOS `.app` bundle or Windows NSIS installation and the user clicks "立即更新"
- **THEN** the application downloads the signed delta patch (falling back automatically to the signed full archive if delta application fails), renders inline download progress with a cancel action, verifies the update's Ed25519 signature and SHA-256 integrity before staging or replacing application files, and transitions to a restart-ready state displaying the new version

#### Scenario: Relaunching into an installed update warns about active sessions and wipes vault secrets
- **WHEN** an update has been verified and staged/installed while one or more SSH or local terminal tabs are open
- **THEN** the About dialog prominently warns that restarting will disconnect active sessions and lock the vault
- **WHEN** the user confirms "立即重启并更新"
- **THEN** the application executes its clean shutdown sequence (closing all SSH and local terminal sessions, finalizing recordings, wiping the in-memory vault KEK, and closing the SQLite database) before relaunching into the updated version

#### Scenario: Windows single-file portable and non-writable installations degrade to browser download
- **WHEN** an update check finds a newer version while running the Windows single-file portable executable or from a non-writable installation location (such as a read-only disk image or restricted directory)
- **THEN** the About dialog displays the newer version and release notes, explains why in-place replacement is unavailable, omits in-place install actions, and provides an action that opens the GitHub Releases page in the user's default browser
