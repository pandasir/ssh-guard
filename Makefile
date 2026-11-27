APP_NAME=ssh-guard
BUILD_DIR=bin
export GOPROXY ?= https://proxy.golang.org,https://goproxy.cn,direct

.PHONY: vendor tidy test vet run-web dev-desktop build-web-mac-m4 build-web-win build-web-win-arm64 build-web-linux build-web-all build-desktop-mac-m4 build-desktop-win build-desktop-win-nsis fix-mac-sign clean

vendor:
	./scripts/fetch-vendor.sh

tidy:
	go mod tidy

test:
	go test -v -race ./...

vet:
	go tool mygo vet .

# --- Web Edition (100% Offline Embedded Assets, Pure Go, Zero CGO, Zero MyGo) ---

run-web:
	go run ./cmd/web -addr 127.0.0.1:8080 -data ./data -open

build-web-mac-m4: vendor
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-darwin-arm64 ./cmd/web

build-web-win: vendor
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-windows-amd64.exe ./cmd/web

build-web-win-arm64: vendor
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-windows-arm64.exe ./cmd/web

build-web-linux: vendor
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-linux-amd64 ./cmd/web

build-web-all: build-web-mac-m4 build-web-win build-web-win-arm64 build-web-linux

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
