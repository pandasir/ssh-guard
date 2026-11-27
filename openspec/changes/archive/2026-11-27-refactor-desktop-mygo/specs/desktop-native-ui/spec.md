# Spec Delta

## Purpose

Defines the zero-WebView native GPU-rendered desktop interface and local security lifecycle for SSH-Guard, delivering low-memory multi-tab SSH operations, vault protection, host management, audit inspection, and native session replay without requiring a browser runtime.

## ADDED Requirements

### Requirement: Zero-WebView native desktop application shell
The desktop edition SHALL render all application views—including vault lock, host sidebar, multi-tab terminal workspace, host configuration dialogs, TOFU/MITM alerts, audit logs, and recording replay—using native UI controls without launching any embedded WebView process or listening on local HTTP/WebSocket ports.

#### Scenario: Cold launch operates without WebView or local listening ports
- **WHEN** the user launches the SSH-Guard desktop application
- **THEN** the application opens a native GPU-rendered window directly in-process, starts zero WebView child processes, and binds zero local TCP ports

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
The desktop application SHALL support opening multiple concurrent SSH sessions (including multiple tabs to the same host) in native GPU-rendered terminal views with bounded per-session scrollback memory and explicit omission of the Web edition's left timestamp/line-number gutter.

#### Scenario: Opening a host creates a tab with immediate connection feedback and bounded memory
- **WHEN** the user connects to a host from the sidebar
- **THEN** a new terminal tab is created and activated immediately, displays connection status inside the terminal, configures a bounded per-tab scrollback limit (default 256 KB to 512 KB rather than an unbounded or multi-megabyte default), and attaches directly to the live SSH session stream upon handshake completion

#### Scenario: Closing a tab releases native terminal emulator resources
- **WHEN** the user closes a terminal tab or disconnects a session
- **THEN** the underlying SSH session is closed and the native terminal emulator instance is explicitly disposed so native off-heap scrollback memory is released

#### Scenario: Native desktop terminal omits the left timestamp and line-number gutter
- **WHEN** a user views an active SSH session in the native desktop application
- **THEN** the terminal canvas fills the tab workspace without rendering the Web edition's left timestamp/line-number gutter

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
- **THEN** the desktop app opens a read-only native terminal sized to the `.cast` header's `width` and `height`, streams output events incrementally according to elapsed timestamps, and supports pause/resume, playback speed selection (`1x`, `2x`, `4x`), progress display, and clean resource disposal on close

#### Scenario: Opening data directory launches native OS file manager
- **WHEN** the user clicks the data directory action in the sidebar
- **THEN** the desktop app ensures the configured data directory exists and reveals it in macOS Finder, Windows Explorer, or the Linux desktop file manager
