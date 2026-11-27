# desktop-native-ui Delta Specification

## ADDED Requirements

### Requirement: Tight steady-state desktop memory budget with transient unlock headroom
The desktop application SHALL maintain a tight steady-state runtime memory limit and garbage collection target while temporarily permitting transient headroom during Argon2id vault key derivation and returning freed memory to the operating system afterward.

#### Scenario: Vault unlock reclaims transient key-derivation memory and restores tight steady-state limits
- **WHEN** the user initializes or unlocks the vault via Argon2id
- **THEN** the application temporarily grants sufficient memory headroom for the 64 MiB key-derivation buffer without GC thrashing, immediately restores the tighter steady-state memory limit upon completion, and releases unused pages back to the OS

#### Scenario: Vault lock and modal dismissal reclaim transient memory
- **WHEN** the user locks the vault, closes the replay player, or dismisses the audit log or recording table dialogs
- **THEN** transient UI collections are cleared, local SQLite cache memory is shrunk on vault lock, and freed heap memory is returned to the OS

## MODIFIED Requirements

### Requirement: Zero-WebView native desktop application shell
The desktop edition SHALL render all application views—including vault lock, host sidebar, multi-tab terminal workspace, host configuration dialogs, TOFU/MITM alerts, audit logs, and recording replay—using native UI controls with a platform-adaptive rendering backend, without launching any embedded WebView process or listening on local HTTP/WebSocket ports.

#### Scenario: Cold launch operates without WebView or local listening ports
- **WHEN** the user launches the SSH-Guard desktop application
- **THEN** the application opens a native window directly in-process using the platform-appropriate rendering backend (low-memory CPU rasterization with native OS blitting by default on Windows, and native GPU rendering on macOS), starts zero WebView child processes, and binds zero local TCP ports

#### Scenario: Windows rendering backend defaults to low-memory CPU mode with GPU opt-in
- **WHEN** the desktop application starts on Windows with `MYGO_GPU` unset
- **THEN** the application defaults to `MYGO_GPU=0` so Direct3D/DXGI GPU drivers are not loaded, honors an explicit `MYGO_GPU=1` override when users opt into GPU rendering, and reflects the active rendering mode in the workspace status bar

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
