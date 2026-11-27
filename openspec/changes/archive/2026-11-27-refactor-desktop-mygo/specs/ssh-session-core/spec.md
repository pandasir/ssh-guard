# Spec Delta

## Purpose

Provides a transport-agnostic SSH session engine and streaming session recording capability shared by both the offline Web edition and the native desktop edition, enforcing consistent zero-trust credential handling, host key verification, high-risk command interception, and audit logging.

## ADDED Requirements

### Requirement: Just-in-time credential decryption and immediate memory zeroization
The SSH session core SHALL decrypt host credentials only for the duration of the SSH handshake and SHALL overwrite decrypted credential buffers in memory immediately after the handshake attempt completes, regardless of whether the handshake succeeds or fails.

#### Scenario: Successful SSH handshake clears decrypted credentials
- **WHEN** a client initiates a session for a host with encrypted password or private key credentials and the vault is unlocked
- **THEN** the core decrypts the host secret and optional passphrase in memory, completes the SSH dial, and immediately zeroizes all temporary plaintext credential buffers before entering interactive I/O

#### Scenario: Failed handshake or locked vault never leaves plaintext credentials behind
- **WHEN** a session handshake fails due to authentication error, network timeout, or host key rejection
- **THEN** the core immediately zeroizes all temporary decrypted credential buffers and returns a structured error to the caller

#### Scenario: Locked vault rejects session creation
- **WHEN** a client attempts to start a session while the vault is locked
- **THEN** the core rejects the request without dialing the remote server

### Requirement: Strict TOFU and anti-MITM host key verification
The SSH session core SHALL reject unknown or changed server host keys during handshake and surface structured verification details so clients can prompt users before trusting a fingerprint.

#### Scenario: First connection requires TOFU approval
- **WHEN** dialing a host that has no stored trusted host key fingerprint
- **THEN** the handshake aborts before opening a shell and returns an untrusted-host-key error containing the server key type, SHA-256 fingerprint, and base64-encoded public key

#### Scenario: Changed server host key triggers MITM block and CRITICAL audit log
- **WHEN** dialing a host whose presented public key does not match the stored trusted fingerprint or public key
- **THEN** the handshake aborts, records a `CRITICAL` severity `MITM_ALERT` audit log entry with expected and actual fingerprints, and returns a host-key-mismatch error containing both fingerprints

#### Scenario: Matching stored host key proceeds to PTY shell
- **WHEN** dialing a host whose presented public key matches the stored trusted fingerprint and public key
- **THEN** the handshake succeeds, requests an `xterm-256color` PTY with the caller's initial terminal dimensions, starts the remote shell, and records a `CONNECT` audit log entry

### Requirement: Unified bidirectional terminal stream with merged output and PTY resize
An established SSH session SHALL expose a unified bidirectional stream that delivers both remote `stdout` and `stderr` to consumers, forwards interactive input to remote `stdin`, and synchronizes terminal window dimension changes.

#### Scenario: Remote stdout and stderr are both delivered and recorded
- **WHEN** the remote SSH session writes output to either `stdout` or `stderr`
- **THEN** all bytes are delivered through the session output stream and recorded as `"o"` events in the active asciinema v2 `.cast` recording

#### Scenario: Terminal resize synchronizes remote PTY dimensions
- **WHEN** the consumer notifies the session of updated valid `cols` and `rows` values greater than zero
- **THEN** the session forwards a PTY window-change request to the remote SSH server

### Requirement: High-risk command interception with inline warning and audit trail
When command guard is enabled for a host, the SSH session core SHALL inspect interactive input lines on newline submission and block commands matching built-in high-risk patterns before execution.

#### Scenario: High-risk command is blocked and reported inline
- **WHEN** a user submits a command matching a high-risk rule (such as `rm -rf /`, `mkfs`, `dd of=/dev/...`, fork bomb, `chmod -R 777 /`, `reboot`/`shutdown`, or `iptables -F`) on a guard-enabled host
- **THEN** the core sends `0x03` (Ctrl+C) to remote `stdin` instead of executing the submission, records a `WARN` severity `BLOCKED_CMD` audit log entry, writes a visible red warning banner to both the `.cast` recording and the session output stream, and invokes any registered block notification callback with the matched rule name and command

#### Scenario: Guard disabled allows input pass-through
- **WHEN** command guard is disabled for the target host
- **THEN** all input bytes are forwarded directly to remote `stdin` without interception

### Requirement: Idempotent session shutdown and lifecycle audit completion
Closing an SSH session SHALL safely release all underlying network, PTY, and recording resources exactly once, even when triggered concurrently by remote exit and local cancellation.

#### Scenario: Concurrent close performs cleanup once without leaks
- **WHEN** a session terminates remotely while the local client also invokes close
- **THEN** the session flushes and closes the `.cast` file once, updates the recording metadata with end timestamp and final byte size, writes a single `DISCONNECT` audit log entry, and closes both the SSH session channel and the underlying SSH TCP client connection

### Requirement: Streaming asciinema v2 recording reader
The core SHALL support streaming playback parsing of asciinema v2 (`.cast`) recordings line by line so clients can replay large recordings without loading the entire file into memory as a single decoded payload.

#### Scenario: Streaming reader yields header and ordered output events
- **WHEN** a caller opens a valid `.cast` recording file via the streaming reader
- **THEN** the reader parses and returns the header (including recorded `width` and `height`) first and subsequently yields timestamped `"o"` output events incrementally until EOF

### Requirement: Offline Web edition compatibility
The Web edition SHALL consume the shared SSH session core while preserving its existing HTTP/WebSocket protocol, host management, TOFU/MITM modal workflow, command guard behavior, and terminal UI without functional regression.

#### Scenario: Web terminal session behaves identically over WebSocket
- **WHEN** a user connects to a host through the offline Web edition browser interface
- **THEN** the Web server bridges WebSocket messages to the shared SSH session core, preserving status messages, TOFU/MITM prompts, high-risk block banners, PTY resizing, and `.cast` recording playback
