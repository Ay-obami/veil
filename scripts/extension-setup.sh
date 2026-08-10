#!/usr/bin/env bash
# extension-setup.sh — Extension-specific setup that runs BEFORE Docker Compose.
#
# This hook runs between pre-build (contract deployment) and docker-up (starting
# the TEE). Use it for any setup whose output the extension needs at startup —
# e.g. deploying auxiliary contracts, writing config files the extension reads.
#
# For the orderbook this:
#   1. Allows the deployer to deposit (KYC allowlist)
#   2. Deploys four TestToken contracts (TUSDT, TFLR, TBTC, TETH)
#   3. Updates config/pairs.json with the deployed token addresses
#   4. Mints tokens to the deployer
#   5. Approves InstructionSender to spend tokens
#   6. Writes config/test-tokens.env
#
# Token deployment (steps 2–4) is skipped automatically by test-setup when
# config/pairs.json is already fully populated — token addresses are read from
# there instead. Steps 1, 5, and 6 always run, because allow + approve are
# tied to the (possibly freshly-redeployed) InstructionSender.
#
# pairs.json is baked into the Docker image, so if you ever re-run token
# deployment you must rebuild the image before `docker compose up`.
#
# Inputs (env vars, typically sourced from .env + config/extension.env):
#   ADDRESSES_FILE       — path to deployed-addresses.json
#   CHAIN_URL            — chain RPC URL
#   INSTRUCTION_SENDER   — InstructionSender contract address (from pre-build)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[extension-setup]${NC} $*"; }
die()  { echo -e "${RED}[extension-setup] ERROR:${NC} $*" >&2; exit 1; }

# --- Load environment ---
if [[ -f "$PROJECT_DIR/.env" ]]; then
    set -a; source "$PROJECT_DIR/.env"; set +a
fi

CONFIG_FILE="$PROJECT_DIR/config/extension.env"
if [[ -f "$CONFIG_FILE" ]]; then
    source "$CONFIG_FILE"
    log "Loaded config from $CONFIG_FILE"
else
    die "config/extension.env not found — run pre-build.sh first"
fi

CHAIN_URL="${CHAIN_URL:-http://127.0.0.1:8545}"
INSTRUCTION_SENDER="${INSTRUCTION_SENDER:-}"
ADDRESSES_FILE="${ADDRESSES_FILE:-}"

[[ -n "$INSTRUCTION_SENDER" ]] || die "INSTRUCTION_SENDER not set. Run pre-build.sh first."

# Resolve relative paths against PROJECT_DIR
if [[ -n "$ADDRESSES_FILE" && "$ADDRESSES_FILE" != /* ]]; then
    ADDRESSES_FILE="$PROJECT_DIR/$ADDRESSES_FILE"
fi

# Auto-detect addresses file if not set
if [[ -z "$ADDRESSES_FILE" ]]; then
    LOCAL_MODE="${LOCAL_MODE:-true}"
    if [[ "$LOCAL_MODE" != "true" ]]; then
        candidate="$PROJECT_DIR/config/coston2/deployed-addresses.json"
        if [[ -f "$candidate" ]]; then
            ADDRESSES_FILE="$(cd "$(dirname "$candidate")" && pwd)/$(basename "$candidate")"
        fi
    fi
    if [[ -z "$ADDRESSES_FILE" ]]; then
        for candidate in \
            "$PROJECT_DIR/../../e2e/docker/sim_dump/deployed-addresses.json" \
            "$PROJECT_DIR/../docker/sim_dump/deployed-addresses.json"; do
            if [[ -f "$candidate" ]]; then
                ADDRESSES_FILE="$(cd "$(dirname "$candidate")" && pwd)/$(basename "$candidate")"
                break
            fi
        done
    fi
    [[ -n "$ADDRESSES_FILE" ]] || die "Cannot find deployed-addresses.json. Set ADDRESSES_FILE."
fi

# Resolve to absolute path
if [[ "$ADDRESSES_FILE" != /* ]]; then
    ADDRESSES_FILE="$(cd "$(dirname "$ADDRESSES_FILE")" && pwd)/$(basename "$ADDRESSES_FILE")"
fi

log "Chain URL:          $CHAIN_URL"
log "InstructionSender:  $INSTRUCTION_SENDER"
log "Addresses file:     $ADDRESSES_FILE"

# --- Run extension setup ---
cd "$PROJECT_DIR/tools"
go run ./cmd/test-setup \
    -a "$ADDRESSES_FILE" \
    -c "$CHAIN_URL" \
    -instructionSender "$INSTRUCTION_SENDER" \
    || die "Extension setup failed"

log ""
log "Extension setup complete. config/pairs.json and config/test-tokens.env written."
log "Docker Compose will pick up the correct config on startup."
