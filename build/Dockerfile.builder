FROM golang:1.22-bookworm

# Cross-compile dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc-mingw-w64-x86-64 \
    gcc-aarch64-linux-gnu \
    make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
