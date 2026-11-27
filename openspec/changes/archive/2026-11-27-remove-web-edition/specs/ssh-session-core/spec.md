# Spec Delta

## REMOVED Requirements

### Requirement: Offline Web edition compatibility
**Reason**: The offline Web edition (`cmd/web`, `web/`, `internal/api`, `internal/auth`, and the WebSocket transport bridge) is being retired so SSH-Guard focuses exclusively on its pure-Go native desktop edition.
**Migration**: Use the native desktop edition (`SSH-Guard` built from `main.go`) which consumes the `internal/sshproxy` session core directly in-process without HTTP or WebSocket transport layers.
