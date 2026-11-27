# Tasks

## 1. Tab Model and SSH-Only Branching

- [x] 1.1 Add an explicit `tabKind` (`ssh` / `local`) and a display-title helper to `SessionTab`; set SSH tabs to `tabKindSSH` in `connectHost` and verify existing `internal/desktopui` tests still pass.
- [x] 1.2 Update tab counting, active-host highlighting, host deletion, and reconnect helpers to ignore local tabs, and verify with focused unit tests that a local tab does not touch host ID 0 or call `GetHost`.
- [x] 1.3 Branch workspace controls by tab kind so local tabs never show SSH reconnect or disconnect actions, and verify via headless UI assertions that local tabs expose a close-terminal action instead.

## 2. Local Terminal Creation and Entry Point

- [x] 2.1 Implement `openLocalTerminal` using `terminal.New` with no `Conn`, the user's home directory, and `ShellIntegration` enabled; verify a headless test opens a tab with `kind = local`, an empty host, no dial, and an active terminal.
- [x] 2.2 Register an `OnExit` callback that marks the local tab idle with its exit code while ignoring callbacks for already-closed tabs; verify the callback path with a focused test or a deterministic local shell exit check.
- [x] 2.3 Add the `>_ 终端` sidebar-header action and the empty-workspace call to action; verify a headless UI frame renders the action after unlock and that invoking it creates exactly one local tab.
- [x] 2.4 Add the local terminal feature to the README desktop feature list and usage notes; verify the documented action name matches the UI text.

## 3. Workspace Integration and Lifecycle

- [x] 3.1 Render local tabs in the tab strip with `本地终端` labels and per-kind sequence numbers (for example `本地终端 #2`), and verify two local tabs plus one SSH tab show distinct labels and close independently.
- [x] 3.2 Confirm local tab creation writes no recording row, no CONNECT/DISCONNECT audit entry, and never opens a TOFU prompt; verify by querying `ListRecordings` and `ListAuditLogs` around local open/close in a test.
- [x] 3.3 Ensure `LockVault` and `Shutdown` close every local tab and release its terminal; verify a test creates a local tab, locks the vault, and asserts zero tabs with the vault locked.

## 4. Integration Verification

- [x] 4.1 Run `go test -race ./...` and verify all packages pass, including the existing SSH multi-tab and TOFU tests.
- [x] 4.2 Run `go tool mygo vet .` and `openspec validate add-local-terminal-tab --strict`; verify both report no errors.
- [x] 4.3 Launch the desktop app, open two local terminal tabs and one SSH tab, resize the window, run a command in each local tab, then lock the vault; verify local shells exit, no `.cast` file or SSH audit entry is created, and SSH sessions remain unaffected before lock.
