#!/usr/bin/env bash
set -euo pipefail

# ─── OBS Agent Release Script ─────────────────────────────────────────────────
#
# Usage:
#   ./release.sh 1.0.0              Build + upload to staging
#   ./release.sh 1.0.0 --promote    Upload local dist/ → stable (users see it now)
#
# R2 layout:
#   agent/manifest.json             ← stable manifest (has download_url per build)
#   agent/manifest-staging.json     ← staging manifest
#   agent/v1.0.0/                   ← versioned binaries
#   agent/latest/                   ← stable copies (versioned filenames for CDN cache busting)
#   agent/staging/                  ← staging copies

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OBS_STACK_DIR="$(cd "$AGENT_DIR/.." && pwd)"
DIST_DIR="$AGENT_DIR/dist"

# R2 config
R2_ACCOUNT_ID="f4c0a9e2ce9585ee94c49de7c493f278"
R2_BUCKET="4throck"
R2_PUBLIC_URL="https://media.4throck.cloud"
R2_TOKEN_FILE="/home/ubuntu/production/widgets-stack/secrets/r2_api_token"
R2_BASE="https://api.cloudflare.com/client/v4/accounts/${R2_ACCOUNT_ID}/r2/buckets/${R2_BUCKET}/objects"

# Docker / GHCR config
DOCKER_IMAGE="ghcr.io/4throckcloud/obs-agent"

# Binary definitions: name os arch
BUILDS=(
    "obs-agent-windows-amd64.exe windows amd64"
    "obs-agent-mac-intel darwin amd64"
    "obs-agent-mac-apple darwin arm64"
    "obs-agent-linux-amd64 linux amd64"
)

# Platform labels for manifest
declare -A PLATFORM_LABELS=(
    ["obs-agent-windows-amd64.exe"]="Windows"
    ["obs-agent-mac-intel"]="macOS Intel"
    ["obs-agent-mac-apple"]="macOS Apple Silicon"
    ["obs-agent-linux-amd64"]="Linux"
)

# ─── Helpers ───────────────────────────────────────────────────────────────────

die() { echo "ERROR: $*" >&2; exit 1; }

validate_semver() {
    [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Invalid version: $1 (must be X.Y.Z)"
}

load_r2_token() {
    [[ -f "$R2_TOKEN_FILE" ]] || die "R2 token file not found: $R2_TOKEN_FILE"
    R2_TOKEN="$(cat "$R2_TOKEN_FILE" | tr -d '[:space:]')"
    [[ -n "$R2_TOKEN" ]] || die "R2 token is empty"
}

# Versioned filename: obs-agent-windows-amd64.exe → obs-agent-windows-amd64-v1.3.0.exe
versioned_name() {
    local filename="$1" version="$2"
    if [[ "$filename" == *.exe ]]; then
        echo "${filename%.exe}-v${version}.exe"
    else
        echo "${filename}-v${version}"
    fi
}

r2_upload() {
    local file="$1" key="$2" content_type="${3:-application/octet-stream}"
    local response
    response=$(curl -s -w "\n%{http_code}" \
        -X PUT \
        -H "Authorization: Bearer $R2_TOKEN" \
        -H "Content-Type: $content_type" \
        --data-binary "@$file" \
        "$R2_BASE/$key")
    local http_code
    http_code=$(echo "$response" | tail -1)
    local body
    body=$(echo "$response" | sed '$d')

    if [[ "$http_code" -ge 200 && "$http_code" -lt 300 ]]; then
        local size
        size=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('result',{}).get('size','?'))" 2>/dev/null || echo "?")
        echo "  ✓ $key (${size} bytes)"
    else
        die "R2 upload failed for $key (HTTP $http_code): $body"
    fi
}

r2_verify_public() {
    local url="$1" expected_size="$2"
    local actual_size
    actual_size=$(curl -sI "$url" 2>/dev/null | grep -i content-length | awk '{print $2}' | tr -d '\r')
    if [[ "$actual_size" == "$expected_size" ]]; then
        echo "  ✓ Verified: $url (${actual_size} bytes)"
    else
        echo "  ✗ MISMATCH: $url (expected ${expected_size}, got ${actual_size:-empty})"
        return 1
    fi
}

