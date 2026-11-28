# Spec Delta: desktop-packaging

## MODIFIED Requirements

### Requirement: Release workflow publishes complete checksummed artifacts
The GitHub Actions release workflow SHALL run unit tests and publish macOS DMG, Windows NSIS installer, Windows single-file portable executable, signed in-app update archives, available delta patches, platform update manifests, and `SHA256SUMS.txt` on version tag pushes.

#### Scenario: Tag release produces verified artifact set
- **WHEN** a `v*` git tag is pushed or the release workflow is manually dispatched
- **THEN** all desktop build jobs execute on Go 1.27, generate `SSH-Guard-macOS-arm64.dmg`, `SSH-Guard-Windows-amd64-installer.exe`, `SSH-Guard-Windows-amd64-portable.exe`, signed platform update archives (`ssh-guard-*-darwin-arm64.tar.gz` and `ssh-guard-*-windows-amd64.tar.gz`), applicable `.delta` patches from prior signed releases, and platform manifests (`update-darwin-arm64.json` and `update-windows-amd64.json`) without building Web binaries, and compute `SHA256SUMS.txt` covering every published file

## ADDED Requirements

### Requirement: Ed25519-signed update feed configuration and portable build isolation
The desktop build configuration SHALL embed the Ed25519 update verification public key and GitHub Releases feed reference, source release notes from a root `CHANGELOG.md`, sign update artifacts only via out-of-repo signing credentials, and isolate the Windows single-file portable build from overwriting Windows NSIS update manifests.

#### Scenario: Build metadata embeds public key and extracts release notes from CHANGELOG.md
- **WHEN** building release desktop artifacts with the repository configuration and signing key supplied via `MYGO_UPDATER_PRIVATE_KEY`
- **THEN** the built macOS `.app` and Windows executables embed the configured Ed25519 public key and `pandasir/ssh-guard` GitHub Releases feed, the private signing key is never committed to the repository, and generated `update-*.json` manifests include the matching version section from `CHANGELOG.md`

#### Scenario: Windows portable build does not overwrite or collide with NSIS update manifests
- **WHEN** the Windows release pipeline builds both the NSIS installer and the single-file portable executable (`-tags=mygo_nowebview,portable`)
- **THEN** only the Windows NSIS build generates and signs `ssh-guard-*-windows-amd64.tar.gz`, Windows `.delta` patches, and `update-windows-amd64.json`, while the portable build runs without signing-key emission so it neither collides with the NSIS manifest nor triggers directory-based self-replacement
