#!/usr/bin/env bash
#
# Start extension TEE node and proxy.
#
# By default, starts services via Docker Compose, picking the compose overlay
# from --chain (or env CHAIN, or legacy LOCAL_MODE):
#   --chain local    → docker-compose.yaml only (local devnet)
#   --chain coston   → + docker-compose.coston.yaml
#   --chain coston2  → + docker-compose.coston2.yaml
#
# Prerequisites:
#   - Infrastructure running (Hardhat, indexer, Redis, normal TEE + proxy)
#   - EXTENSION_ID set (from `veil status`'s `outputs.extensionId`, or manually)
#   - Redis will be started on :6382 automatically (separate from infrastructure Redis)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[start-services]${NC} $*"; }
die()  { echo -e "${RED}[start-services] ERROR:${NC} $*" >&2; exit 1; }

# --- Parse flags ---
USE_LOCAL=false
CHAIN="${CHAIN:-}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --local) USE_LOCAL=true; shift ;;
        --chain) [[ $# -ge 2 ]] || die "--chain requires a value (local|coston|coston2)"
                 CHAIN="$2"; shift 2 ;;
        --chain=*) CHAIN="${1#--chain=}"; shift ;;
        *) die "Unknown argument: $1" ;;
    esac
done

# --- Load .env from project root (if present) ---
if [[ -f "$PROJECT_DIR/.env" ]]; then
    set -a
    source "$PROJECT_DIR/.env"
    set +a
fi

# --- Load extension config ---
CONFIG_FILE="$PROJECT_DIR/config/extension.env"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

EXTENSION_ID="${EXTENSION_ID:-}"
PROXY_PRIVATE_KEY="${PROXY_PRIVATE_KEY:-0x983760a4ebf75b2ac3a93531168a0f225d01e5dc6e3568adbd46233ba1fb4fa4}"
LOCAL_MODE="${LOCAL_MODE:-true}"

# --- Resolve CHAIN (flag > env > legacy LOCAL_MODE) ---
if [[ -z "$CHAIN" ]]; then
    if [[ "$LOCAL_MODE" == "true" ]]; then
        CHAIN="local"
    else
        CHAIN="coston2"  # legacy
    fi
fi
case "$CHAIN" in
    local|coston|coston2) ;;
    *) die "Unknown --chain value: $CHAIN (valid: local, coston, coston2)" ;;
esac

[[ -n "$EXTENSION_ID" ]] || die "EXTENSION_ID not set. Run pre-build.sh first or set it manually."

log "Chain:        $CHAIN"
log "Extension ID: $EXTENSION_ID"
log "Local mode:   $LOCAL_MODE"

