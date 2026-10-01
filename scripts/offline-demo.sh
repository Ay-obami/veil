#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "== Veil offline verification =="
go version

echo
echo "== focused unit/integration packages =="
go test ./pkg/orderbook ./pkg/solvency ./internal/extension

echo
echo "== race detector: matching + extension state =="
go test -race ./pkg/orderbook ./internal/extension

echo
echo "Offline verification passed. No FCC, chain RPC, proxy, Docker, or live FTSO endpoint was required."
