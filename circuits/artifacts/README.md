# Circuit Artifacts

Real, working outputs from a completed local Groth16 setup for
`solvency.circom` — not placeholders. See `BUILD_NOTES.md` at the repo
root for the full pipeline history, including the exact commands used and
the verification performed.

## Contents

- `solvency.wasm` — the witness calculator. Needed by a prover (e.g. the
  frontend) to compute a witness from `(collateral, threshold, orderId,
  nonce, commitment)` inputs.
- `solvency_final.zkey` — the proving key, from a **single-contributor**
  local ceremony (see the caveat in `contracts/SolvencyVerifier.sol`'s
  header — this is a documented hackathon-scope limitation, not a
  production-grade multi-party ceremony). Needed alongside the wasm to
  generate a proof via `snarkjs groth16 prove`.
- `verification_key.json` — the public verification key, for off-chain
  verification (`snarkjs groth16 verify`) without needing the full zkey.

The deployed counterpart of this verification key is
`contracts/SolvencyVerifier.sol` — regenerated from the exact same
ceremony, so it accepts proofs made with these files.

## `example/`

A complete, real, working proof — not a hypothetical:

- `input.json` — the circuit inputs used (`collateral=1500000,
  threshold=1000000, orderId=42, nonce=42`, plus the matching commitment)
- `proof.json` / `public.json` — the resulting Groth16 proof and its
  public signals, generated with these exact artifacts
- `sample_calldata.txt` — the same proof formatted as ready-to-paste
  Solidity calldata for `SolvencyVerifier.verifyProof(...)`

This proof was independently verified against `verification_key.json` in
this session (`snarkjs groth16 verify` → `OK!`), and a tampered version of
`public.json` (with `orderId` changed) was confirmed to correctly **fail**
verification — confirming the soundness property actually holds, not just
that the happy path works.

## Regenerating from scratch

See the `circuits/package.json` scripts (`compile`, `ptau:*`, `zkey:*`,
`export:*`, `build:all`, `publish:artifacts`) — running `npm run build:all`
reproduces everything in this directory from `solvency.circom` alone.
**Publishing an updated `contracts/SolvencyVerifier.sol` after a rebuild is
a deliberate manual step, not automatic** — see the provenance note at the
top of that file for why.