# ============================================================
# Docker Compose mode (default)
# ============================================================
if [[ "$USE_LOCAL" == "false" ]]; then
    log "Starting services with Docker Compose..."

    # Dockerfile expects SOURCE_DATE_EPOCH for reproducible builds — see REPRODUCIBILITY.md.
    # Without it, `touch -h -d @${SOURCE_DATE_EPOCH}` in the builder stage fails with "invalid date format '@'".
    if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then
        if SOURCE_DATE_EPOCH=$(git -C "$PROJECT_DIR" log -1 --format=%ct 2>/dev/null) && [[ -n "$SOURCE_DATE_EPOCH" ]]; then
            export SOURCE_DATE_EPOCH
        else
            export SOURCE_DATE_EPOCH=0
        fi
    fi
    log "SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH"

    # --- Build tee-proxy image locally if no remote registry is configured ---
    if [[ -z "${REGISTRY:-}" ]]; then
        if ! docker image inspect local/tee-proxy >/dev/null 2>&1; then
            TEE_ROOT="$(cd "$PROJECT_DIR/../.." && pwd)"
            TEE_PROXY_DIR="$TEE_ROOT/tee-proxy"
            if [[ ! -d "$TEE_PROXY_DIR" ]]; then
                die "Image local/tee-proxy not found and tee-proxy repo not present at $TEE_PROXY_DIR.\n  Either set REGISTRY in .env to pull from a remote registry, or clone the tee-proxy repo into $TEE_ROOT/."
            fi
            # tee-proxy builds self-contained: tee-node is a normal module dependency
            # (fetched via `go mod download`), so the build context is tee-proxy/ itself.
            log "Building local/tee-proxy image from $TEE_PROXY_DIR..."
            docker build -f "$TEE_PROXY_DIR/Dockerfile" -t local/tee-proxy "$TEE_PROXY_DIR" || die "Failed to build tee-proxy image"
            log "local/tee-proxy image built successfully"
        else
            log "local/tee-proxy image already exists (use 'docker rmi local/tee-proxy' to force rebuild)"
        fi
    fi

    COMPOSE_FILES=("-f" "$PROJECT_DIR/docker-compose.yaml")

    case "$CHAIN" in
        local) ;;
        coston)
            log "Coston mode — attaching docker-compose.coston.yaml"
            COMPOSE_FILES+=("-f" "$PROJECT_DIR/docker-compose.coston.yaml")
            ;;
        coston2)
            log "Coston2 mode — attaching docker-compose.coston2.yaml"
            COMPOSE_FILES+=("-f" "$PROJECT_DIR/docker-compose.coston2.yaml")
            ;;
    esac

    # Reproducible builds: clamp mtimes to the commit timestamp.
    # docker-compose.yaml forwards this to the Dockerfile as a build arg.
    if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then
        if SOURCE_DATE_EPOCH="$(git -C "$PROJECT_DIR" log -1 --format=%ct 2>/dev/null)" && [[ -n "$SOURCE_DATE_EPOCH" ]]; then
            export SOURCE_DATE_EPOCH
        else
            export SOURCE_DATE_EPOCH="$(date +%s)"
            warn "Not a git repo or no commits — using current time as SOURCE_DATE_EPOCH (build not reproducible)"
        fi
    fi

    docker compose "${COMPOSE_FILES[@]}" up -d --build || die "docker compose up failed"

    # Wait for proxy to be ready
    E2E="$SCRIPT_DIR/e2e.sh"
    EXT_PROXY_URL="${EXT_PROXY_URL:-http://localhost:6674}"
    log "Waiting for extension proxy at $EXT_PROXY_URL/info ..."
    "$E2E" wait-for-url "$EXT_PROXY_URL/info" 120

    # Validate EXTENSION_ID is recognized by proxy
    log "Validating EXTENSION_ID against proxy..."
    PROXY_INFO=$(curl -sf "$EXT_PROXY_URL/info" 2>/dev/null || true)
    if [[ -n "$PROXY_INFO" ]]; then
        if ! echo "$PROXY_INFO" | grep -q "$EXTENSION_ID" 2>/dev/null; then
            echo -e "${RED}WARNING: EXTENSION_ID $EXTENSION_ID not found in proxy /info response${NC}" >&2
            echo -e "${RED}The proxy may be filtering for a different extension. Check config.${NC}" >&2
        fi
    fi

    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN} Services started (Docker Compose)${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    echo -e "${CYAN}Mode${NC}"
    case "$CHAIN" in
        local)   echo "  Local devnet" ;;
        coston)  echo "  Coston testnet (chain_id=16)" ;;
        coston2) echo "  Coston2 testnet (chain_id=114)" ;;
    esac
    echo ""
    echo -e "${CYAN}Services${NC}"
    echo "  redis, ext-proxy, extension-tee"
    echo "  Proxy URL: $EXT_PROXY_URL"
    echo ""
    echo -e "${CYAN}Commands${NC}"
    echo "  Logs:    docker compose ${COMPOSE_FILES[*]} logs -f"
    echo "  Stop:    ./scripts/stop-services.sh --chain $CHAIN"
    exit 0
fi


# ============================================================
# Local Go process mode (--local) — REMOVED
# ============================================================
# This mode ran the extension TEE and proxy as background Go processes via
# tools/cmd/start-tee and tools/cmd/start-proxy. tools/cmd/start-proxy
# depended on github.com/flare-foundation/tee-proxy via a local path
# `replace` directive pointing at a sibling checkout (../../../tee-proxy) —
# meaning this repo could not be built or run standalone by anyone who
# didn't also have that sibling repo checked out, for ANY mode, not just
# --local. It has been removed so the repo builds and runs on its own.
#
# Use Docker Compose mode instead (the default — just omit --local).
die "--local mode has been removed (see this script's header comment). Use Docker Compose mode instead: ./scripts/start-services.sh --chain <local|coston|coston2>"
