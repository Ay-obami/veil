# Veil — Build Notes

## What this repo is

Veil is a confidential OTC settlement layer for FAssets, built for the Flare
Summer Signal hackathon (bounties: Interoperable Asset Products + Private Apps /
Flare Confidential Compute).

**This repo is forked from `flare-foundation/fce-orderbook`**, Flare's own
reference implementation of a confidential exchange on FCC. That's a deliberate,
disclosed choice — see the PRD and hackathon discussion for the reasoning. The
fork gives us a working TEE matching engine, vault contract, proxy, and frontend
on day one. It does **not** give us FAssets support, FTSO price integrity, or
privacy-preserving solvency proofs — those three are entirely new and are what
the submission needs to demonstrate.

**Disclosure requirement:** the hackathon submission must explicitly state this
fork relationship (see Section 7, "What's Reused vs. Newly Built," in the PRD).
Do not present the base matching engine/vault/frontend as original work.

## Confirmed during setup (this session)

- Base repo (`fce-orderbook`) has **zero** FAssets/FTSO/FXRP references anywhere
  in contracts, Go code, or config — confirmed by grep across the full tree.
- Coston2 already has FTSO feeds relevant to us live and deployed
  (see `config/coston2/deployed-addresses.json`): `FtsoV2`, `FtsoTestXrp`,
  `ChainlinkAdapterXrp`, `FdcHub`, plus the full TEE registry/facet set
  (`FlareTeeManager`, `MachineManagerFacet`, `InstructionsFacet`, etc.).
- Bundler/AA infra on Flare: Etherspot provides Skandha (bundler) + Arka
  (paymaster) + Prime SDK with a working Coston2 reference dapp
  (`github.com/taylorferran/etherspot-flare`). This is the path for the
  ERC-4337 order-submission layer — not building a bundler from scratch.
- `flare-foundation/flare-foundry-starter` exists as an official Foundry
  template with FTSO/FDC/FAssets example scripts — worth pulling FTSO
  read patterns from directly rather than writing the interface calls blind.

## Extension points

### 1. FTSO price-band check — implemented, partially verified