# ─── Build + Stage ─────────────────────────────────────────────────────────────

do_build() {
    local version="$1"
    echo "═══════════════════════════════════════════════════════════"
    echo "  Building obs-agent v${version} (staging)"
    echo "═══════════════════════════════════════════════════════════"

    # Clean dist
    rm -rf "$DIST_DIR"
    mkdir -p "$DIST_DIR"

    # Build all platforms via Docker
    echo "→ Building binaries..."
    (cd "$OBS_STACK_DIR" && docker compose run --rm obs-agent-builder make -C build build-all VERSION="v${version}")

    # Verify builds exist
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        [[ -f "$DIST_DIR/$filename" ]] || die "Build artifact missing: $DIST_DIR/$filename"
    done

    # UPX compress (best-effort, skip Windows — UPX is the #1 AV false-positive trigger)
    echo "→ Compressing binaries with UPX..."
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        if [[ "$filename" == *.exe ]]; then
            echo "  SKIP: $filename (UPX triggers Windows AV false positives)"
            continue
        fi
        echo "  UPX: $filename"
        upx --best "$DIST_DIR/$filename" 2>/dev/null || echo "  (UPX skipped for $filename)"
    done

    # Generate manifest with checksums + download URLs
    echo "→ Generating manifest..."
    local manifest_builds="["
    local first=true
    for entry in "${BUILDS[@]}"; do
        read -r filename os arch <<< "$entry"
        local filepath="$DIST_DIR/$filename"
        local sha256
        sha256=$(sha256sum "$filepath" | cut -d' ' -f1)
        local size
        size=$(stat -c%s "$filepath")
        local platform="${PLATFORM_LABELS[$filename]}"
        local vname
        vname=$(versioned_name "$filename" "$version")
        local dl_url="${R2_PUBLIC_URL}/agent/latest/${vname}"

        echo "  $filename: sha256=$sha256 size=$size"

        $first || manifest_builds+=","
        first=false
        manifest_builds+=$(cat <<ENDJSON
{
      "platform": "$platform",
      "os": "$os",
      "arch": "$arch",
      "filename": "$filename",
      "download_url": "$dl_url",
      "sha256": "$sha256",
      "size": $size
    }
ENDJSON
)
    done
    manifest_builds+="]"

    local released_at
    released_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    local manifest
    manifest=$(cat <<ENDJSON
{
  "version": "v${version}",
  "channel": "staging",
  "released_at": "$released_at",
  "docker": "${DOCKER_IMAGE}:v${version}",
  "builds": $manifest_builds
}
ENDJSON
)
    echo "$manifest" > "$DIST_DIR/manifest-staging.json"

    # Upload to R2
    load_r2_token
    echo "→ Uploading to R2..."

    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        r2_upload "$DIST_DIR/$filename" "agent/v${version}/$filename"
        r2_upload "$DIST_DIR/$filename" "agent/staging/$filename"
    done

    r2_upload "$DIST_DIR/manifest-staging.json" "agent/manifest-staging.json" "application/json"

    # Docker image build + push
    echo "→ Building Docker image..."
    cp "$DIST_DIR/obs-agent-linux-amd64" "$SCRIPT_DIR/obs-agent-linux-amd64"
    docker build \
        --build-arg VERSION="v${version}" \
        -t "${DOCKER_IMAGE}:v${version}" \
        -t "${DOCKER_IMAGE}:staging" \
        "$SCRIPT_DIR"
    rm -f "$SCRIPT_DIR/obs-agent-linux-amd64"

    echo "→ Pushing Docker image to GHCR..."
    docker push "${DOCKER_IMAGE}:v${version}"
    docker push "${DOCKER_IMAGE}:staging"

    echo ""
    echo "═══════════════════════════════════════════════════════════"
    echo "  Staging complete! v${version}"
    echo "  Download: ${R2_PUBLIC_URL}/agent/staging/"
    echo "  Manifest: ${R2_PUBLIC_URL}/agent/manifest-staging.json"
    echo "  Docker:   ${DOCKER_IMAGE}:v${version} / :staging"
    echo ""
    echo "  Test the staging binary, then promote:"
    echo "    ./release.sh ${version} --promote"
    echo "═══════════════════════════════════════════════════════════"
}

