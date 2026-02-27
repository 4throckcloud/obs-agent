#!/usr/bin/env bash
set -euo pipefail

# ─── OBS Agent Release Script ─────────────────────────────────────────────────
#
# Usage:
#   ./release.sh 1.0.0              Build + upload to staging (GitHub prerelease)
#   ./release.sh 1.0.0 --promote    Promote to stable (GitHub latest release)
#
# R2 layout (manifests only):
#   agent/manifest.json             ← stable manifest
#   agent/manifest-staging.json     ← staging manifest
#
# GitHub Releases (4throckcloud/obs-agent):
#   vX.Y.Z prerelease → promoted to latest on --promote
#   Assets: obs-agent-{platform}.zip

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OBS_STACK_DIR="$(cd "$AGENT_DIR/.." && pwd)"
DIST_DIR="$AGENT_DIR/dist"

# R2 config (manifests only)
R2_ACCOUNT_ID="f4c0a9e2ce9585ee94c49de7c493f278"
R2_BUCKET="4throck"
R2_PUBLIC_URL="https://media.4throck.cloud"
R2_TOKEN_FILE="/home/ubuntu/production/widgets-stack/secrets/r2_api_token"
R2_BASE="https://api.cloudflare.com/client/v4/accounts/${R2_ACCOUNT_ID}/r2/buckets/${R2_BUCKET}/objects"

# Docker / GHCR config
DOCKER_IMAGE="ghcr.io/4throckcloud/obs-agent"

# GitHub config
GH_REPO="4throckcloud/obs-agent"
GH_TOKEN_FILE="/home/ubuntu/production/obs-stack/secrets/ghcr_token"

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

load_gh_token() {
    [[ -f "$GH_TOKEN_FILE" ]] || die "GitHub token file not found: $GH_TOKEN_FILE"
    export GH_TOKEN
    GH_TOKEN="$(cat "$GH_TOKEN_FILE" | tr -d '[:space:]')"
    [[ -n "$GH_TOKEN" ]] || die "GitHub token is empty"
}

# Zip name: obs-agent-windows-amd64.exe → obs-agent-windows-amd64.zip
zip_name() {
    local filename="$1"
    if [[ "$filename" == *.exe ]]; then
        echo "${filename%.exe}.zip"
    else
        echo "${filename}.zip"
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

    # Zip binaries for GitHub Release
    echo "→ Zipping binaries..."
    local zip_assets=()
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        local zname
        zname=$(zip_name "$filename")
        (cd "$DIST_DIR" && zip -j "$zname" "$filename")
        zip_assets+=("$DIST_DIR/$zname")
        echo "  ✓ $zname"
    done

    # Create GitHub prerelease with zip assets
    load_gh_token
    echo "→ Creating GitHub prerelease v${version}..."
    gh release create "v${version}" \
        --repo "$GH_REPO" \
        --prerelease \
        --title "v${version}" \
        --notes "Staging release v${version}" \
        "${zip_assets[@]}"
    echo "  ✓ GitHub prerelease created"

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
        local zname
        zname=$(zip_name "$filename")
        local dl_url="https://github.com/${GH_REPO}/releases/download/v${version}/${zname}"

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

    # Upload staging manifest to R2
    load_r2_token
    echo "→ Uploading staging manifest to R2..."
    r2_upload "$DIST_DIR/manifest-staging.json" "agent/manifest-staging.json" "application/json"

    # Docker multi-arch image build + push
    echo "→ Building Docker image (amd64 + arm64)..."
    cp "$DIST_DIR/obs-agent-linux-amd64" "$SCRIPT_DIR/obs-agent-linux-amd64"
    cp "$DIST_DIR/obs-agent-linux-arm64" "$SCRIPT_DIR/obs-agent-linux-arm64"
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --build-arg VERSION="v${version}" \
        -t "${DOCKER_IMAGE}:v${version}" \
        -t "${DOCKER_IMAGE}:staging" \
        --push \
        "$SCRIPT_DIR"
    rm -f "$SCRIPT_DIR/obs-agent-linux-amd64" "$SCRIPT_DIR/obs-agent-linux-arm64"

    echo ""
    echo "═══════════════════════════════════════════════════════════"
    echo "  Staging complete! v${version}"
    echo "  GitHub:   https://github.com/${GH_REPO}/releases/tag/v${version} (prerelease)"
    echo "  Manifest: ${R2_PUBLIC_URL}/agent/manifest-staging.json"
    echo "  Docker:   ${DOCKER_IMAGE}:v${version} / :staging"
    echo ""
    echo "  Test the staging binary, then promote:"
    echo "    ./release.sh ${version} --promote"
    echo "═══════════════════════════════════════════════════════════"
}

# ─── Promote → Stable ─────────────────────────────────────────────────────────

do_promote() {
    local version="$1"
    echo "═══════════════════════════════════════════════════════════"
    echo "  Promoting v${version} to stable"
    echo "═══════════════════════════════════════════════════════════"

    # Verify local dist exists (needed for manifest checksums)
    for entry in "${BUILDS[@]}"; do
        read -r filename _ _ <<< "$entry"
        [[ -f "$DIST_DIR/$filename" ]] || die "Local build missing: $DIST_DIR/$filename (re-run build first)"
    done
    echo "  Local dist/ verified"

    # Promote GitHub prerelease → latest release
    load_gh_token
    echo "→ Promoting GitHub release v${version} to latest..."
    gh release edit "v${version}" \
        --repo "$GH_REPO" \
        --prerelease=false \
        --latest
    echo "  ✓ GitHub release marked as latest"

    # Generate and upload stable manifest
    load_r2_token
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
        local zname
        zname=$(zip_name "$filename")
        local dl_url="https://github.com/${GH_REPO}/releases/download/v${version}/${zname}"

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

    # Docker: rebuild with :latest tag (multi-arch)
    echo "→ Building Docker :latest (amd64 + arm64)..."
    cp "$DIST_DIR/obs-agent-linux-amd64" "$SCRIPT_DIR/obs-agent-linux-amd64"
    cp "$DIST_DIR/obs-agent-linux-arm64" "$SCRIPT_DIR/obs-agent-linux-arm64"
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --build-arg VERSION="v${version}" \
        -t "${DOCKER_IMAGE}:v${version}" \
        -t "${DOCKER_IMAGE}:latest" \
        --push \
        "$SCRIPT_DIR"
    rm -f "$SCRIPT_DIR/obs-agent-linux-amd64" "$SCRIPT_DIR/obs-agent-linux-arm64"

    # Update README version badge
    local readme="$AGENT_DIR/README.md"
    if [[ -f "$readme" ]]; then
        echo "→ Updating README.md version..."
        sed -i -E "s/^\*\*Latest:\*\* v[0-9]+\.[0-9]+\.[0-9]+/**Latest:** v${version}/" "$readme"
        if git -C "$OBS_STACK_DIR" diff --quiet "$readme" 2>/dev/null; then
            echo "  (no changes needed)"
        else
            echo "  ✓ README updated to v${version}"
            git -C "$OBS_STACK_DIR" add "$readme"
            git -C "$OBS_STACK_DIR" commit -m "Release v${version}"
            git -C "$OBS_STACK_DIR" push
            echo "  ✓ Committed and pushed"
        fi
    fi

    echo ""
    echo "═══════════════════════════════════════════════════════════"
    echo "  ✓ Promoted v${version} to STABLE"
    echo "  GitHub:   https://github.com/${GH_REPO}/releases/tag/v${version}"
    echo "  Manifest: ${R2_PUBLIC_URL}/agent/manifest.json"
    echo "  Docker:   ${DOCKER_IMAGE}:latest"
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
