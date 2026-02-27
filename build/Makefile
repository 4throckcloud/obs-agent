VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X main.Version=$(VERSION) -s -w
LDFLAGS_WIN := -X main.Version=$(VERSION) -w -H windowsgui
ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))..

# Parse version components for goversioninfo (strip leading 'v')
VER_RAW := $(subst v,,$(VERSION))
VER_MAJOR := $(word 1,$(subst ., ,$(VER_RAW)))
VER_MINOR := $(word 2,$(subst ., ,$(VER_RAW)))
VER_PATCH := $(word 3,$(subst ., ,$(subst -, ,$(VER_RAW))))
ifeq ($(VER_MAJOR),)
VER_MAJOR := 0
endif
ifeq ($(VER_MINOR),)
VER_MINOR := 0
endif
ifeq ($(VER_PATCH),)
VER_PATCH := 0
endif

DOCKER_IMAGE := ghcr.io/4throckcloud/obs-agent

.PHONY: build-all build-windows build-mac-intel build-mac-arm build-linux build-docker push-docker clean

build-all: build-windows build-mac-intel build-mac-arm build-linux
	@echo "All builds complete in dist/"
	@ls -lh $(ROOT)/dist/

build-windows:
	@echo "Building Windows amd64..."
	cd $(ROOT)/cmd/agent && goversioninfo \
		-ver-major=$(VER_MAJOR) -ver-minor=$(VER_MINOR) -ver-patch=$(VER_PATCH) \
		-product-ver-major=$(VER_MAJOR) -product-ver-minor=$(VER_MINOR) -product-ver-patch=$(VER_PATCH) \
		-file-version="$(VERSION)" -product-version="$(VERSION)" \
		-o resource_windows_amd64.syso
	cd $(ROOT) && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -mod=mod -ldflags="$(LDFLAGS_WIN)" -o dist/obs-agent-windows-amd64.exe ./cmd/agent
	rm -f $(ROOT)/cmd/agent/resource_windows_amd64.syso

build-mac-intel:
	@echo "Building macOS amd64..."
	cd $(ROOT) && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -mod=mod -ldflags="$(LDFLAGS)" -o dist/obs-agent-mac-intel ./cmd/agent

build-mac-arm:
	@echo "Building macOS arm64..."
	cd $(ROOT) && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -mod=mod -ldflags="$(LDFLAGS)" -o dist/obs-agent-mac-apple ./cmd/agent

build-linux:
	@echo "Building Linux amd64..."
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod -ldflags="$(LDFLAGS)" -o dist/obs-agent-linux-amd64 ./cmd/agent

build-docker:
	@test -f $(ROOT)/dist/obs-agent-linux-amd64 || (echo "ERROR: dist/obs-agent-linux-amd64 not found — run build-linux first" && exit 1)
	@echo "Building Docker image $(DOCKER_IMAGE):$(VERSION)..."
	cp $(ROOT)/dist/obs-agent-linux-amd64 $(dir $(abspath $(lastword $(MAKEFILE_LIST))))obs-agent-linux-amd64
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(DOCKER_IMAGE):$(VERSION) \
		-t $(DOCKER_IMAGE):staging \
		$(dir $(abspath $(lastword $(MAKEFILE_LIST))))
	rm -f $(dir $(abspath $(lastword $(MAKEFILE_LIST))))obs-agent-linux-amd64

push-docker:
	@echo "Pushing $(DOCKER_IMAGE) tags to GHCR..."
	docker push $(DOCKER_IMAGE) --all-tags

clean:
	rm -rf $(ROOT)/dist/