# ─── Promote Local Dist → Stable ─────────────────────────────────────────────

do_promote() {
    local version="$1"
    echo "═══════════════════════════════════════════════════════════"
    echo "  Promoting v${version} to stable"
    echo "═══════════════════════════════════════════════════════════"

    # Verify local dist exists (upload from local, not r2_copy — avoids CDN staleness)
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        [[ -f "$DIST_DIR/$filename" ]] || die "Local build missing: $DIST_DIR/$filename (re-run build first)"
    done
    echo "  Local dist/ verified"

    load_r2_token

    # Upload from local dist → latest/ with versioned filenames
    echo "→ Uploading versioned binaries to latest/..."
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        local vname
        vname=$(versioned_name "$filename" "$version")
        r2_upload "$DIST_DIR/$filename" "agent/latest/$vname"
    done

    # Generate and upload stable manifest
    echo "→ Generating stable manifest..."
    local manifest_builds="["
    local first=true
    for entry in "${BUILDS[@]}"; do
        read -r filename os arch <<< "$entry"
        local filepath="$DIST_DIR/$filename"
        local sha256
        sha256=$(sha256sum "$filepath" | cut -d' ' -f1)
        local size
        size=$(stat -c%s "$filepath")
        local platform="${PLATFORM_LABELS[$filename]}"
        local vname
        vname=$(versioned_name "$filename" "$version")
        local dl_url="${R2_PUBLIC_URL}/agent/latest/${vname}"

        $first || manifest_builds+=","
        first=false
        manifest_builds+=$(cat <<ENDJSON
{
      "platform": "$platform",
      "os": "$os",
      "arch": "$arch",
      "filename": "$filename",
      "download_url": "$dl_url",
      "sha256": "$sha256",
      "size": $size
    }
ENDJSON
)
    done
    manifest_builds+="]"

    local released_at
    released_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    local manifest
    manifest=$(cat <<ENDJSON
{
  "version": "v${version}",
  "channel": "stable",
  "released_at": "$released_at",
  "docker": "${DOCKER_IMAGE}:latest",
  "builds": $manifest_builds
}
ENDJSON
)
    local tmpfile
    tmpfile=$(mktemp)
    echo "$manifest" > "$tmpfile"
    r2_upload "$tmpfile" "agent/manifest.json" "application/json"
    rm -f "$tmpfile"

    # Docker: tag as :latest and push
    echo "→ Tagging Docker image as :latest..."
    docker tag "${DOCKER_IMAGE}:v${version}" "${DOCKER_IMAGE}:latest"
    echo "→ Pushing :latest to GHCR..."
    docker push "${DOCKER_IMAGE}:latest"

    # Verify public URLs actually serve the right files
    echo "→ Verifying public downloads..."
    sleep 2  # Brief pause for R2 propagation
    local verify_ok=true
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        local vname
        vname=$(versioned_name "$filename" "$version")
        local expected_size
        expected_size=$(stat -c%s "$DIST_DIR/$filename")
        r2_verify_public "${R2_PUBLIC_URL}/agent/latest/${vname}" "$expected_size" || verify_ok=false
    done

    echo ""
    echo "═══════════════════════════════════════════════════════════"
    if $verify_ok; then
        echo "  ✓ Promoted v${version} to STABLE — all downloads verified"
    else
        echo "  ⚠ Promoted v${version} to STABLE — some downloads may be cached"
        echo "  CDN may take a few minutes to serve new files"
    fi
    echo "  Manifest:  ${R2_PUBLIC_URL}/agent/manifest.json"
    echo "  Docker:    ${DOCKER_IMAGE}:latest"
    echo "═══════════════════════════════════════════════════════════"
}

# ─── Main ──────────────────────────────────────────────────────────────────────

[[ $# -ge 1 ]] || die "Usage: $0 <version> [--promote]"

VERSION="$1"
validate_semver "$VERSION"

if [[ "${2:-}" == "--promote" ]]; then
    do_promote "$VERSION"
else
    do_build "$VERSION"
fi
