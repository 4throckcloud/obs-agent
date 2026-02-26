FROM golang:1.22-bookworm

# Cross-compile dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc-mingw-w64-x86-64 \
    gcc-aarch64-linux-gnu \
    make \
    && rm -rf /var/lib/apt/lists/*

# Windows VERSIONINFO resource generator (install to /usr/local/bin so volume mount doesn't mask it)
RUN GOBIN=/usr/local/bin go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.1

WORKDIR /workspace
