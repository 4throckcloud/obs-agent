VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X main.Version=$(VERSION) -s -w
LDFLAGS_WIN := $(LDFLAGS) -H windowsgui
ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))..

.PHONY: build-all build-windows build-mac-intel build-mac-arm build-linux clean

build-all: build-windows build-mac-intel build-mac-arm build-linux
	@echo "All builds complete in dist/"
	@ls -lh $(ROOT)/dist/

build-windows:
	@echo "Building Windows amd64..."
	cd $(ROOT) && GOOS=windows GOARCH=amd64 go build -mod=mod -ldflags="$(LDFLAGS_WIN)" -o dist/obs-agent-windows-amd64.exe ./cmd/agent

build-mac-intel:
	@echo "Building macOS amd64..."
	cd $(ROOT) && GOOS=darwin GOARCH=amd64 go build -mod=mod -ldflags="$(LDFLAGS)" -o dist/obs-agent-mac-intel ./cmd/agent

build-mac-arm:
	@echo "Building macOS arm64..."
	cd $(ROOT) && GOOS=darwin GOARCH=arm64 go build -mod=mod -ldflags="$(LDFLAGS)" -o dist/obs-agent-mac-apple ./cmd/agent

build-linux:
	@echo "Building Linux amd64..."
	cd $(ROOT) && GOOS=linux GOARCH=amd64 go build -mod=mod -ldflags="$(LDFLAGS)" -o dist/obs-agent-linux-amd64 ./cmd/agent

clean:
	rm -rf $(ROOT)/dist/
