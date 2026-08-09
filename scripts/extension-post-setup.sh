#!/usr/bin/env bash
# extension-post-setup.sh — Extension-specific setup that runs AFTER Docker Compose
# and AFTER the TEE has been registered on-chain by post-build.sh.
#
# This hook runs once the TEE node is live and registered on-chain via the
# FlareTeeManager diamond. Use it for any setup that needs the TEE's on-chain
# identity to exist — things you couldn't do in extension-setup.sh because the
# TEE didn't exist yet.
#
# For the orderbook this:
#   1. Looks up the TEE node's signing address via FlareTeeManager.getActiveTeeMachines
#      (returns the registered TEE IDs for this extension)
#   2. Calls setTeeAddress() on InstructionSender so executeWithdrawal can
#      verify TEE-signed withdrawal authorizations via ecrecover.
#
# Without this, executeWithdrawal reverts with "TEE not configured".
#
# Inputs (env vars, typically sourced from .env + config/extension.env):
#   ADDRESSES_FILE          — path to deployed-addresses.json
#   CHAIN_URL               — chain RPC URL
#   INSTRUCTION_SENDER      — InstructionSender contract address (from pre-build)
#   EXTENSION_ID            — extension id (from pre-build)
#   DEPLOYMENT_PRIVATE_KEY  — admin key for setTeeAddress call
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[extension-post-setup]${NC} $*"; }
die()  { echo -e "${RED}[extension-post-setup] ERROR:${NC} $*" >&2; exit 1; }

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

# cast/forge read the CHAIN env var as their --chain flag. The project exports
# CHAIN with its own values (local/coston/coston2), which aren't valid foundry
# chains, so keep it out of cast's environment. This script targets the chain via
# --rpc-url "$CHAIN_URL" instead.
unset CHAIN

CHAIN_URL="${CHAIN_URL:-http://127.0.0.1:8545}"
INSTRUCTION_SENDER="${INSTRUCTION_SENDER:-}"
EXTENSION_ID="${EXTENSION_ID:-}"
ADDRESSES_FILE="${ADDRESSES_FILE:-}"
DEPLOYMENT_PRIVATE_KEY="${DEPLOYMENT_PRIVATE_KEY:-}"

[[ -n "$INSTRUCTION_SENDER" ]]     || die "INSTRUCTION_SENDER not set. Run pre-build.sh first."
[[ -n "$EXTENSION_ID" ]]           || die "EXTENSION_ID not set. Run pre-build.sh first."
[[ -n "$DEPLOYMENT_PRIVATE_KEY" ]] || die "DEPLOYMENT_PRIVATE_KEY not set. Check .env."

# Resolve relative paths against PROJECT_DIR
if [[ -n "$ADDRESSES_FILE" && "$ADDRESSES_FILE" != /* ]]; then
    ADDRESSES_FILE="$PROJECT_DIR/$ADDRESSES_FILE"
fi

# Auto-detect addresses file (same logic as other scripts)
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

[[ -f "$ADDRESSES_FILE" ]] || die "Addresses file not found: $ADDRESSES_FILE"
command -v jq   >/dev/null || die "jq not found on PATH"
command -v cast >/dev/null || die "cast (foundry) not found on PATH"

FLARE_TEE_MANAGER="$(jq -r '.[] | select(.name == "FlareTeeManager").address // empty' "$ADDRESSES_FILE")"
[[ -n "$FLARE_TEE_MANAGER" ]] || die "FlareTeeManager not found in $ADDRESSES_FILE"

log "Chain URL:           $CHAIN_URL"
log "InstructionSender:   $INSTRUCTION_SENDER"
log "FlareTeeManager:     $FLARE_TEE_MANAGER"
log "Extension ID:        $EXTENSION_ID"

# Foundry auto-loads .env and treats CHAIN as its --chain flag. The project's
# CHAIN=coston isn't a valid foundry chain name, and the `unset CHAIN` above does
# NOT help — cast re-reads .env internally. Pin --chain to the RPC's numeric chain
# id; a command-line flag overrides the .env value. `cast chain-id` takes no --chain.
FCHAIN="$(cast chain-id --rpc-url "$CHAIN_URL")" || die "could not read chain id from $CHAIN_URL"

# --- Idempotency: skip if already set ---
already_set="$(cast call "$INSTRUCTION_SENDER" "teeAddressSet()(bool)" --rpc-url "$CHAIN_URL" --chain "$FCHAIN" 2>/dev/null || echo "")"
if [[ "$already_set" == "true" ]]; then
    current="$(cast call "$INSTRUCTION_SENDER" "teeAddress()(address)" --rpc-url "$CHAIN_URL" --chain "$FCHAIN")"
    log "TEE address already set on InstructionSender ($current) — nothing to do"
    exit 0
fi

# --- Look up TEE signing address from TeeMachineRegistry ---
# getActiveTeeMachines returns (address[] teeIds, string[] urls). We take the
# first entry — orderbook uses single-TEE signing (cosignersThreshold=0).
log "Querying FlareTeeManager.getActiveTeeMachines($EXTENSION_ID)..."
raw="$(cast call "$FLARE_TEE_MANAGER" \
    "getActiveTeeMachines(uint256)(address[],string[])" \
    "$EXTENSION_ID" --rpc-url "$CHAIN_URL" --chain "$FCHAIN")" || die "getActiveTeeMachines call reverted"

# First line of output is the address array: "[0xabc..., 0xdef...]" or "[]"
addrs_line="$(echo "$raw" | head -n1)"
addrs_stripped="${addrs_line//[\[\]]/}"
# Pick the first comma-separated entry, trim whitespace
tee_addr="$(echo "$addrs_stripped" | cut -d, -f1 | xargs || true)"

if [[ -z "$tee_addr" || "$tee_addr" == "0x0000000000000000000000000000000000000000" ]]; then
    die "No active TEE machines registered for extension $EXTENSION_ID. Did post-build.sh complete?"
fi

# Count how many TEEs are active — warn if more than one, since we only wire one.
num_addrs="$(echo "$addrs_stripped" | tr ',' '\n' | grep -c '0x' || true)"
if [[ "$num_addrs" -gt 1 ]]; then
    log "Note: $num_addrs active TEEs registered; using the first ($tee_addr)"
fi

log "TEE signing address: $tee_addr"

# --- Set on InstructionSender ---
log "Calling setTeeAddress($tee_addr) on $INSTRUCTION_SENDER..."
cast send "$INSTRUCTION_SENDER" "setTeeAddress(address)" "$tee_addr" \
    --rpc-url "$CHAIN_URL" --chain "$FCHAIN" --private-key "$DEPLOYMENT_PRIVATE_KEY" >/dev/null \
    || die "setTeeAddress failed"

log "TEE address set on InstructionSender — executeWithdrawal() is now enabled"
