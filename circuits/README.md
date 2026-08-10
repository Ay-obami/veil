# Veil — Solvency Circuit (ZK layer)

Not present in the fce-orderbook base — this is the layer that makes Veil different
from a straight fork. See /BUILD_NOTES.md at repo root for the full picture.

## What it proves

`solvency.circom` proves two things about a private `collateral` value,
without revealing it:
1. `collateral >= threshold`
2. `Poseidon(collateral, nonce) == commitment` — i.e. `collateral` is the
   real value a commitment (see `pkg/solvency` at the repo root) was
   issued for, not an arbitrary made-up number.

Full design rationale, unit conventions, and a real (source-verified, not
assumed) security analysis of the underlying circomlib primitives are
documented as comments directly in `solvency.circom` — including one real
bug that was found and fixed via actual testing (an incomplete range
check), not just reasoned about in advance. Read those comments before
using this circuit.

## Status: compiled, tested, and a real proof generated and verified end-to-end

This is no longer "written but unverified." In this session:

- **circom itself was obtained and run for real** — a prebuilt binary from
  `github.com/iden3/circom`'s GitHub releases (v2.2.3), after building from
  source failed on an old system Rust toolchain (see BUILD_NOTES.md for
  that history).
- **The circuit compiled successfully**: 436 non-linear + 281 linear
  constraints, 3 public inputs (`threshold`, `orderId`, `commitment`), 1
  public output (`valid`), 2 private inputs (`collateral`, `nonce`) — small,
  as expected for a single comparator + one Poseidon(2) call.
- **All 6 tests pass** against the real compiler (independently confirmed
  twice — once locally by the project owner, once again in this session).
- **A full local Groth16 trusted setup ceremony was run** — fresh Powers of
  Tau (bn128, 2^12), single-contributor phase 2 (documented as a hackathon-
  scope limitation, not production-grade — see `contracts/SolvencyVerifier.sol`'s
  header comment).
- **A real proof was generated and verified**: witness → proof → `snarkjs
  groth16 verify` → `OK!`. The public signal ordering
  (`[valid, threshold, orderId, commitment]`) was confirmed **empirically**
  from the actual generated `public.json`, not just asserted from the
  circuit's declaration order.
- **Soundness was checked, not just the happy path**: a tampered
  `public.json` (changed `orderId`) was confirmed to correctly **fail**
  verification.
- **`contracts/SolvencyVerifier.sol` now exists** — a real, generated
  Groth16 verifier, exported from this exact ceremony. See its header
  comment for provenance, the public-input ordering, and the
  single-contributor ceremony caveat.
- **The runtime artifacts a prover actually needs are committed** at
  `circuits/artifacts/` (`solvency.wasm`, `solvency_final.zkey`,
  `verification_key.json`) — see `circuits/artifacts/README.md`, including
  a complete real example proof someone can verify without regenerating
  anything.

## Status update: the items below are now closed — kept for history, see BUILD_NOTES.md

The three gaps this section originally listed are all closed now,
via Go-side verification rather than an on-chain Solidity call — worth
knowing since it changes where to look, not just whether it's done:

- ~~Nothing on-chain calls `SolvencyVerifier.verifyProof(...)`~~ —
  correct as far as it goes (still true, and likely to stay true: nothing
  in `contracts/InstructionSender.sol` calls it), but verification doesn't
  need to happen on-chain in this architecture. `pkg/solvency/verify.go`
  verifies proofs directly in Go (via `go-rapidsnark`, confirmed working
  against this circuit's real artifacts), and `internal/extension`'s
  `PLACE_ORDER` handler calls it before accepting an order. The Solidity
  verifier contract exists and is real, but the actual enforcement path
  is Go, not Solidity — see BUILD_NOTES.md's PLACE_ORDER integration
  entries for the full history.
- ~~The `orderId` binding is still just a public input, not enforced~~ —
  closed. It's repurposed as a client-chosen anti-replay nonce (order IDs
  don't exist until after an order is accepted, so they can't be known
  when a proof is generated — see `internal/extension/solvency.go`'s
  comments), tracked and single-use-enforced atomically with order
  registration.
- ~~Depends on the TEE-issued commitment being checked against its
  signature~~ — closed, via a different mechanism than originally assumed:
  since commitment issuance and proof verification happen in the same TEE
  process, an in-memory provenance registry (`issuedCommitmentRecord`)
  serves the same purpose without needing a signature round-trip. The TEE
  signature itself still exists, for a genuinely external verifier that
  would need it — just not for this.

Two things this circuit's own scope still doesn't cover, unrelated to the
three above: **single-contributor ceremony** (fine for a hackathon
submission, not production-grade — stated plainly, not implied away), and
the **threshold vs. actual-order-size check**, which lives entirely on
the Go side (`verifySolvencyProofForOrder`) rather than in the circuit
itself, since it depends on order economics the circuit has no way to
know about.

## Regenerating everything from scratch

```bash
cd circuits
npm install
npm test              # 6/6 should pass
npm run build:all      # compile -> ptau ceremony -> zkey -> export
npm run publish:artifacts   # copies wasm/zkey/vkey into artifacts/
```

`npm run build:all` writes the exported verifier to `build/SolvencyVerifier.sol`,
**not** directly to `../contracts/` — publishing an updated verifier there
is a deliberate manual step (copy it over, re-apply the contract rename and
provenance header — see that file's own note for exactly why this is
manual, not automatic).

You'll need `circom` on your `PATH` — grab a prebuilt binary from
https://github.com/iden3/circom/releases (e.g.
`circom-linux-amd64` for Linux) or build from source with a current Rust
toolchain.
