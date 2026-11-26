APP_NAME=ssh-guard
BUILD_DIR=bin
export GOPROXY ?= https://proxy.golang.org,https://goproxy.cn,direct

.PHONY: vendor tidy test run-web dev-desktop build-web-mac-m4 build-web-win build-web-win-arm64 build-web-linux build-web-all build-desktop-mac-m4 build-desktop-win build-desktop-win-nsis fix-mac-sign clean

vendor:
	./scripts/fetch-vendor.sh

tidy:
	go mod tidy

test:
	go test -v ./internal/...

# --- Web Edition (100% Offline Embedded Assets, Pure Go, Zero CGO) ---

run-web: tidy
	go run ./cmd/web -addr 127.0.0.1:8080 -data ./data -open

build-web-mac-m4: vendor tidy
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-darwin-arm64 ./cmd/web

build-web-win: vendor tidy
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-windows-amd64.exe ./cmd/web

build-web-win-arm64: vendor tidy
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-windows-arm64.exe ./cmd/web

build-web-linux: vendor tidy
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME)-web-linux-amd64 ./cmd/web

build-web-all: build-web-mac-m4 build-web-win build-web-win-arm64 build-web-linux

# --- Desktop Edition (Wails v2 Native WebView, Pure In-Process IPC, Zero Local Port) ---

dev-desktop: tidy
	wails dev

build-desktop-mac-m4: vendor tidy
	wails build -skipbindings -clean -platform darwin/arm64
	./scripts/fix-mac-gatekeeper.sh build/bin/SSH-Guard.app

fix-mac-sign:
	./scripts/fix-mac-gatekeeper.sh build/bin/SSH-Guard.app

build-desktop-win: tidy
	wails build -skipbindings -clean -platform windows/amd64

build-desktop-win-nsis: tidy
	wails build -skipbindings -clean -platform windows/amd64 -nsis

clean:
	rm -rf $(BUILD_DIR) build/bin
