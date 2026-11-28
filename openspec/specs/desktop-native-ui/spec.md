# desktop-native-ui Specification

## Purpose

Defines the zero-WebView native GPU-rendered desktop interface and local security lifecycle for SSH-Guard, delivering low-memory multi-tab SSH operations, vault protection, host management, audit inspection, and native session replay without requiring a browser runtime.

## Requirements

### Requirement: Zero-WebView native desktop application shell
The desktop edition SHALL render all application views—including vault lock, host sidebar, multi-tab terminal workspace, host configuration dialogs, TOFU/MITM alerts, audit logs, and recording replay—using native UI controls with a platform-adaptive rendering backend, without launching any embedded WebView process or listening on local HTTP/WebSocket ports.

#### Scenario: Cold launch operates without WebView or local listening ports
- **WHEN** the user launches the SSH-Guard desktop application
- **THEN** the application opens a native window directly in-process using the platform-appropriate rendering backend (low-memory CPU rasterization with native OS blitting by default on Windows, and native GPU rendering on macOS), starts zero WebView child processes, and binds zero local TCP ports

#### Scenario: Windows rendering backend defaults to low-memory CPU mode with GPU opt-in
- **WHEN** the desktop application starts on Windows with `MYGO_GPU` unset
- **THEN** the application defaults to `MYGO_GPU=0` so Direct3D/DXGI GPU drivers are not loaded, honors an explicit `MYGO_GPU=1` override when users opt into GPU rendering, and reflects the active rendering mode in the workspace status bar

### Requirement: Vault initialization, unlock, manual lock, and automatic idle/lifetime lock
The desktop application SHALL protect all stored credentials behind the master-password vault, clear plaintext input fields immediately after submission, and automatically lock the vault and terminate active SSH sessions upon inactivity timeout, maximum session lifetime, manual lock, or application exit.

#### Scenario: First-run vault initialization and subsequent unlock
- **WHEN** the database has no vault metadata on launch
- **THEN** the desktop app displays the vault setup screen requiring a master password of at least 8 characters with confirmation; when metadata already exists, it displays the unlock screen and verifies the master password via Argon2id before showing the main workspace

#### Scenario: Plaintext password fields are cleared after submission
- **WHEN** the user submits the vault setup/unlock form or saves host credentials
- **THEN** the desktop UI clears the bound password, private key, and passphrase input state immediately so plaintext secrets do not linger in UI state

#### Scenario: Automatic idle or max-lifetime lock tears down sessions and wipes KEK
- **WHEN** the desktop application experiences 30 minutes of continuous inactivity or reaches 8 hours since the last unlock
- **THEN** the application closes all active SSH sessions and terminal instances, wipes the in-memory KEK via vault lock, writes a lock audit record, and returns to the vault lock screen

#### Scenario: Window close or app exit cleans up sessions and wipes KEK
- **WHEN** the user closes the desktop application window or quits the application
- **THEN** all active SSH sessions and native terminal instances are closed cleanly, recordings are finalized, the vault KEK is zeroized, and the SQLite store is closed

### Requirement: Host inventory management with native private key import
The desktop application SHALL allow unlocked users to list, search, create, edit, and delete SSH host entries backed by the encrypted local SQLite store, including importing local SSH private key files through the native OS file picker.

#### Scenario: Creating or editing a host encrypts credentials and preserves existing secrets on blank edit
- **WHEN** a user submits a valid host form with password or private-key authentication
- **THEN** new credentials are envelope-encrypted and persisted; when editing an existing host with blank secret or passphrase fields, existing encrypted credentials are preserved

#### Scenario: Native private key picker validates size limit
- **WHEN** the user clicks to import a private key file from disk
- **THEN** a native OS file picker opens defaulting to `~/.ssh` (falling back to home directory) without blocking UI rendering, rejects files larger than 64 KB with a clear error message, and populates the multi-line private key input on valid selection

#### Scenario: Deleting a host closes its active sessions
- **WHEN** the user confirms deletion of a host that has active terminal tabs open
- **THEN** the desktop app closes all tabs belonging to that host before removing the host and its encrypted credentials from storage

### Requirement: Multi-tab native SSH terminal workspace with bounded scrollback
The desktop application SHALL support opening multiple concurrent SSH sessions (including multiple tabs to the same host) in native terminal views with bounded per-session scrollback memory, non-blinking default cursors for idle framebuffer reclamation, on-demand native terminal engine loading, and explicit omission of the Web edition's left timestamp/line-number gutter.

#### Scenario: Opening a host creates a tab with immediate connection feedback and bounded memory
- **WHEN** the user connects to a host from the sidebar
- **THEN** a new terminal tab is created and activated immediately, displays connection status inside the terminal, configures a bounded per-tab scrollback limit (default 256 KB rather than an unbounded or multi-megabyte default) and non-blinking default cursor, buffers output through a bounded self-shrinking stream, and attaches to the live SSH session upon handshake completion

#### Scenario: Closing a tab releases native terminal emulator resources
- **WHEN** the user closes a terminal tab or disconnects a session
- **THEN** the underlying SSH session is closed and the native terminal emulator instance is explicitly disposed so native off-heap scrollback memory is released

#### Scenario: Native desktop terminal omits the left timestamp and line-number gutter
- **WHEN** a user views an active SSH session in the native desktop application
- **THEN** the terminal canvas fills the tab workspace without rendering the Web edition's left timestamp/line-number gutter

#### Scenario: Idle terminal session stops periodic redraws to allow framebuffer reclamation
- **WHEN** an active terminal tab has keyboard focus and receives no input or remote output for more than 2 seconds
- **THEN** the terminal does not trigger periodic cursor-blink redraws, allowing the native rendering host to become idle and reclaim transient framebuffers and text shaping caches

