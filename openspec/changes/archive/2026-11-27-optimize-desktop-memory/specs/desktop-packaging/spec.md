# desktop-packaging Delta Specification

## MODIFIED Requirements

### Requirement: Windows 100% offline single-file portable executable
The Windows release pipeline SHALL also produce a standalone, zero-install single-file portable executable (`SSH-Guard-Windows-amd64-portable.exe`) that operates 100% offline with no accompanying external DLL files required beside the executable, extracting and verifying its embedded native terminal library lazily with streaming hashing only when the terminal subsystem is first initialized.

#### Scenario: First offline launch extracts verified native library atomically
- **WHEN** a user copies only `SSH-Guard-Windows-amd64-portable.exe` to an offline Windows 10 (1809+) / 11 machine, launches it without `MYGO_GHOSTTY_VT` preset, and initializes the terminal subsystem for the first time (by opening an SSH session or starting a recording replay)
- **THEN** before loading the terminal subsystem (and without extracting prematurely on the vault lock screen), the executable verifies or atomically writes the embedded `ghostty-vt.dll` (temp file + rename) to `%LocalAppData%\SSH-Guard\natives\ghostty-vt-<sha256-prefix>.dll`, sets `MYGO_GHOSTTY_VT` to that path, and initializes the terminal cleanly

#### Scenario: Subsequent launches reuse cached library without rewriting
- **WHEN** `SSH-Guard-Windows-amd64-portable.exe` initializes the terminal subsystem again and the target cached DLL already exists with a matching file size and SHA-256 digest
- **THEN** the executable verifies the cached file's digest using streaming I/O without allocating a full-file heap buffer, skips extraction, and loads the cached DLL directly

#### Scenario: Explicit environment override takes precedence
- **WHEN** `MYGO_GHOSTTY_VT` is already set in the environment prior to launch
- **THEN** the portable executable respects the user-supplied path and does not overwrite it
