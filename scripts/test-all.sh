#!/usr/bin/env bash
# test-all.sh — Run all orderbook test commands in sequence.
#
# Prerequisites:
#   - Full setup completed (phases 1-3)
#   - Extension TEE + proxy running
#   - config/extension.env exists
#
# Usage:
#   ./scripts/test-all.sh              # run all tests
#   ./scripts/test-all.sh --skip-setup # skip test-setup (tokens already deployed)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[test-all]${NC} $*"; }
step() { echo -e "\n${CYAN}══════════════════════════════════════${NC}"; echo -e "${CYAN}  $1${NC}"; echo -e "${CYAN}══════════════════════════════════════${NC}"; }
die()  { echo -e "${RED}[test-all] ERROR:${NC} $*" >&2; exit 1; }

SKIP_SETUP=false
for arg in "$@"; do
    case "$arg" in
        --skip-setup) SKIP_SETUP=true ;;
    esac
done

# --- Load environment ---
if [[ -f "$PROJECT_DIR/.env" ]]; then
    set -a; source "$PROJECT_DIR/.env"; set +a
fi

CONFIG_FILE="$PROJECT_DIR/config/extension.env"
if [[ -f "$CONFIG_FILE" ]]; then
    source "$CONFIG_FILE"
fi

# Auto-detect proxy URL.
if [[ -z "${EXT_PROXY_URL:-}" ]]; then
    if docker compose -f "$PROJECT_DIR/docker-compose.yaml" ps ext-proxy --status running 2>/dev/null | grep -q ext-proxy; then
        EXT_PROXY_URL="http://localhost:6674"
    else
        EXT_PROXY_URL="http://localhost:6664"
    fi
fi

CHAIN_URL="${CHAIN_URL:-http://127.0.0.1:8545}"
INSTRUCTION_SENDER="${INSTRUCTION_SENDER:-}"

# Auto-detect addresses file.
ADDRESSES_FILE="${ADDRESSES_FILE:-}"
if [[ -z "$ADDRESSES_FILE" ]]; then
    for candidate in \
        "$PROJECT_DIR/config/coston2/deployed-addresses.json" \
        "$PROJECT_DIR/../../e2e/docker/sim_dump/deployed-addresses.json" \
        "$PROJECT_DIR/../docker/sim_dump/deployed-addresses.json"; do
        if [[ -f "$candidate" ]]; then
            ADDRESSES_FILE="$(cd "$(dirname "$candidate")" && pwd)/$(basename "$candidate")"
            break
        fi
    done
fi

[[ -n "$INSTRUCTION_SENDER" ]] || die "INSTRUCTION_SENDER not set. Run 'veil deploy contracts' first."
[[ -n "$ADDRESSES_FILE" ]] || die "ADDRESSES_FILE not found."

# Resolve to absolute path.
if [[ "$ADDRESSES_FILE" != /* ]]; then
    ADDRESSES_FILE="$PROJECT_DIR/$ADDRESSES_FILE"
fi

log "Chain URL:          $CHAIN_URL"
log "Proxy URL:          $EXT_PROXY_URL"
log "InstructionSender:  $INSTRUCTION_SENDER"
log "Addresses file:     $ADDRESSES_FILE"

COMMON_FLAGS="-a $ADDRESSES_FILE -c $CHAIN_URL -p $EXT_PROXY_URL -instructionSender $INSTRUCTION_SENDER"

cd "$PROJECT_DIR/tools"

# --- Step 1: Setup ---
if [[ "$SKIP_SETUP" == "false" ]]; then
    step "test-setup"
    go run ./cmd/test-setup $COMMON_FLAGS || die "test-setup failed"
    log ""
    log "Restart the extension now to pick up new pairs.json,"
    log "then press Enter to continue..."
    read -r
fi

# --- Step 2: Deposits ---
step "test-deposit"
go run ./cmd/test-deposit $COMMON_FLAGS || die "test-deposit failed"

# --- Step 3: Orders ---
step "test-orders"
go run ./cmd/test-orders $COMMON_FLAGS || die "test-orders failed"

# --- Step 4: Withdrawals ---
step "test-withdraw"
go run ./cmd/test-withdraw $COMMON_FLAGS || die "test-withdraw failed"

echo ""
echo -e "${GREEN}════════════════════════════════════════${NC}"
echo -e "${GREEN}  All tests passed!${NC}"
echo -e "${GREEN}════════════════════════════════════════${NC}"