#### Scenario: Native terminal library loads lazily on first terminal or replay use
- **WHEN** the desktop application launches into the vault lock screen or unlocked host inventory before opening any terminal tab or recording replay
- **THEN** the native terminal dynamic library is not loaded until the user first connects to a host or starts a session replay

### Requirement: Local login-shell tabs in the native terminal workspace
While the vault is unlocked, the desktop workspace SHALL allow the user to open
multiple concurrent local login-shell tabs rendered by the native terminal engine
without selecting or creating an SSH host. Local tabs SHALL remain outside the
SSH connection, TOFU, command-guard, audit, and recording flows.

#### Scenario: Opening local terminals starts independent numbered shell tabs
- **WHEN** the user activates the local terminal creation action from the empty workspace or the session tab bar
- **THEN** a new active tab opens with a stable sequential label (such as `本地终端 #1`, `本地终端 #2`), reflects shell title updates in the workspace status bar, runs the user's local login shell in an independent pseudo-terminal, and performs no SSH dial, TOFU check, command-guard setup, audit write, or `.cast` recording creation

#### Scenario: Local shell exit is reported with in-place restart without disturbing SSH tabs
- **WHEN** a local login shell exits or fails to start
- **THEN** the affected local tab reports the exit or startup status in the workspace status bar, offers an in-place restart action alongside closing the tab, and leaves all other local and SSH session tabs connected and unchanged

#### Scenario: Closing a local terminal tab releases its shell resources
- **WHEN** the user closes a local terminal tab
- **THEN** the local shell process and its pseudo-terminal are terminated and
  released without producing an SSH disconnect audit entry

#### Scenario: Vault lock and application exit close local terminal tabs
- **WHEN** the vault locks automatically or manually, or the application exits
- **THEN** every local terminal tab and its shell process are closed together
  with the SSH sessions before the vault key is wiped

### Requirement: Queued TOFU and MITM verification dialogs with in-tab reconnect
The desktop application SHALL present queued modal verification prompts for untrusted or mismatched host keys across concurrent connection attempts and allow trusting and reconnecting inside the originating tab.

#### Scenario: Concurrent TOFU/MITM events are queued without overwriting active prompts
- **WHEN** two tabs concurrently encounter untrusted or mismatched host keys
- **THEN** the first verification modal is shown immediately with key type and fingerprint details (plus expected fingerprint and high-severity warning for MITM mismatch), and the second prompt is queued and displayed once the first modal is resolved or dismissed

#### Scenario: Confirming host key trust persists fingerprint and reconnects in place
- **WHEN** the user confirms trusting the presented host key in the modal
- **THEN** the desktop app persists the trusted host key in storage, writes a `TOFU_TRUST` audit log entry, refreshes host trust status in the sidebar, and automatically reconnects inside the same terminal tab

### Requirement: High-risk command interception feedback in desktop workspace
The desktop application SHALL surface high-risk command blocks prominently without losing terminal keyboard focus control or blocking terminal I/O threads.

#### Scenario: Blocked command displays both inline terminal warning and non-blocking toast
- **WHEN** the shared SSH session core blocks a high-risk command in an active desktop tab
- **THEN** the red warning banner renders inline in the terminal output stream and a thread-safe native toast notification alerts the user with the matched rule name

### Requirement: Native audit log viewer, recording browser, data directory reveal, and streaming replay
The desktop application SHALL provide native tabular views for audit logs and session recordings, a button to reveal the local data directory in the OS file manager, and a native read-only streaming `.cast` player.

#### Scenario: Viewing audit logs and recordings renders native virtualized tables
- **WHEN** the user opens the audit logs or recordings view
- **THEN** the desktop app queries the local SQLite store and renders severity-coded audit logs (newest first) or recorded sessions with size and timestamp in native tables

#### Scenario: Native streaming playback honors recording dimensions, pause, and speed controls
- **WHEN** the user starts playback of a recorded `.cast` session
- **THEN** the desktop app opens a read-only non-blinking native terminal sized to the `.cast` header's `width` and `height`, streams output events incrementally according to elapsed timestamps with throttled progress-label updates, and supports pause/resume, playback speed selection (`1x`, `2x`, `4x`), progress display, and clean resource disposal with OS memory reclamation on close

#### Scenario: Opening data directory launches native OS file manager
- **WHEN** the user clicks the data directory action in the sidebar
- **THEN** the desktop app ensures the configured data directory exists and reveals it in macOS Finder, Windows Explorer, or the Linux desktop file manager

### Requirement: Tight steady-state desktop memory budget with transient unlock headroom
The desktop application SHALL maintain a tight steady-state runtime memory limit and garbage collection target while temporarily permitting transient headroom during Argon2id vault key derivation and returning freed memory to the operating system afterward.

#### Scenario: Vault unlock reclaims transient key-derivation memory and restores tight steady-state limits
- **WHEN** the user initializes or unlocks the vault via Argon2id
- **THEN** the application temporarily grants sufficient memory headroom for the 64 MiB key-derivation buffer without GC thrashing, immediately restores the tighter steady-state memory limit upon completion, and releases unused pages back to the OS

#### Scenario: Vault lock and modal dismissal reclaim transient memory
- **WHEN** the user locks the vault, closes the replay player, or dismisses the audit log or recording table dialogs
- **THEN** transient UI collections are cleared, local SQLite cache memory is shrunk on vault lock, and freed heap memory is returned to the OS

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
- **THEN** session creation, host creation, tab reconnect/close, terminal clear/zoom, sidebar toggle, audit log, recording list, data directory, and vault lock menu actions are disabled, while standard edit, fullscreen, About, Check for Updates, and Quit actions remain available

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
