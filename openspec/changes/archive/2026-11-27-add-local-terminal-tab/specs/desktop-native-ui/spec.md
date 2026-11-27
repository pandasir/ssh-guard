# Spec Delta

## ADDED Requirements

### Requirement: Local login-shell tabs in the native terminal workspace
While the vault is unlocked, the desktop workspace SHALL allow the user to open
a local login-shell tab rendered by the native terminal engine without selecting
or creating an SSH host. Local tabs SHALL remain outside the SSH connection,
TOFU, command-guard, audit, and recording flows.

#### Scenario: Opening a local terminal starts a host-independent shell tab
- **WHEN** the user activates the local terminal action in the unlocked workspace
- **THEN** a new active tab labeled as a local terminal opens, runs the user's
  local login shell in a pseudo-terminal, and performs no SSH dial, TOFU check,
  command-guard setup, audit write, or `.cast` recording creation

#### Scenario: Local shell exit is reported without disturbing SSH tabs
- **WHEN** the local login shell exits or fails to start
- **THEN** the affected local tab shows the exit or startup error inline, keeps
  the tab closable, and leaves all SSH session tabs connected and unchanged

#### Scenario: Closing a local terminal tab releases its shell resources
- **WHEN** the user closes a local terminal tab
- **THEN** the local shell process and its pseudo-terminal are terminated and
  released without producing an SSH disconnect audit entry

#### Scenario: Vault lock and application exit close local terminal tabs
- **WHEN** the vault locks automatically or manually, or the application exits
- **THEN** every local terminal tab and its shell process are closed together
  with the SSH sessions before the vault key is wiped
