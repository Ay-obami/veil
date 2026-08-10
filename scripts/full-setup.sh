#!/usr/bin/env bash
# full-setup.sh — thin compatibility wrapper around `veil deploy all`.
#
# The deployment lifecycle this script used to hand-orchestrate
# (pre-build -> extension-setup -> start-services -> post-build ->
# extension-post-setup -> test) is now a single dependency graph run by
# the veil CLI: validate -> contracts -> extension -> machine -> frontend
# -> smoke. See docs/deployment.md for the full architecture, or run
# `veil explain <stage>` for what each stage does.
#
# Usage:
#   ./scripts/full-setup.sh                          # local devnet
#   ./scripts/full-setup.sh --chain coston2           # target Coston2
#   ./scripts/full-setup.sh --chain coston2 --dry-run # validate + plan only
#
# --test is no longer a separate flag: the smoke stage always runs as
# the last step of `veil deploy all`.
#
# --local (running the TEE + proxy as background Go processes instead
# of Docker Compose) is NOT supported by veil -- the new engine is
# Docker-only by design. If you relied on --local, run
# scripts/start-services.sh --local directly instead, then
# `veil deploy resume` to pick up from wherever it left off.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'; YELLOW='\033[0;33m'; NC='\033[0m'
warn() { echo -e "${YELLOW}[full-setup]${NC} $*"; }
die()  { echo -e "${RED}[full-setup] ERROR:${NC} $*" >&2; exit 1; }

NETWORK="local"
VEIL_ARGS=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --chain) [[ $# -ge 2 ]] || die "--chain requires a value (local|coston|coston2)"
                 NETWORK="$2"; shift 2 ;;
        --chain=*) NETWORK="${1#--chain=}"; shift ;;
        --local) die "--local is no longer supported by full-setup.sh -- see this script's header comment" ;;
        --test) warn "--test is a no-op now; the smoke stage always runs as part of 'veil deploy all'"; shift ;;
        --dry-run) VEIL_ARGS+=(-dry-run); shift ;;
        -v|--verbose) VEIL_ARGS+=(-verbose); shift ;;
        --debug) VEIL_ARGS+=(-debug); shift ;;
        *) die "Unknown argument: $1" ;;
    esac
done

cd "$PROJECT_DIR"
if command -v veil >/dev/null 2>&1; then
    veil deploy all -network "$NETWORK" "${VEIL_ARGS[@]}"
else
    go run ./cmd/veil deploy all -network "$NETWORK" "${VEIL_ARGS[@]}"
fi
