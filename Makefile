BUILD_DIR=bin
export GOPROXY ?= https://proxy.golang.org,https://goproxy.cn,direct

.PHONY: tidy test vet dev-desktop build-desktop-mac-m4 build-desktop-win build-desktop-win-nsis fix-mac-sign clean

tidy:
	go mod tidy

test:
	go test -v -race ./...

vet:
	go tool mygo vet .

# --- Desktop Edition (MyGo Pure-Go Native UI + Ghostty libghostty-vt, Zero WebView) ---

dev-desktop:
	go tool mygo dev

build-desktop-mac-m4:
	go tool mygo build -platform darwin/arm64
	codesign --verify --deep --strict --verbose=2 dist/mygo/darwin-arm64/SSH-Guard.app

fix-mac-sign:
	./scripts/fix-mac-gatekeeper.sh dist/mygo/darwin-arm64/SSH-Guard.app

build-desktop-win: build-desktop-win-nsis

build-desktop-win-nsis:
	@set -euo pipefail; \
	cleanup() { rm -rf internal/desktopui/nativeembed dist/mygo-portable; }; \
	trap cleanup EXIT; \
	GOFLAGS=-tags=mygo_nowebview go tool mygo build -platform windows/amd64; \
	mkdir -p internal/desktopui/nativeembed; \
	cp dist/mygo/windows-amd64/ghostty-vt.dll internal/desktopui/nativeembed/ghostty-vt.dll; \
	GOFLAGS=-tags=mygo_nowebview,portable go tool mygo build -platform windows/amd64 -skip-installer -o dist/mygo-portable; \
	cp dist/mygo-portable/windows-amd64/SSH-Guard.exe dist/mygo/windows-amd64/SSH-Guard-Windows-amd64-portable.exe

clean:
	rm -rf $(BUILD_DIR) dist .mygo internal/desktopui/nativeembed