Done:
- `pkg/orderbook/orderbook.go`: added a `PriceOracle` interface
  (`PriceBand(pair) (low, high uint64, ok bool)`), an optional `oracle` field
  on `OrderBook`, and a `priceInBand` check wired into both `matchBuy` and
  `matchSell`. Behavior is **opt-in** (nil oracle = old behavior, unchanged)
  and **fail-open** (oracle unavailable = no restriction, not "reject
  everything") — the fail-open choice is deliberate so a temporary FTSO
  hiccup can't freeze trading; flag this trade-off explicitly in the
  submission writeup rather than presenting it as free of downsides.
- `pkg/orderbook/price_oracle_test.go`: 5 new tests (rejected-outside-band,
  accepted-inside-band, fail-open on unavailable oracle, unchanged behavior
  with no oracle, and a multi-level sweep that stops at the first
  out-of-band price rather than skipping it).
- `pkg/oracle/ftso.go`: a real `FTSOPriceOracle` implementing `PriceOracle`
  via `eth_call` against Coston2's FtsoV2 proxy (`getFeedById`), with a
  small in-process cache and a symmetric basis-point tolerance band.

**Verification status — read before trusting this:**
- `pkg/orderbook` (including the new oracle hook and all 5 new tests) was
  compiled and run in isolation in the sandbox this was built in:
  **26/26 tests pass**, including every pre-existing test unchanged. This
  part is solid.
- `pkg/oracle/ftso.go` could **not** be compiled in that sandbox —
  `go-ethereum` (both the repo's pinned v1.17.2 and latest) requires Go
  ≥1.24, and the sandbox only had Go 1.22 with no network path to fetch a
  newer toolchain. Only `gofmt` (syntax-level) checked cleanly. **Run
  `go build ./... && go test ./...` yourself before trusting this file** —
  the ABI/eth_call pattern was cross-checked against real, cloned code from
  `flare-foundation/flare-foundry-starter` (`FtsoExample.sol`,
  `FtsoV2Consumer.sol`), not memory, so it should be close, but it is
  genuinely unverified by compilation.
- Also unverified: whether Flare's FTSO actually publishes a feed literally
  named `"XRP/USD"` (vs. some other naming) — confirm at
  https://dev.flare.network/ftso/feeds before wiring `FTSOPriceOracle` into
  the extension's startup code with a real pair mapping.

Now done: `internal/extension/extension.go`'s `New()` constructs a shared
`oracle.FTSOPriceOracle` (opt-in via `FTSO_RPC_URL`) and calls
`SetPriceOracle` on every configured pair that has a non-empty `ftsoFeed` in
its pairs config. `config/coston2/pairs.json` now sets `ftsoFeed` for all
three existing pairs (`FLR/USD`, `BTC/USD`, `ETH/USD` — all three names
confirmed against `flare-foundry-starter`'s example constants, not guessed).
`config/coston/pairs.json` (non-2) was deliberately left unchanged — Coston2
is the target network per this file's "Open items," so it wasn't worth
guessing an unverified FtsoV2 address for a network we're not targeting.

**Update — confirmed working on a real machine (not just the sandbox):**
user ran `go build ./...` and `go test ./...` locally after two fixes:
(1) `tee-node` needed to be cloned as a sibling repo per `REPRODUCIBILITY.md`'s
documented layout (`../../tee-node` relative to this repo), and (2) `go mod
tidy` bumped `go-flare-common` to a newer pin to resolve a version-skew
compile error inside `tee-node` itself (unrelated to anything in this repo).
Result: **`go build ./...` succeeds, including `pkg/oracle` and
`cmd/docker`** — `pkg/oracle/ftso.go` is now confirmed to actually compile,
closing out the biggest open question from earlier. `pkg/orderbook`,
`internal/extension`, `pkg/balance`, `pkg/types` all pass their test suites.

Added `cmd/ftso-smoketest/main.go` — a small standalone binary that calls
`FTSOPriceOracle.PriceBand()` directly against a live RPC endpoint for a list
of feed names, without starting the full extension server. This is the tool
to answer the one thing still not verified: whether `getFeedById` returns
sane data for real, and whether `"XRP/USD"` actually exists as a feed name.
Not yet run against live Coston2 — that's the next step.

### 2. FXRP as a supported asset — add to `config/coston2/pairs.json`
   alongside the existing FLR/USDT, BTC/USDT, ETH/USDT pairs. Needs the FXRP
   token address on Coston2 (FAssets test deployment) — not yet looked up.
### 3. ZK solvency proof — not started

New, lives in `/circuits`. Integration point is before order acceptance: the
proxy/extension should verify a submitted proof (or the Solidity vault
should, depending on where we land) before the TEE locks funds for an order.
Not present in the base at all.

### 4. ERC-4337 order submission — not started

Wraps `InstructionSender.sol`'s `deposit`/`withdraw` calls and the
frontend's direct-action order submission behind a smart account +
Etherspot's Skandha bundler, so deposit/order flows can be gasless.

## Open items before deeper implementation

- FXRP token address on Coston2 (FAssets test deployment) — need to look up.
- Confirm whether FTSO price check belongs in the Go matching engine (per-match,
  inside the TEE) or as an on-chain check at withdrawal time — the former is
  tighter (rejects bad matches before they happen) and is the current plan.
- Confirm current FCC governance/availability status on Songbird vs Coston2
  before assuming Coston2-only is the safe target (this repo's docker-compose
  already supports both).

## Repo layout (inherited from fce-orderbook, unchanged)

```
contracts/        InstructionSender.sol (vault + entrypoint), TestToken.sol
internal/extension/  TEE extension logic
pkg/orderbook/     matching engine (order.go, orderbook.go, orderside.go)
pkg/balance/       per-user balance ledger
pkg/types/         shared types
frontend/          React/Vite/Tailwind trading UI
config/coston2/    pairs.json, deployed-addresses.json
circuits/          NEW — ZK solvency circuit (not yet implemented)
```

## Production cleanup + rename pass

Done in this pass, purely mechanical/structural — no matching-engine or
oracle logic touched:

- **Removed AI-tooling artifacts inherited from the base repo**: `.claude/`
  (Claude Code skills for the base scaffold's own rename/verify workflows),
  the entire `testing/` directory (three Claude Code agents + `CLAUDE.md`
  files running continuous fuzz/chaos/smoketesting on a GCP VM — Flare's
  own internal dev-ops for maintaining `fce-orderbook`, not part of the
  product), and `docs/manual-setup.md` (a "how to rename this scaffold"
  tutorial, no longer relevant post-rename). Also stripped the now-dead
  `.claude` entries from `.gitignore`.
- **Removed stray build artifacts**: two committed macOS ARM64 binaries
  (`tools/stress-test`, `tools/test-setup`, ~28MB combined — already
  gitignored, evidently committed by accident upstream) and a stale
  `results/audit.log` runtime log from an unrelated prior test session.
  Added a `results/*.log` gitignore rule to stop it recurring.
- **Module rename**: `extension-scaffold` → `veil` (root module) and
  `extension-scaffold/tools` → `veil/tools`, across all 58 Go files that
  referenced the old import path, plus both `go.mod` files,
  `docker-compose.coston.yaml`, `docker-compose.coston2.yaml`, and
  `REPRODUCIBILITY.md`. Verified zero stale references remain (`grep -rl
  "extension-scaffold"` returns nothing outside `.git`).
- **Contract/branding rename**, scoped carefully to avoid touching the
  *matching-engine* package (`pkg/orderbook` — a legitimate domain name,
  left untouched) while renaming the *generated-bindings* package and
  Solidity contract (which followed the base's own `Orderbook` placeholder
  naming from when Flare's team ran their own rename-scaffold process):
  - `contracts/InstructionSender.sol`: `OrderbookInstructionSender` →
    `VeilInstructionSender`
  - `tools/pkg/contracts/orderbook/` → `tools/pkg/contracts/veil/`
    (dir + `orderbook.go` → `veil.go`, `go:generate` directive updated)
  - All 7 consumers of that bindings package (`tools/integration/*_test.go`,
    `tools/cmd/{run-test,test-setup,test-withdraw}/main.go`,
    `tools/pkg/stress/sweep.go`, `tools/pkg/utils/instructions.go`) updated
    for the new import path and `veil.` qualifier
  - `scripts/generate-bindings.sh`: `CONTRACT_NAME`/`GO_PKG` updated
  - `frontend/src/abi/orderbookInstructionSender.ts` →
    `veilInstructionSender.ts`, plus its 4 consumers
    (`useDeposit.ts`, `useWithdraw.ts`, `lib/withdraw.ts`, `lib/deposit.ts`)
  - `frontend/package.json`: `orderbook-frontend` → `veil-frontend`
  - `docs/{extension-guide,architecture,flows/*}.md` updated to match —
    `docs/flows/orders.md`'s references to `pkg/orderbook` (the matching
    engine) were correctly left alone
  - `README.md` re-titled and re-framed around Veil, with the fork
    disclosure moved to the top rather than buried — technical content
    describing the (unchanged) matching-engine mechanics was kept as-is
- **Formatting**: the rename shortened several identifiers
  (`extension-scaffold` → `veil`), which shifted `gofmt`'s column alignment
  in a number of files unrelated to this session's actual logic changes.
  Ran `gofmt -w .` across the whole repo to fix; `gofmt -l .` now returns
  nothing (all files clean).

**Verification**: `pkg/orderbook`'s full test suite (26 tests, including the
5 oracle ones from earlier) re-run in isolation post-rename — still 26/26.
The broader rename across files that depend on `go-ethereum`/`tee-node`
could not be `go build`-verified in this sandbox for the same reason as
always (Go ≥1.24 needed) — **run `go build ./... && go test ./...` after
pulling this** to confirm the rename didn't break anything outside what
this sandbox can check. Given the mechanical, scoped nature of the sed
passes (verified zero stray references of every renamed identifier), risk
here should be low, but "should be low" isn't "verified," per the standing
principle in this file.

## ZK solvency circuit — written, dependency-verified, not compiled

`circuits/solvency.circom` now exists: proves `collateral >= threshold`
(both in the same 1e6-tick fixed-point units the matching engine and
`pkg/oracle` already use) via circomlib's `GreaterEqThan`, with a bare
`orderId` public input for the caller to bind the proof to a specific
order (unconstrained by the circuit itself — binding is the caller's
responsibility, not yet wired anywhere). Full design rationale — including
why this uses a constraint-only pattern instead of a checked boolean
output, and a real (source-verified) analysis of why an out-of-range or
insufficient `collateral` value makes the witness unsatisfiable rather than
just producing a false output — is documented as comments in the circuit
file itself; see `circuits/README.md` for the summary and setup steps.

**What's actually been verified in this sandbox** (stronger than the usual
"gofmt syntax-check only" caveat, worth noting): `npm install` succeeded,
confirming every dependency version in `circuits/package.json` genuinely
exists. More importantly, `GreaterEqThan`/`LessThan`/`Num2Bits`'s exact
behavior — including the range-safety property the circuit's security
argument depends on — was read directly from the real, installed
`circomlib` source, not assumed from training data. `test/solvency.test.js`
was written against `circom_tester`'s actual installed API (checked its
README and source directly) rather than a remembered interface.

**What has not been verified**: the circuit has never actually been
compiled. Attempting to build `circom` itself from source in this sandbox
hit the same class of wall as `pkg/oracle/ftso.go` earlier — a cascading
chain of transitive Rust dependencies requiring rustc ≥1.83, while this
sandbox only has 1.75 via `apt` with no network path to a newer toolchain.
Running `npx mocha test/` here fails cleanly at `circom --version: command
not found` — confirming the test harness and JS tooling work, but not the
circuit's actual compilability. **Run `cd circuits && npm install && npm
test` locally** (after installing `circom` — see `circuits/README.md` for
options) to get the first real compile-and-witness confirmation, the same
way the local build closed out `ftso.go`'s verification gap earlier.

**Update — first real local test run (not just gofmt/npm-install-level
checks) found and fixed a real bug**: 4/5 tests passed on the first local
`npm test` run against actual `circom`; the "rejects out-of-range
collateral" test failed. The circuit's original note 5 claimed
`GreaterEqThan(64)` alone would range-check `collateral`, which was wrong
— it only range-checks the *combined difference* between `collateral` and
`threshold`, not either input independently. `collateral = 2^64,
threshold = 1,000,000` produced a valid proof, because the underlying
arithmetic statement (`2^64 >= 1,000,000`) is genuinely true — not a false
proof going through, but still an out-of-range value slipping past a check
that was supposed to reject it. Fixed by adding explicit `Num2Bits(bits)`
calls directly on both `collateral` and `threshold` in the circuit, which
independently bound each one via `Num2Bits`'s own `lc1 === in` constraint.
The corrected circuit has **not yet been re-run against real `circom`** —
this sandbox still can't compile it (see above); re-run `npm test` locally
to confirm the fix actually closes the gap it found.

## Balance-binding — Poseidon commitment added at the circuit level

Decision: went with a Poseidon commitment over the alternative considered
(a TEE-signed balance snapshot verified in-circuit). Reasoning that led
there — the TEE's likely existing key is secp256k1/ECDSA, and verifying
ECDSA inside a circuit means bigint + elliptic-curve gadgets running to
hundreds of thousands to low millions of constraints, a materially
different scope of work than a hackathon timeline supports. A
circuit-friendly EdDSA/Baby Jubjub variant would've been a real middle
ground (thousands of constraints, closer to Poseidon) but needs a second,
purpose-built TEE keypair that doesn't exist yet. Poseidon was picked as
the scoped, achievable option, with the honest tradeoff that it's a static
commitment rather than a live TEE attestation — see solvency.circom note 6
for exactly what closing that residual gap would still require.

`solvency.circom` now takes `nonce` (private) and `commitment` (public),
and constrains `Poseidon(collateral, nonce) === commitment` in addition to
the threshold and range checks. `test/solvency.test.js` was extended with
a real Poseidon implementation (`circomlibjs`, not a hardcoded/fake hash)
to compute genuinely valid commitments for the positive test cases, plus a
new negative test confirming a mismatched commitment makes the witness
unsatisfiable. Total: 6 tests, none yet run against real `circom` in this
sandbox (same environment wall as before) — confirmed only that the JS
loads, `circomlibjs`'s Poseidon computation itself runs correctly
standalone, and `npx mocha` fails at the same expected
`circom --version: command not found` boundary as before, nothing earlier.

**Confirmed on a real machine: all 6/6 tests pass against actual `circom`
and `circom_tester`** — the circuit genuinely compiles, and every property
claimed in the comments (threshold enforcement, boundary case, range
safety on `collateral`, Poseidon commitment binding, `orderId` being
correctly unconstrained) is now verified by real proof-system execution,
not just source-reading and reasoning. This closes out the circuit-level
verification gap entirely — everything left below is genuinely new scope
(on-chain wiring), not unverified claims about the circuit itself.

**Not yet done, unchanged in kind from before**: no integration with
`contracts/InstructionSender.sol` or the settlement flow (the generated
`SolvencyVerifier.sol` doesn't exist yet, and even once it does, nothing
calls it or enforces the `orderId` binding). And now more specifically:
**nothing on-chain writes `Poseidon(collateral, nonce)` at deposit time** —
the circuit-level binding is real and tested (once compiled), but the
`commitment` public input currently has no anchor to actual deposited
funds. That's the next concrete gap, not a hypothetical one — either
`InstructionSender.sol`'s deposit handler or a small dedicated
commitment-registry contract needs to write this, keyed to the depositor,
before the ZK layer's guarantee is actually end-to-end trustworthy.

## FXRP token address — resolved dynamically, not hardcoded

Two candidate addresses were suggested for FXRP on Coston2
(`0x0b6A3645...` and `0xa3Bd00D6...`). Checked rather than picked between
them: an independent third-party source (unrelated to either suggestion)
explicitly documents `0xa3Bd00D6...` as a **deprecated legacy address**,
and Flare's own official docs are unambiguous that FXRP's address "should
always be retrieved dynamically" via the Flare Contract Registry —
hardcoding is explicitly called out as something to avoid, precisely
because these addresses can and do change.

Added `cmd/get-fxrp-address` — a standalone tool that resolves the real
address live, on-chain, in two steps: `FlareContractRegistry` (deployed at
the identical address on every Flare network —
`0xaD67FE66660Fb8dFE9d6b1b4240d8650e30F6019`, confirmed from
dev.flare.network directly) → `getContractAddressByName("AssetManagerFXRP")`
→ `AssetManager.fAsset()`. The ABI signatures used are sourced directly
from dev.flare.network's own guides (`fassets-mint`,
`fassets-asset-manager-address-contracts-registry`,
`flare-contracts-registry`, fetched and read directly this session), not
memory — but as with `pkg/oracle/ftso.go`, this hasn't been compiled here
(same Go≥1.24 sandbox wall) or run against live Coston2. **Run
`go run ./cmd/get-fxrp-address` locally** to get the actual current
address before adding an FXRP pair to `config/coston2/pairs.json` —
neither of the two originally-suggested addresses should be used without
this confirmation first, given one is independently documented as
deprecated and the other is unverified.

**Confirmed on a real machine**: `go run ./cmd/get-fxrp-address` against
live Coston2 resolved `AssetManagerFXRP` to `0xc1Ca88b937d0b528842F95d5731ffB586f4fbDFA`
and `fAsset()` to `0x0b6A3645c240605887a5532109323A3E12273dc7` — matching
the address originally suggested (not the one independently documented as
deprecated). Added as a new `FXRP/USDT` pair in
`config/coston2/pairs.json`, with `ftsoFeed: "XRP/USD"` (already confirmed
live and working via `cmd/ftso-smoketest` in an earlier session — see
above).

**Worth flagging honestly**: the pair's `quoteToken`
(`0x3635Cb375fb0fbdFad888874064d3D5e3D4aA585`) is reused from the base
repo's existing FLR/USDT, BTC/USDT, ETH/USDT pairs and does **not** appear
in `config/coston2/deployed-addresses.json` — it's almost certainly one of
the base repo's own `TestToken.sol` demo deployments, not a real stablecoin.
So this pair is a genuine mix: `baseToken` (FXRP) is real, live-resolved
FAssets infrastructure; `quoteToken` is a testnet mock. That's an
acceptable, common pattern for a testnet MVP (real base asset, mock quote
for settlement testing) but should be stated plainly in the submission
rather than implied to be end-to-end "real."

## On-chain integration, part 1: TEE-issued solvency commitments

Started closing the biggest gap flagged in the checklist ("nothing on-chain
writes Poseidon(collateral, nonce) at deposit time"). Design decision made
first: the vault contract (`InstructionSender.sol`) has **no on-chain
balance storage at all** — deposits go straight into the contract and real
balances live entirely inside the TEE (`pkg/balance`), with withdrawals
authorized by a TEE ECDSA signature the contract verifies via `ecrecover`
(see `_recoverSigner` / `executeWithdrawal`). Given that, a separate
on-chain "commitment registry" contract (the design floated earlier) would
have been redundant infrastructure — the TEE is already the trust anchor
for real balances and already signs attestations the same way. So instead:
the TEE now issues signed commitments directly, reusing the exact signing
path (`signWithTEE`) that withdrawal authorization already uses.

**New: `pkg/solvency/commitment.go`** — computes
`Poseidon(balance, nonce)` via `github.com/iden3/go-iden3-crypto`
(the canonical Go implementation from the same org that maintains
circom/circomlib), and packs a TEE-attestation message
(`user, token, commitment, issuedAt`) in the same `abi.encodePacked` +
keccak256 + EIP-191 style `packWithdrawalMessage` already uses, so the
same `_recoverSigner` pattern can verify it on-chain later without a new
verification code path.

**Real cross-check performed, not just claimed**: before writing any of
this, I confirmed `iden3/go-iden3-crypto`'s `poseidon.Hash([]*big.Int{1500000, 42})`
produces the **exact same field element**
(`12360375947920094242516870753702830388276488141210735978590714515247575931361`)
that `circomlibjs` computed for the identical inputs during the ZK circuit
work earlier this session. That match is now locked in as a regression
test (`TestNewCommitmentWithNonce_KnownVector`) — if a future dependency
bump ever broke Go/circuit Poseidon agreement, this test would catch it.

**New: `internal/extension/solvency.go`** — a new direct instruction,
`GET_SOLVENCY_COMMITMENT` (not in the base fce-orderbook), following the
exact conventions of the existing `GET_MY_STATE`/`EXPORT_HISTORY` handlers:
reads the caller's real balance from `e.balances` (the same source of
truth as everything else), computes a commitment over it, signs an
attestation via the existing `signWithTEE`, and returns
`(balance, nonce, commitment, issuedAt, signature)` to the caller. Wired
into the OPCommand registry (`pkg/types/register.go`), the config constant
list, and the routing switch in `extension.go`.

**Verification status**: `pkg/solvency`'s core logic (Poseidon computation,
message packing, error handling — 8 tests) was compiled and run in
isolation in the sandbox this was built in, **8/8 passing**, including the
known-vector regression test above. This required a sandbox-only stand-in
for `common.Address` (a local `[20]byte` type with `.Bytes()`/`HexToAddress`)
since `go-ethereum/common` itself hits the same Go≥1.24 wall as everything
else in this sandbox — the stand-in only matters for byte-length
compatibility, which it has, so this is real confidence in the logic, not
just a syntax check, but it is **not** the same as compiling against the
real `go-ethereum` types. The full wiring (`extension.go`, `types.go`,
`register.go`, `config.go`, `solvency.go` together) is gofmt-clean but
unbuilt in this sandbox. **Run `go mod tidy && go build ./... && go test ./...`
locally** — `go mod tidy` specifically needs to run to fetch
`github.com/iden3/go-iden3-crypto` and populate `go.sum`, since that
couldn't be done here without a real internet connection.

**Still not done — the actual on-chain half**: nothing yet calls
`_recoverSigner`-equivalent verification against a solvency commitment
attestation on-chain. The TEE can now issue a real, signed commitment; a
contract (or the settlement logic wherever proofs get checked) still needs
to (1) verify that signature before trusting `commitment` as an input to
`SolvencyVerifier.sol`'s proof check, and (2) enforce the `orderId`
binding from the ZK circuit against the order actually being settled.
`SolvencyVerifier.sol` itself still doesn't exist (needs a local
circom/snarkjs run — see `circuits/README.md`). Both remain the next
concrete pieces of work.

## Major unblock: circom obtained, full pipeline run for real, SolvencyVerifier.sol now exists

The "not yet done" line above (`SolvencyVerifier.sol` doesn't exist) is now
false. What changed: building `circom` from source kept failing in this
sandbox (cascading Rust dependency requirements — see earlier notes), but
a **prebuilt binary** turned out to be fetchable — `release-assets.githubusercontent.com`
and `github.com` are both in this sandbox's allowed domains, and circom
publishes Linux binaries as GitHub release assets
(`github.com/iden3/circom/releases/download/v2.2.3/circom-linux-amd64`).
That single download unblocked the entire rest of the ZK pipeline that had
been sitting on "reasoned about, not verified" for several sessions.

**What was actually run, for real, in this session** (not reasoning about
what should happen — actual commands, actual output):
- Compiled `solvency.circom` for real: 436 non-linear + 281 linear
  constraints, matching the small size predicted earlier.
- Ran the existing 6-test suite against the real compiler —
  **independently reconfirmed** 6/6 passing (matching what was reported
  from a real machine earlier, now also confirmed directly).
- Ran a complete local Groth16 trusted setup: fresh Powers of Tau (bn128,
  2^12) generated from scratch (not downloaded — avoided needing
  `storage.googleapis.com`, which isn't in the allowed domains, by
  generating a ceremony locally instead, which a circuit this small can do
  trivially), single-contributor phase-2 zkey ceremony.
- **Generated a real proof and verified it**: witness → `groth16 prove` →
  `groth16 verify` → `OK!`. Then deliberately broke it — tampered with
  `orderId` in the public signals — and confirmed verification correctly
  **fails**. This is real soundness confirmation, not just a happy-path
  demo.
- Exported the actual verifier contract and empirically confirmed the
  public-input ordering from the real generated `public.json`:
  `[valid, threshold, orderId, commitment]` — matching what the circuit's
  comments predicted, but now confirmed rather than assumed.
- Re-ran the entire pipeline a second time using only the `npm run`
  scripts (not manually-typed commands) to prove they're accurate — caught
  and fixed two real bugs in the process: `circom`'s `-o build` requires
  the output directory to already exist (doesn't auto-create it), and the
  original `export:verifier` script wrote directly to
  `../contracts/SolvencyVerifier.sol`, which **silently clobbered** the
  hand-annotated version the first time it was re-run. Fixed by having the
  script write to `build/` only, with publishing to `contracts/` now a
  deliberate manual step — worth knowing if this pipeline gets re-run
  again, since the failure mode (silently losing the annotation) is easy
  to miss.

**New/changed files**:
- `contracts/SolvencyVerifier.sol` — real, generated, working verifier.
  Header comment documents provenance, the empirically-confirmed public
  input ordering, and the single-contributor ceremony caveat.
- `circuits/artifacts/` — the three files an actual prover needs
  (`solvency.wasm`, `solvency_final.zkey`, `verification_key.json`),
  committed (not gitignored) since they're real runtime dependencies, not
  build scratch. `circuits/artifacts/example/` holds a complete, real,
  independently-verified example proof.
- `circuits/circuits/.gitignore` fixed — the original `*.zkey`/`*.ptau`
  global patterns would have accidentally hidden the new `artifacts/`
  files; narrowed to just `build/`.
- `circuits/package.json` — scripts rewritten to the exact real commands
  used (previous versions were reasonable-looking placeholders never
  actually run; two had real bugs, both caught by actually running them).

**What's still open, now more precisely scoped than before**: nothing
on-chain or in the Go extension actually calls
`SolvencyVerifier.verifyProof(...)` yet, and the `orderId` binding still
isn't enforced against a real order anywhere. Both are now blocked only on
integration work, not on missing artifacts — the cryptographic pieces
this depends on are real and confirmed working.

## On-chain integration, part 2: Go-side proof verification wired into PLACE_ORDER

Closed the two items the previous entry ended on. Design decision made
first, same reasoning as part 1: given orders/matching happen entirely
inside the TEE extension (not on-chain — the chain only ever sees
deposit/withdraw), verifying the ZK proof **inside the Go extension**
fits the existing architecture far better than deploying
`SolvencyVerifier.sol` and having a separate on-chain "settle with proof"
entrypoint that doesn't correspond to anything that currently happens
on-chain. The TEE is already the trust boundary for everything else in
this system (balances, matching, withdrawal authorization); checking a
proof in the same process is consistent with that, not a new trust
assumption.

**Found a real sequencing bug before writing any wiring code**: order IDs
are assigned server-side (`e.nextOrderID()`, inside `processPlaceOrder`),
but a proof has to be generated *before* submission — so a client can
never know the real `orderId` to embed in a proof ahead of time. Fixed by
repurposing the circuit's `orderId` public signal
(`pkg/solvency.PublicSignalOrderIDIdx`) as a **client-chosen anti-replay
nonce** instead — the server tracks consumed nonces
(`Extension.usedSolvencyNonces`) rather than requiring the signal to match
its own internal ID. This is documented at the field-declaration level in
`pkg/types/types.go` and in `internal/extension/solvency.go`'s comments,
not just here — anyone reading the code cold should hit the explanation.

**New: `pkg/solvency/verify.go`** — wraps
`github.com/iden3/go-rapidsnark/verifier`, whose `ProofData`/`ZKProof`
types happen to mirror snarkjs's `proof.json`/`public.json` output shape
almost exactly, so no custom parsing was needed. `PublicSignalsLen` and
the `PublicSignal*Idx` constants encode the empirically-confirmed ordering
from part 1 (`[valid, threshold, orderId, commitment]`) in one place
rather than as scattered magic numbers.

**Real verification performed, not just reasoning — twice**:
1. Standalone, before touching the extension: loaded the real committed
   `circuits/artifacts/example/proof.json` + `public.json` +
   `verification_key.json` and ran `verifier.VerifyGroth16` directly —
   confirmed valid, then confirmed a tampered public signal is correctly
   rejected. Same result both times as the `snarkjs`-side check from part
   1, now independently confirmed from Go.
2. As committed tests: `pkg/solvency/verify_test.go` (7 tests) loads the
   same real fixtures — no synthetic/mocked proof data anywhere in this
   package's tests. Ran the full `pkg/solvency` suite (commitment + verify,
   15 tests total) in an isolated module mirroring the real repo's
   directory layout (so the relative path to `circuits/artifacts` resolves
   correctly) — **15/15 passing**.

**Wired into `internal/extension`**:
- `Extension` gained `verificationKey []byte` and
  `usedSolvencyNonces map[string]time.Time`. The verification key loads at
  startup from `config.SolvencyVerificationKeyPath` (defaults to the real
  committed path — opt-OUT via `SOLVENCY_VK_PATH=""`, not opt-in, since
  unlike the FTSO oracle this is now genuine committed infrastructure, not
  something someone has to stand up separately). Fails open at startup
  (log + disable) on a load error, consistent with the FTSO oracle's
  philosophy.
- `PlaceOrderRequest` gained optional `SolvencyProof`/`SolvencyPublicSignals`
  fields. `processPlaceOrder` now: verifies the proof statelessly (before
  any locks, since it's pure computation) via
  `verifySolvencyProofForOrder`; inside the existing locked critical
  section, checks and consumes the anti-replay nonce atomically with order
  registration (matching this function's own documented lock policy);
  marks the resulting `Order.SolvencyVerified` accordingly. Attaching an
  invalid proof rejects the whole order — it is never silently treated as
  "no proof attached."
- `Order` (in `pkg/orderbook`) gained a `SolvencyVerified bool` field.
  Purely informational at the matching-engine level right now — it does
  **not** yet change matching behavior, visibility, or anything in
  `GET_BOOK_STATE`. Actually hiding verified orders' price/quantity from
  public book state (the real dark-pool value proposition from the
  original PRD) is a separate, NOT yet implemented feature — this only
  adds the data point that a future feature could act on.

**Bug caught by writing tests, before any human ran them**: the existing
`newTestExtension` test helper in `internal/extension/handlers_test.go`
didn't initialize `usedSolvencyNonces`, which would have caused a nil-map
write panic the first time any test exercised the verified-proof path.
Fixed as part of this change, not left for the next `go test` run to
discover the hard way.

**New: `internal/extension/solvency_test.go`** (6 tests, using the same
real fixture proof, not synthetic data) — valid proof accepted, tampered
proof rejected, verification-disabled rejected, nonce replay rejected,
mismatched proof/signals fields rejected, and a control case confirming
proof-free orders are completely unaffected by any of this.

**Verification status — the honest part**: `pkg/solvency` (both files,
15 tests) was fully compiled and run in this sandbox, real confidence, not
just review. `internal/extension`'s changes (the actual wiring) could
**not** be compiled here — tried harder than usual this time: found that
`golang-1.24-go` is installable via `apt` (unlike 1.22, which was all that
had been tried before), which seemed promising, but `go-flare-common`
itself (a transitive dependency, not just this repo's own `go.mod`)
requires Go ≥1.25.1 in its own `go.mod` — confirmed by temporarily
lowering this repo's `go` directive to test the theory, watching it fail
one level deeper, and then restoring the original file exactly (verified
via `git diff` showing only the intended dependency additions). So this
genuinely cannot be compiled without Go 1.25 specifically, not just "a
newer Go" — worth knowing if this comes up again. In lieu of compilation,
did a careful, complete manual re-read of every changed function
end-to-end (not spot-checks) before finalizing. **Run
`go mod tidy && go build ./... && go test ./...` locally** — this is the
one that actually closes the loop, same as every other `internal/extension`
change this session.

**What's still open**: the matching engine doesn't yet use
`Order.SolvencyVerified` for anything — no privacy/visibility feature
exists yet to hide a verified order's details, which is the actual
dark-pool value proposition. `GET_SOLVENCY_COMMITMENT`'s returned
signature (part 1) still isn't checked against
`PackAttestationMessage(...)` anywhere before a commitment is trusted —
right now `processPlaceOrder` trusts whatever `commitment` value is
embedded in a submitted proof's public signals without confirming a real
TEE attestation actually vouches for it matching a real balance. That's
the next real gap, not a hypothetical one: the ZK math is fully verified
end-to-end now, but the link from "this proof is valid" back to "this
commitment came from a real balance the TEE actually attested" isn't
closed yet.

## Closing the gap: commitment provenance now checked before trusting a proof

The gap named above is closed, though via a deliberately different
mechanism than the ECDSA-signature-verification path the earlier notes
assumed would be needed. Reasoning: `GET_SOLVENCY_COMMITMENT` (issuance)
and `PLACE_ORDER`'s proof check (verification) both run inside the same
extension process — the whole point of the TEE signature was to let a
*different* party trust an attestation it didn't itself witness. Within
one process, that's unnecessary machinery: an in-memory record, written at
issuance and read at verification, establishes the same fact ("this
commitment really came from this TEE's real balance state, for this user
and token, recently") without a signature round-trip. The TEE signature
itself (`PackAttestationMessage` + `signWithTEE`) still exists and is
still returned to callers — it remains the right mechanism for a
*different* verifier (e.g. an on-chain contract, or a separate service)
to trust a commitment without re-deriving it, which is exactly the
scenario the earlier notes were implicitly assuming. That case just isn't
what's needed for TEE-internal verification, and building the
signature-check path anyway would've been solving a problem this
architecture doesn't actually have yet.

**New**: `issuedCommitmentRecord` (in `internal/extension/solvency.go`) —
`User`, `Token`, `Balance`, `IssuedAt`, keyed by commitment. Written by
`processGetSolvencyCommitment` under a new `solvencyMu` (deliberately
separate from `e.mu`, since issuance shouldn't serialize with order
placement/matching). Read by `verifySolvencyProofForOrder`, which now
checks, after the Groth16 proof itself verifies: the commitment was
genuinely issued by this TEE (found in the map at all), for the exact
same user placing the order, for the token the order actually needs
(`pair.QuoteToken` for a buy / `pair.BaseToken` for a sell — computed
independently of `calculateHold` since that needs a fully-constructed
order with an ID that doesn't exist yet at this point), and within
`SolvencyCommitmentTTL` (5 minutes) of issuance.

**Deliberately NOT single-use**: unlike the nonce (which the proof itself
is keyed to and *is* single-use, preventing the same proof from backing
two orders), the same commitment can legitimately back multiple orders
within its TTL — e.g. a market maker placing several orders off one
balance snapshot. This is safe because the commitment registry is a
*provenance* check, not a *spending* check: real fund custody is still
enforced separately and unconditionally by `e.balances.Hold(...)`, using
the user's actual current balance, regardless of what any proof claims.
Documented explicitly on `issuedCommitmentRecord` so this isn't
mistaken for a security gap later.

**Reordering required**: `processPlaceOrder` now looks up `pairConfig`
*before* calling the solvency check (previously after), since determining
`expectedToken` needs it. Mechanical change, no behavior difference for
the proof-free path — confirmed by
`TestPlaceOrder_NoProofAttached_UnaffectedByFeature` still passing
unchanged.

**Tests**: the two existing success-path tests
(`ValidProofAccepted`, `NonceReplayRejected`) needed updating — they now
seed `e.issuedCommitments` with the real fixture's actual commitment value
(`circuits/artifacts/example/public.json`'s 4th signal) before placing an
order, since that commitment was never *actually* issued through
`GET_SOLVENCY_COMMITMENT` in a test context. Five new tests added:
unissued commitment rejected, wrong user rejected, wrong token rejected,
expired commitment rejected, and — the important positive case for the
not-single-use design — the same commitment successfully backing two
separate verification calls.

**Verification status**: same honest limitation as every other
`internal/extension` change this session — could not be compiled here
(`tee-node` isn't available in this sandbox at all, a harder blocker than
the go-ethereum Go-version wall that affects `pkg/solvency`). Reviewed the
full diff line-by-line, including re-tracing the lock ordering
(`solvencyMu` vs `e.mu`) for the deadlock-freedom argument the file's
existing lock-policy comment already makes. **Run `go build ./... &&
go test ./...` locally** — this is the one that actually confirms the
11 solvency-related tests in `internal/extension` (6 original + 5 new)
all pass together.

## The last named gap: verified orders now actually hidden from the public book

`Order.SolvencyVerified` existed since the PLACE_ORDER integration but did
nothing — no privacy/visibility feature used it. This closes that: the
actual dark-pool mechanism, previously just a flag with no consumer.

**Change is entirely in `pkg/orderbook/orderside.go`'s `Depth()`** — the
one function `processGetBookState` calls to build the public order book
view. Orders with `SolvencyVerified == true` are now excluded from the
aggregation. Two specific design choices, both documented directly on
`Depth()`:

- A price level containing **only** verified orders is omitted entirely,
  not shown with `Quantity: 0` — a zero-quantity level would still leak
  "a hidden order exists at exactly this price," undermining a real part
  of the privacy this is for.
- A level with a **mix** of verified and public orders shows only the
  public portion's aggregated quantity/count. The level's existence is
  already known from the visible order there; only the hidden order's
  contribution to the displayed size stays hidden. This is a deliberate,
  bounded leak (price-level existence, not exact size at that level) —
  the same shape of tradeoff real dark pools accept, stated as a choice,
  not discovered as a bug later.

**The property that actually matters, confirmed by test, not just
claimed**: hiding an order from `Depth()` does not hide it from matching.
`matchBuy`/`matchSell`/`fillFromQueue` walk `priceLevels` directly and
never call `Depth()` at all — verified and non-verified orders are
completely indistinguishable to the matching engine itself.
`TestDepth_VerifiedOrdersStillMatchNormally` places a fully-hidden sell,
confirms it produces zero ask levels in `Depth()`, then places a
compatible buy and confirms it fills against the hidden order exactly as
it would against a visible one.

**Verification status — the strongest in this session**: `pkg/orderbook`
has no external dependency beyond `emirpasic/gods` (no `go-ethereum`, no
`tee-node`), so unlike almost everything else built today, this was
**fully compiled and tested for real in this sandbox**, not just reviewed
by hand. Full suite: 30/30 passing (26 pre-existing + 4 new), including a
regression guard (`TestDepth_AllPublicUnaffected`) confirming the
zero-verified-orders case is byte-for-byte identical to the original
`Depth()` behavior.

**What this doesn't do**: a user's own orders remain fully visible to
themselves regardless of `SolvencyVerified` — `GET_MY_STATE` reads from a
separate per-user snapshot (`e.orders`), never from `Depth()`, so this
change couldn't have affected it even accidentally (confirmed by grepping
every call site of `.Depth()` in the repo — there is exactly one, in
`processGetBookState`). Also unaffected: `EvictExcessLevels` (resource
bounds, not a reporting path) still evicts based on real price levels
regardless of visibility.

**With this, every gap named across this whole build session — FTSO
price-band enforcement, the ZK circuit and its real generated verifier,
FXRP as a live-resolved asset, TEE-issued and provenance-checked solvency
commitments, and now actual dark-pool visibility — has a real,
implementation, not just a design note.** The honest remaining work is
verification (running the parts that couldn't be compiled in this
sandbox) and product polish (frontend integration, a demo, external
feedback), not missing functionality.

## A real gap found while starting frontend integration: threshold wasn't checked

Before writing any frontend code, working out what `threshold` value it
should send exposed that `verifySolvencyProofForOrder` never checked the
proof's `threshold` against what the order actually requires. Concretely:
a proof generated with `threshold=1` (trivially satisfiable by any nonzero
balance) would have passed every existing check — Groth16 validity,
commitment provenance, user match, token match, freshness — while being
attached to an order of any size. The "solvency" guarantee was real at the
cryptographic level but hollow at the product level: it proved something,
just not necessarily anything about *this* order.

**Fixed** in `verifySolvencyProofForOrder`: computes the order's actual
required amount the same way `calculateHold` does
(`quantity * price / pricePrecision` for a buy, `quantity` for a sell) and
rejects the proof if its `threshold` public signal is below that.
**Restricted to limit orders** — a market order's fill amount depends on
available liquidity at execution time, not something fixable in advance,
so there's no single value to check a pre-generated proof against; market
orders with a proof attached are now rejected outright rather than given
a check that couldn't mean anything.

Two tests added (`ThresholdTooLowRejected`, `MarketOrderRejected`); one
existing test (`SameCommitmentCanBackTwoOrders`) needed `Type`/`Side`/
`Price`/`Quantity` added — it previously left those at zero-value, which
would now fail the new check.

## Frontend integration: private orders, end to end

Wired the whole private-order flow into the existing React frontend —
requesting a commitment, generating a proof client-side, and attaching it
to an order — plus fixed a config-generation gap and the FXRP pair listing
along the way.

**FXRP pair listing fix**: `frontend/src/config/generated.ts` is a
*committed snapshot*, not live-generated in this checkout —
`scripts/sync-config.ts` reads a root-level `config/pairs.json` that
doesn't exist here (only the per-network `config/coston2/pairs.json` and
`config/coston/pairs.json` do; the root file is populated by whatever
deploy tooling points a dev environment at a specific network, and wasn't
present in this checkout). Editing the generated snapshot directly to add
`FXRP/USDT` was the correct fix given that — not fabricating the missing
`config/extension.env`/`config/test-tokens.env`/`config/pairs.json` files,
which would mean inventing deployment-specific values (contract address,
token addresses) this session doesn't actually have. Once a real
Coston2 dev environment populates those files, `npm run sync-config` will
pick up FXRP automatically (the per-network source file already has it).

**New: `frontend/src/lib/solvencyProof.ts`** — generates a real Groth16
proof entirely in the browser via `snarkjs.groth16.fullProve()`, against
the exact circuit artifacts already committed at `circuits/artifacts/`
(copied to `frontend/public/zk/` so Vite serves them as static assets —
duplication is deliberate, not accidental; `public/` needs its own copy,
symlinks don't survive git/cross-platform reliably). No private witness
data (collateral, nonce) is ever sent to any server — only the resulting
proof and public signals leave the browser.

**New: `frontend/src/lib/orderbook.ts` additions** — `SolvencyProof` and
`GetSolvencyCommitmentReq/Resp` types mirror the Go backend's JSON field
names exactly (`pi_a`/`pi_b`/`pi_c`/`protocol`), so a proof object from
`snarkjs` can be attached to a `PlaceOrderReq` with zero remapping.
`getSolvencyCommitment()` wraps the new `GET_SOLVENCY_COMMITMENT`
direct instruction using the same generic `sendDirectAndPoll` every other
instruction already uses.

**`usePlaceOrder.ts`**: a new optional `privateOrder` flag drives the
whole flow — request a commitment for the correct hold token (mirrors
`calculateHold`'s own quote-for-buy/base-for-sell logic), generate a proof
with `threshold` set to the order's actual required amount (matching the
backend check added above — sending anything else would just get
rejected), attach the result to the order. Rejects client-side up front
if `type !== "limit"`, mirroring the backend's market-order restriction
rather than letting the request round-trip just to fail.

**`OrderForm.tsx`**: a "PRIVATE ORDER" checkbox, disabled and
auto-unchecked for market orders, with inline copy explaining what it
actually does ("price and size stay off the public book"). Uses a
4-step progress tray (commitment → proof → submit → execute) instead of
the normal 2-step one when active, since proof generation is a real,
possibly multi-second, CPU-bound step users should see progress for.

**Verification status — the strongest frontend verification available**:
unlike the Go backend (blocked on `tee-node` not existing in this sandbox
at all), the frontend has no such blocker. Ran the real toolchain, for
real, in this session:
- `npm install` — succeeded, including the new `snarkjs` and
  `@types/snarkjs` dependencies.
- `npx tsc --noEmit` — **zero errors**, across the entire frontend, not
  just the new files. (One real type error was hit and fixed along the
  way: `snarkjs`'s community type definitions require an index signature
  on `fullProve`'s input type — added one to `SolvencyCircuitInput`.)
- `npm run build` — a full real Vite production build, **succeeded**,
  37.7s, confirmed the `solvency.wasm`/`solvency_final.zkey` static
  assets landed in `dist/zk/` exactly where `solvencyProof.ts` expects
  them at runtime.

**Not yet done**: no actual end-to-end run against a live extension —
this confirms the frontend *compiles and builds correctly*, not that a
real proof-generate-and-verify round trip works through a running
backend. That's the natural next verification step once the Go side is
also confirmed building (see the standing `go build ./... && go test
./...` request throughout this file).

## Closing the last "not yet done": a visible indicator for private orders

The gap named above is closed for `OpenOrders.tsx`. Turned out to need no
backend change at all — `GetMyStateResponse.OpenOrders` is typed as
`[]orderbook.Order` directly, and `Order.SolvencyVerified` (added earlier
this session) already had a `json:"solvencyVerified,omitempty"` tag, so it
was already flowing over the wire; the frontend's own `OpenOrder` type
just hadn't been told about it yet. Added the field there,
`useMyState.ts` needed no change (it passes `resp.openOrders` straight
through, no per-field mapping to update), and `OpenOrders.tsx` now shows a
🔒 next to the pair name for orders placed with a verified proof, with a
tooltip explaining what that means.

**Also fixed while there**: `OpenOrders.tsx`'s populated-table header was
missing a `TIME` column that the empty-state header (and every actual
row) already had — a pre-existing off-by-one between the two render
paths, unrelated to this session's work, just noticed and fixed in
passing since it was directly adjacent to what was being edited.

**Deliberately NOT touched: `MyFills.tsx` / `RecentTrades.tsx`.** Checked
first rather than assumed: `orderbook.Match` has no `SolvencyVerified`
field at all — only `Order` does. This is correct, not an oversight: once
a trade executes, the print becomes public trade history regardless of
whether either resting order was originally private, mirroring how real
dark pools/OTC desks still report executed trades to a consolidated tape
even though they don't show pre-trade interest. Nothing to indicate there
because there's deliberately nothing being hidden at that stage.

**Verification**: same real toolchain as the rest of the frontend work —
`npx tsc --noEmit` (zero errors) and a full `npm run build` (succeeded),
both re-run after this change specifically, not just before it.

## First real local `go test` run against `internal/extension` — 2 real bugs found, both in tests

This is the run every prior session's "run this locally" request was
building toward. Result: `pkg/oracle` (no test files — expected),
`pkg/orderbook`, `pkg/solvency`, `pkg/types`, `pkg/balance` all green.
`internal/extension` failed — genuinely useful, since both failures were
real test-design bugs the sandbox's inability to compile this package
could never have caught, not backend logic bugs:

1. **`NonceReplayRejected`'s first (expected-success) placement failed**
   with `insufficient balance: amount must be greater than zero`. Cause:
   the test used `Price: 1`. `calculateHold`'s buy-side formula is
   `quantity * price / pricePrecision` (integer division,
   `pricePrecision = 1_000_000`) — at `Price: 1, Quantity: 1`, that's
   `1/1_000_000 = 0`, and `balance.Hold` correctly refuses a zero-amount
   hold. Not a bug in `calculateHold` or `Hold` — a test that picked a
   `Price` far too small for the fixed-point scale everything else in the
   system uses. Fixed: `Price: 1_000_000`.

2. **`ThresholdTooLowRejected` didn't reject** — the order succeeded when
   it should have been rejected for an insufficient threshold. Cause: the
   test's own comment claimed "Price*Quantity here is 2,000,000... more
   than the proof covers," but never accounted for the `/ pricePrecision`
   step — at `Price: 2_000_000, Quantity: 1`, `requiredAmount` is
   actually `2`, not `2,000,000`, nowhere near exceeding the fixture's
   `threshold=1,000,000`. The test's premise was simply arithmetically
   wrong. Fixed by scaling `Quantity` instead of `Price`
   (`Quantity: 2_000_000, Price: 1_000_000` → `requiredAmount = 2,000,000`,
   genuinely above the fixture's threshold).

Both are exactly the class of bug that written-but-never-executed test
code accumulates — plausible-looking arithmetic that's subtly wrong,
invisible until something actually runs it. Worth being direct about
this rather than glossing over it: several tests in this file were
written across multiple sessions without ever being run for real until
now, and two of them were wrong. The fix in both cases was to the test
data, not to `verifySolvencyProofForOrder`, `calculateHold`, or any other
production code — but that's only established by having actually run it,
not by how the fix turned out.

**Confirmed on a real machine**: after a false-alarm detour (a `../../tee-node
does not exist` failure that turned out to be a stale terminal state, not
a real regression — `ls`/`pwd` confirmed the sibling layout was correct
all along), a genuinely fresh `go clean -testcache && go test ./...` run
came back **fully green**: every package in the module builds and every
test passes, including `internal/extension` — 26 + several new
solvency-specific tests — which is the one that actually mattered here,
since that's where both fixes lived. This closes out the backend
verification loop that's been open since the very first "run this
locally" request many sessions ago.

## Frontend: confirmed on a real machine too

`cd frontend && npm install && npx tsc --noEmit && npm run build` — run
independently on the project owner's machine, not just in the sandbox
that originally verified this. `tsc --noEmit` produced no output (clean),
and `npm run build` completed in 23.17s with no errors — matching exactly
what the sandbox found earlier. The build log's only noise is pre-existing
and unrelated to anything built this session: Rollup's inability to
relocate a few `/*#__PURE__*/` annotations inside third-party wallet-
connector packages (`ox`, used transitively by `@base-org/account`,
`@coinbase/wallet-sdk`, `@walletconnect/utils`, `@reown/appkit*`) — cosmetic,
does not affect correctness, and predates any change made here. The
chunk-size warning (several bundles over 500kB) is likewise pre-existing,
driven by the wallet-connector ecosystem (RainbowKit/wagmi pull in
support for many wallets), not by `snarkjs` or anything added for the ZK
proof flow.

**Both the backend and frontend are now confirmed building and passing
on a real machine, independently of this session's sandbox — not just
reasoned through.**

## History: does shipping this on GitHub require the `tee-node` sibling folder? (superseded below — see "Side quest" further down)

Investigated first, decided second — findings below, decision and what
changed as a result at the bottom of this section.

- `go.mod`'s `replace github.com/flare-foundation/tee-node => ../../tee-node`
  is why a fresh GitHub clone won't build without also having `tee-node`
  checked out as a sibling directory two levels up. A local-path
  `replace` always takes precedence over the real published module,
  unconditionally — Go will never fall back to fetching it from GitHub
  once a local replace is declared, even though `tee-node` has real tags
  and a normal public repo.
- **Checked, not assumed**: `tee-node`'s own `go.mod` has zero replace
  directives of its own — it's a fully normal, self-contained, versioned
  module. Nothing about its structure forces the local-sibling pattern.
- **Checked, not assumed**: `go get github.com/flare-foundation/tee-node@v0.0.24`
  (no local replace, just a normal versioned dependency) resolved
  successfully in a sandbox test — it only stopped afterward on the same
  Go-version wall (`requires go >= 1.25.1`) that's blocked full
  compilation in this sandbox all session, not on anything about needing
  a local checkout.
- **Conclusion**: the sibling-folder requirement is a choice in this
  repo's `go.mod` (likely inherited from Flare's own team co-developing
  `tee-node` and this extension together), not a structural necessity.
  Pinning a real tagged version instead should work on a machine with a
  real Go ≥1.25 toolchain — but this hasn't been tried end-to-end, and
  given how much earlier debugging went into getting `tee-node` and
  `go-flare-common` versions to align, it carries real (if bounded) risk
  of reintroducing a subtle break.

**Three options considered**:
1. Drop the `replace`, pin a tagged `tee-node` version — cleanest for
   anyone cloning from GitHub, unverified end-to-end.
2. Leave the local-replace pattern, document the sibling-clone
   requirement prominently (this is what upstream `fce-orderbook`'s own
   `REPRODUCIBILITY.md` already expects and documents) — lowest risk,
   known-working.
3. `go mod vendor` — bakes a full dependency copy into the repo, fully
   self-contained, heaviest option.

**Decision made at the time: option 2 — leave `go.mod` as-is, document the
sibling-clone requirement prominently.** Reasoning given: it doesn't
drift from the standard already established by upstream `fce-orderbook`,
and given how much earlier debugging went into getting
`tee-node`/`go-flare-common` versions to align on the local-checkout path
specifically, that's a known-working configuration not worth risking for
a cosmetic convenience — correctly weighted against option 1's unverified
upside, at the time.

**Later revisited**: after this decision was documented, it was tried
anyway as a bounded, explicitly-abandonable side quest — see "Side quest:
actually trying option 1" further down. It worked, first try, and was
merged. This section is kept as-written for the historical reasoning
(sound at the time, given what was known then), not corrected after the
fact — the point of a build log is to show how decisions actually
evolved, not to make it look like the right answer was obvious from the
start.

**What actually changed as a result**:
- `go.mod` — untouched, exactly as before.
- `README.md` — new "Prerequisites" section added, right before "Try It
  Locally", spelling out the exact sibling-clone layout and commands
  (matching what was actually confirmed to work on a real machine this
  session), plus what the `../../tee-node does not exist` error means if
  someone hits it anyway.
- `scripts/full-setup.sh` — its own prerequisites comment block was
  missing this entirely (only mentioned Hardhat/indexer/Redis) despite
  `--local` mode needing a working `go build` just like the bare
  commands do. Added a pointer to the README section rather than
  duplicating the full instructions in two places.
- `Dockerfile` — already documented this correctly (its own top comment
  already states the build-context requirement); left unchanged,
  confirmed rather than assumed correct.

## Side quest: actually trying option 1 (pinned version, no local replace)

Decision was option 2 (previous section) — this is a bounded, explicitly
low-stakes experiment on top of that, not a reversal. Agreed upfront: try
a few times, abandon cleanly if it doesn't work, no risk to the confirmed
option-2 state either way.

**Safety net set up first**: tagged the current commit
(`known-working-option2`) before touching anything, and did the
experiment on a separate branch (`experiment-pinned-tee-node`). If this
doesn't pan out, reverting is `git checkout master` (or resetting to the
tag) — the confirmed-working state is untouched and always one command
away.

**What changed**:
- Root `go.mod`: `github.com/flare-foundation/tee-node v0.0.0` →
  `v0.0.24` (a real, published tag — confirmed to exist and resolve as a
  normal module in the earlier investigation), and the
  `replace ... => ../../tee-node` line removed entirely.
- `tools/go.mod`: same fix — `tee-node`'s pseudo-versioned require bumped
  to `v0.0.24`, and its own (separate, one-directory-deeper) `replace
  ... => ../../../tee-node` line removed from the replace block.

**Found along the way, deliberately left alone**: `tools/go.mod`'s
replace block has a *third* entry, `github.com/flare-foundation/tee-proxy
=> ../../../tee-proxy` — a completely separate sibling-repo dependency
we hadn't previously investigated, specific to the `tools/` submodule
(deployment/testing tooling, not part of the core `go build ./... && go
test ./...` path this session has been validating). Fixing tee-node
doesn't touch this. Explicitly out of scope for this side quest — flagged
here so it isn't mistaken for an oversight if `tools/` is ever built
directly. `veil => ../` in that same block is unrelated and correctly
untouched — that's the normal way a multi-module monorepo references its
own root module locally, not a sibling-repo workaround.

**What could be verified in the sandbox, and what couldn't**: confirmed
(in the earlier investigation, and unchanged here) that `tee-node@v0.0.24`
downloads successfully as a real module with no local replace, and that
`tee-node`'s own `go.mod` has zero replace directives of its own. Could
**not** verify a full `go build`/`go test` here — same Go-version wall as
every other Go change this session (this repo's `go.mod` requires Go
≥1.25.1; the sandbox has 1.22 with no path to a newer toolchain).
Additionally, and specific to this change: **`go.sum` will need new real
checksum entries for `tee-node@v0.0.24`** now that it's a genuine
versioned dependency instead of a locally-replaced one (replaced modules
bypass checksum verification entirely, which is part of why this wasn't
an issue before). Computing real checksums needs the actual Go module
checksum database, unreachable from this sandbox — `go mod tidy` on a
real machine is required and expected to change `go.sum` meaningfully,
not just tidy up.

**Status: CONFIRMED WORKING, first try, on a real machine.** `go mod
tidy && go build ./... && go test ./...` from a fresh extraction (in a
separate `veil-experiment/` folder, deliberately not overwriting the
known-working checkout while testing) came back fully green — every
package builds, every test passes, `internal/extension` included,
identical result to the local-replace version. No `go.sum` issues, no
version conflicts. Merged into `master` (`git merge
experiment-pinned-tee-node --no-ff`); the `known-working-option2` tag and
the original decision writeup above are kept as history, not deleted —
this session went with option 1 in the end, but the option-2 reasoning
that led there first was sound at the time and remains a legitimate
fallback if this ever regresses.

**One more thing found while wrapping this up, not yet acted on**: the
`Dockerfile` still explicitly `COPY`s `tee-node/` from a `tee/`-parent
build context and its own top comment still says the build context "must
be `tee/`" for the (now-removed) replace directive to resolve. That
requirement is gone for `go build`/`go test`, but the Dockerfile wasn't
touched as part of this — Docker builds weren't tested (no Docker
available in the environment that ran this change), and simplifying it
(dropping the `COPY tee-node/` step, letting `go mod download` inside the
container fetch it normally like every other real dependency) is a
reasonable next step but a different, unverified change from the one
just confirmed. Flagged, not done.

## Making it production-ready: fixing what the tee-node fix exposed

"Continue and make everything test and production ready" — this pass
went broader than just the `tee-node` pin, and found real, previously
undiscovered bugs along the way, not just cleanup.

**`go.sum` caveat, stated plainly**: this session's sandbox could not
regenerate a fully correct `go.sum` for the `tee-node@v0.0.24` subtree —
tried, and hit the same Go-version wall as every other Go change this
session, just one level deeper (resolving the actual package graph, not
just the top-level module version, requires the real `go >=1.25.1`
toolchain `tee-node` itself declares). The `go.sum` shipped in this zip
may be incomplete for the new dependency. This has been true and handled
the same way all session — `go mod tidy` is a required first step, not
optional — but it's worth being explicit here specifically because a
stale `go.sum` fails loudly and immediately on `go build`, unlike some
other kinds of staleness. **If you already ran `go mod tidy`
successfully on your own machine after the last update, do not let this
zip's `go.sum` silently overwrite your already-correct local one** —
re-run `go mod tidy` again after extracting regardless; it's always safe
and idempotent.

**Two real, previously undiscovered bugs found and fixed, unrelated to
the tee-node pin itself**: `Dockerfile`, `Dockerfile.staging`, and
`docker-compose.yaml` all still referenced `extension-examples/orderbook/`
— the path from *before* the extension-scaffold → veil rename many
sessions ago. That rename pass (thorough at the time for Go imports,
Solidity, and the frontend) never touched these three files, since they
weren't part of what `go build`/`go test` covers. They'd have silently
built a stale/wrong path if anyone had actually run a Docker build any
time since the rename — caught now, not before, because this was the
first time anything in this session actually looked closely at the
Docker build path specifically.

**Fixed, once the `tee-node` copy was already being removed anyway**:
- `Dockerfile` / `Dockerfile.staging`: `COPY tee-node/` and
  `COPY extension-examples/orderbook/` (two separate module copies) →
  one `COPY . .` (single repo, build context is just this repo's root
  now). `WORKDIR /build/extension-examples/orderbook` → `WORKDIR /build`.
  Final stage's `pairs.json` copy path fixed to match. `NETWORK` build
  arg's default bumped from the legacy `coston` to `coston2` — our actual
  target network throughout this whole build, per every other decision
  this session.
- `docker-compose.yaml`: `context: ../..` → `context: .`,
  `dockerfile: extension-examples/orderbook/Dockerfile` → `dockerfile:
  Dockerfile`, `${CHAIN:-coston}` → `${CHAIN:-coston2}`.
- New `.dockerignore` — needed now that build context is the full repo
  root; excludes `node_modules`, build scratch, `.git`, matching the
  various `.gitignore` files already in the repo. `circuits/artifacts/`
  (the real committed proving artifacts) is deliberately NOT excluded.
- `REPRODUCIBILITY.md`: "Build context" section and the remote-image
  verification clone/build commands both rewritten — no more `tee/`
  parent directory, no more `tee-node` clone step, build command's
  trailing context argument changed from `../..` to `.`.
- `docs/deployment-steps.md`: the claim "the extension's Dockerfiles
  consume both repos from `../../tee-node/`" was true when written, false
  now — fixed to say so precisely. Left `tee-proxy`'s sibling-clone
  requirement alone deliberately — that's a genuinely separate service
  this session never touched or investigated, not something to silently
  imply is also fixed just because it appeared in the same doc section.
- `docker/gcp-coston2/README.md` and
  `docker/gcp-coston2/gcp-extension-tee/README.md`: two more stale
  `extension-examples/orderbook/...` path references, in GCP deployment
  instructions — found only by a repo-wide grep sweep, not by anything
  that would have surfaced them otherwise (nothing in `go build`/`go
  test`/`npm run build` touches these files at all).

**Also found, flagged, deliberately not acted on**:
`Dockerfile.staging`'s own header comment claimed go.mod "currently
requires go 1.25.8," needing a base-image bump to match. This repo's
`go.mod` actually says `go 1.25.1` — exactly matching the pinned base
image already. If that claim was ever accurate for this repo, it isn't
now; if it was copied from upstream without adjustment, the whole
GOTOOLCHAIN=auto workaround this file exists for may not be needed here
at all. Corrected the comment to state this plainly rather than either
deleting the file (a bigger, unverified call — `docs/state-backup-staging.md`
describes a specific validation purpose beyond just the Go version
question, not fully investigated) or leaving a claim that doesn't match
reality.

**Verification status for this pass**: everything here is Dockerfile/
compose/doc changes — none of it touches Go or TypeScript source, so
`gofmt -l .` (clean) and the fact that nothing in `pkg/`, `internal/`, or
`frontend/src/` was touched are the closest things to "verified" this
pass has for the Go/frontend side. All three `docker-compose*.yaml` files
were parsed with Python's `yaml.safe_load` and confirmed syntactically
valid — genuine verification, if a shallow one (confirms the YAML parses,
not that Compose would actually accept every field or that the images
build). **No Docker build was actually run** — no Docker available in
this sandbox, same limitation noted when the `tee-node` Dockerfile
question first came up. The Dockerfile/path fixes themselves are
straightforward greps-and-replaces against an already-understood
structure, not independently compiled or built. **A real `docker build`
or `docker compose up` run would be the natural next confirmation,
whenever Docker is available to run one.**

## Found while writing the testing/deployment guide: another stale pre-rename reference, this one live-dangerous

`frontend/vercel.json`'s rewrite destinations pointed to
`tee-proxy-coston2-orderbook.flare.rocks` — the old pre-rename hostname,
almost certainly the base `fce-orderbook` repo's own live demo proxy, not
anything this project controls. Worse than the path bugs found in the
earlier production-readiness pass: those would have failed loudly (a
build error, a 404). This one would have **succeeded silently** — anyone
deploying this frontend to Vercel as-is would have gotten a working-looking
app that quietly proxied every order/balance/state request through
someone else's live infrastructure instead of their own deployment.
Replaced with an explicit `REPLACE-WITH-YOUR-DEPLOYED-PROXY-URL`
placeholder so it fails obviously (visibly broken requests) instead of
succeeding against the wrong backend. Documented as a required manual
step in the new deployment guide.
