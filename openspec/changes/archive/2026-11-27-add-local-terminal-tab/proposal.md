# Proposal

## Why

SSH-Guard currently requires an SSH host entry even when the user only needs a
local shell, which makes the app cumbersome as a daily terminal and hides the
fact that the bundled Ghostty terminal engine already runs local PTYs. Exposing
a local terminal tab reuses the existing native terminal stack, keeps the
zero-WebView and zero-CGO guarantees, and gives users a quick local workspace
without weakening the SSH zero-trust flow.

## What Changes

- Add a "本地终端" action in the unlocked workspace that opens a new terminal
  tab running the user's local login shell.
- Run the local shell through the existing `terminal` plugin's native command
  mode (no `Conn`), so the app continues to use the same Ghostty renderer,
  scrollback limits, theme, and pure-Go PTY implementation.
- Extend the session tab model with an explicit tab kind so local tabs can
  share the tab strip and terminal view without being treated as SSH hosts.
- Local tabs do not create SSH connections, TOFU prompts, audit entries, command
  guard interception, or `.cast` recordings in this first iteration.
- Keep the current vault lifecycle boundary: local terminal tabs are available
  only while the vault is unlocked and are closed when the vault locks or the
  application exits.
- Show local shell start failures inline in the affected tab and allow the tab
  to be closed without affecting SSH sessions.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `desktop-native-ui`: the multi-tab terminal workspace gains local terminal
  tabs alongside SSH session tabs, with distinct lifecycle and security
  behavior.

## Impact

- `internal/desktopui/session_tab.go`: add tab-kind state and a local terminal
  creation path that bypasses `sshproxy.DialSession`.
- `internal/desktopui/sidebar.go`: add the local terminal entry point.
- `internal/desktopui/workspace.go`: render local tab labels and status without
  SSH host metadata.
- `internal/desktopui/app.go`: keep local tabs within the existing vault
  lock/shutdown teardown.
- `internal/desktopui/desktopui_test.go`: cover local tab creation, tab
  switching/closing, and vault-lock cleanup without dialing SSH.
- No new dependencies, database migrations, or SSH core changes.
