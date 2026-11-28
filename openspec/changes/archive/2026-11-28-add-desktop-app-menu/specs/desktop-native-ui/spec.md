# Spec Delta

## ADDED Requirements

### Requirement: Cross-platform native application menu bar with consistent dark chrome
The desktop application SHALL register a native OS application menu bar exposing common session, editing, terminal, view, security, and help actions with platform-appropriate structure and conflict-free keyboard shortcuts, and SHALL enforce dark native window and menu appearance across desktop platforms.

#### Scenario: Application launches with platform-appropriate native menu bar and dark chrome
- **WHEN** the user launches SSH-Guard Desktop on macOS, Windows, or Linux
- **THEN** the application installs a native OS menu bar tailored to the platform (including the macOS application and Window menus on macOS, and placing Quit under Session and About under Help on Windows/Linux), and sets application-level dark appearance so Windows title bars, menu bars, and popup menus match the dark workspace regardless of the OS light/dark setting

#### Scenario: Custom menu shortcuts avoid colliding with interactive terminal control keys
- **WHEN** the user works inside an active SSH or local terminal tab on Windows or Linux
- **THEN** standard terminal control sequences (such as `Ctrl+C`, `Ctrl+W`, `Ctrl+R`, `Ctrl+K`, and `Ctrl+B`) continue to reach the terminal session rather than being intercepted by custom application menu shortcuts, while custom menu actions use `Cmd`-based shortcuts on macOS and `Ctrl+Shift` / `Ctrl+Alt`-based shortcuts on Windows and Linux

### Requirement: Vault- and session-aware menu state synchronization
The desktop application SHALL keep native menu item enabled and checked states synchronized with vault lock status, active tab state, and workspace layout so privileged or inapplicable actions cannot be invoked out of context.

#### Scenario: Vault lock disables privileged workspace and session menu items
- **WHEN** the vault is locked (on cold launch, manual lock, or automatic idle/lifetime lock)
- **THEN** session creation, host creation, tab reconnect/close, terminal clear/zoom, sidebar toggle, audit log, recording list, data directory, and vault lock menu actions are disabled, while standard edit, fullscreen, About, and Quit actions remain available

#### Scenario: Active tab state controls session and terminal menu availability
- **WHEN** the vault is unlocked and the user opens, switches, disconnects, or closes SSH or local terminal tabs
- **THEN** tab-scoped menu actions (close active tab, close all sessions, clear screen, clear scrollback) are enabled only when tabs exist, and reconnect/restart is enabled only when the active tab is disconnected or in an error state

### Requirement: Menu-driven terminal clear, font zoom, sidebar toggle, and About dialog
The desktop application SHALL support clearing the active terminal screen and scrollback, adjusting terminal font size across open and newly created tabs, toggling host sidebar visibility, and viewing application metadata in a native About dialog from the menu bar.

#### Scenario: Clearing screen or scrollback acts on the active terminal tab
- **WHEN** an unlocked user invokes "清屏" or "清除回滚历史" from the Terminal menu with an active terminal tab
- **THEN** the active terminal clears its screen or scrollback buffer without disconnecting the session or altering other open tabs

#### Scenario: Terminal font size zoom updates open tabs and persists for new tabs
- **WHEN** an unlocked user invokes zoom in, zoom out, or reset font size from the Terminal menu
- **THEN** the application updates the terminal font size within bounded limits (`10pt` to `28pt`, default `14pt`), applies the updated font size immediately to all open terminal tabs, and uses the updated size for subsequently opened SSH and local terminal tabs

#### Scenario: Toggling host sidebar reclaims workspace width and syncs menu checkmark
- **WHEN** an unlocked user toggles "显示主机侧边栏" from the View menu
- **THEN** the left host sidebar hides or reappears immediately so the terminal workspace expands to fill the available width, and the menu item's checked state reflects whether the sidebar is currently visible

#### Scenario: Opening About dialog displays application metadata without unlocking vault
- **WHEN** the user activates "关于 SSH-Guard" from the application menu (macOS) or Help menu (Windows/Linux) in either locked or unlocked state
- **THEN** a native About dialog opens displaying the application version, active rendering backend, OS/architecture platform, configured data directory path, and zero-trust architecture summary, and can be dismissed cleanly
