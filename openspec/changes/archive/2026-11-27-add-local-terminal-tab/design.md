# Design

## Context

See `proposal.md` - Why. The desktop app currently creates every terminal with
`terminal.Options.Conn` bound to a `TabConn`, which forwards bytes to
`sshproxy.LiveSession`. The MyGo terminal plugin already supports the opposite
mode: when `Conn` is nil, it resolves the user's login shell from `Command` (or
`$SHELL` when empty) and runs it in a pure-Go PTY (`/dev/ptmx` on macOS,
ConPTY on Windows). The app also gates the entire workspace behind vault
unlock and tears down all tabs through `closeAllTabs`.

## Goals / Non-Goals

**Goals:**

- Add a local login-shell tab that reuses the existing native terminal engine.
- Keep local tabs independent of SSH host records, TOFU checks, SSH audits,
  command guard, and `.cast` recording.
- Preserve the existing tab strip, terminal sizing, scrollback, theme, and
  vault lock/shutdown teardown behavior.
- Keep zero WebView, zero CGO, and no new dependencies.

**Non-Goals:**

- Local shell access from the vault lock screen.
- Recording local shell output or auditing local commands.
- Applying the SSH command guard to local shell input.
- User-configurable shell, working directory, or environment in this iteration.

## Decisions

### Use the terminal plugin's command mode instead of a custom PTY adapter

`terminal.New(terminal.Options{Command: nil, Dir: home, ShellIntegration: true})`
already starts the platform login shell in a PTY and handles resize, signals,
and process exit. A custom `Conn` around a separate PTY library would be needed
only for guard/recording/audit integration, which is out of scope. This choice
avoids duplicating platform PTY code and preserves the zero-CGO guarantee.

Alternative considered: implement `LocalConn` and feed it to the existing
`TabConn`-style pipeline. Rejected for this iteration because it adds a
dependency and creates new security surface for recording local output.

### Model local tabs with an explicit tab kind, not a pseudo-host

Extend `SessionTab` with a `kind` field (`ssh` or `local`) and a display title.
Local tabs use a zero-value `host`, no `conn`, and no `dialCancel`. Tab labels,
counts, status text, and workspace actions branch on `kind`.

Alternative considered: insert a synthetic host with ID 0 into the sidebar.
Rejected because it pollutes the host inventory, risks accidental storage or
TOFU operations, and makes "delete host" semantics ambiguous.

### Keep local tabs inside the existing vault lifecycle

For this iteration, the local terminal action is visible only in the unlocked
workspace. `LockVault` and `Shutdown` continue to call `closeAllTabs`, which now
also closes local tabs. Making local terminals available on the lock screen
would require decoupling the workspace from the vault and is deferred.

### Exclude local sessions from SSH security and recording paths

Local tabs never call `sshproxy.DialSession`, never create a `CastRecorder`,
never write CONNECT/DISCONNECT audit rows, and never enqueue TOFU prompts.
This avoids capturing sensitive local output in `.cast` files and keeps the
SSH audit trail meaningful.

### Derive local tab status from terminal lifecycle callbacks

Create the tab with status `connected` and text `本地终端`. Register
`terminal.Options.OnExit` to update the tab to `idle` with an exit-code message,
guarded by tab lookup and the `closing` flag so a callback racing with tab close
does not revive removed tabs. PTY startup failures are rendered inline by the
terminal plugin and surface as a non-zero exit code.

### Put the entry point in the sidebar header

Add a compact `>_ 终端` action next to `+ 主机`. The existing empty-workspace
state can also offer the same action as a secondary call to action. Sidebar host
cards remain visually unchanged.

## Risks / Trade-offs

- **Local shell output is sensitive but untracked** -> No recording or audit is
  created; the feature is documented as a plain shell, not a guarded session.
- **Workspace actions assume SSH host metadata** -> Branch reconnect/status
  controls on `kind` so local tabs never attempt `GetHost(0)` or redial.
- **Tab close races with `OnExit`** -> `OnExit` checks `findTabByID` and
  `closing` before mutating UI state.
- **Headless tests need the Ghostty library and a real shell** -> Test tab
  creation through the app action with `terminal.Load`; skip when the native
  terminal runtime is unavailable, and test teardown via tab state assertions.
- **Windows and macOS default shells differ** -> Rely on the terminal plugin's
  platform-specific login-shell resolution instead of hard-coding a shell path.

## Migration Plan

No data migration. Existing SSH tabs continue to carry `kind = ssh`. Rollback is
limited to removing the local action and the `kind` branches; no stored state or
schema changes are involved.
