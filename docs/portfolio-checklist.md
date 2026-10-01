# Veil Portfolio Checklist

Status: **READY FOR REVIEW — bounded offline case study**

This checklist separates what can be reproduced from this repository snapshot
without external infrastructure from the larger historical FCC deployment.

## Reproduce the verified path

Requirements: Go 1.25.1 or newer.

```bash
./scripts/offline-demo.sh
```

The script fails on the first failed command and runs:

```bash
go test ./pkg/orderbook ./pkg/solvency ./internal/extension
go test -race ./pkg/orderbook ./internal/extension
```

Fresh 2026-10-01 verification on Go 1.25.1: both groups passed.
A separate JSON test run of `./internal/extension` recorded 27 test passes,
0 skips, and 0 failures.

## What this demonstrates

- price-time-priority matching and the extension's order/balance state
  regressions run without chain or FCC infrastructure;
- the FTSO price-band hook is tested, including its deliberate fail-open
  behavior when no usable band is available;
- solvency-proof verification uses the committed proof artifacts under
  `circuits/artifacts/` and is exercised by the Go tests;
- balance restart behavior is tested: optional snapshots preserve deposited
  balances and migrate stale held amounts back to available on load;
- race detection covers the matching engine and extension state;
- upstream provenance is explicit: Veil began as a fork of
  `flare-foundation/fce-orderbook`.

## Safe claims

- Veil adds FTSO-aware matching and a ZK-solvency layer to an attributed FCC
  orderbook base.
- FTSO enforcement is optional and currently fail-open on oracle
  unavailability; it is not a fail-closed market-safety guarantee.
- Balance persistence is optional through `BALANCES_PATH`; without it,
  balances are process-memory state.
- The focused offline verification above passes on the current source.

## Do not claim

- Do not claim a fresh live FCC end-to-end run from this portfolio pass.
- Do not claim a fresh live Coston2 FTSO call from this portfolio pass.
- Do not claim all user balances are always confined to memory: optional
  balance snapshots write them to an operator-controlled local path.
- Do not claim `go build ./...` or `go test ./...` is currently a
  clean-clone gate.
- Do not claim the tracked deployment CLI is presently reproducible from this
  Git tree.

## Current source gap

`cmd/veil/main.go` imports `veil/internal/deploy`, but that package is
absent from the current repository snapshot. The deployment architecture is
documented in `docs/deployment.md`, and historical work used it, but the
source itself is not recoverable from this checkout.

An earlier `.gitignore` rule used unanchored `deploy/`, which also matches
`internal/deploy/`. The rule is now anchored to `/deploy/` so a restored
source package cannot be silently ignored again.

No replacement stubs were added: restoring the original deployment engine is
parked rather than fabricating behavior that cannot be verified.

## Parked / infrastructure-dependent work

- Restore the original `internal/deploy` package before treating the
  state-driven `veil deploy ...` CLI as supported again.
- Re-run the full module build/tests after restoration.
- Re-run frontend install/typecheck/build when frontend presentation becomes
  part of the active portfolio scope.
- Live FCC registration, proxy/indexer operation, Coston2 transactions,
  withdrawal delivery, and stress/soak runs require external infrastructure
  and are not part of this bounded verification gate.

## Evidence map

- `scripts/offline-demo.sh` — reproducible no-network verification entrypoint.
- `pkg/orderbook/orderbook.go` and `price_oracle_test.go` — FTSO-band
  policy and failure behavior.
- `pkg/solvency/` and `internal/extension/solvency*.go` — proof verifier
  and balance-bound commitment flow.
- `pkg/balance/persist.go` and `internal/extension/bugs_test.go` — optional
  atomic balance snapshots and restart regression coverage.
- `BUILD_NOTES.md` — development history and reused-vs-new attribution.
- `docs/deployment.md` — historical deployment-engine design, now clearly
  marked as parked until source restoration.
