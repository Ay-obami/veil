pragma circom 2.1.6;

include "circomlib/circuits/comparators.circom";
include "circomlib/circuits/bitify.circom";
include "circomlib/circuits/poseidon.circom";

// Solvency proves that a private `collateral` value is greater than or
// equal to a public `threshold`, without revealing `collateral` itself.
//
// Design notes (read before wiring this into the settlement contract):
//
// 1. SECURITY PATTERN: this circuit does NOT rely on the caller checking a
//    boolean output. `valid === 1` is a hard constraint — if collateral <
//    threshold, no valid witness exists and proof generation fails
//    outright. A verified proof, by itself, already means the check
//    passed. `valid` is exposed as an output purely for local debugging
//    (e.g. printing it during witness generation); the Solidity verifier
//    does not need to separately assert it equals 1. This avoids an entire
//    bug class where a caller checks the wrong public input, or forgets to
//    check it, and accepts an invalid proof.
//
// 2. UNITS: `collateral` and `threshold` must be supplied in the same
//    fixed-point tick units the matching engine already uses
//    (pricePrecision = 1e6, see pkg/oracle/ftso.go and
//    internal/extension/handlers.go) — NOT raw 18-decimal token wei
//    amounts. This is a deliberate scope decision, not an oversight: at
//    raw wei precision (up to ~2^96+ for realistic token supplies),
//    GreaterEqThan needs a much larger bit width, which roughly multiplies
//    circuit size and proving time. Working in the same 1e6-tick units the
//    rest of the system already uses keeps this a genuinely single-purpose,
//    cheap circuit — appropriate for a hackathon MVP. A production version
//    would need either a wider bit width or an explicit scaling step
//    (with its own overflow/precision-loss analysis) to work in raw wei.
//    `bits=64` comfortably covers any realistic tick-scaled amount without
//    overflow inside the field.
//
// 3. BINDING: `orderId` is a public input that is intentionally NOT used
//    in any constraint. Its only job is to let the caller (the settlement
//    contract or off-chain relayer) confirm the proof was generated for
//    the specific order being settled, not replayed from a different one.
//    That check happens OUTSIDE this circuit — the contract must compare
//    the `orderId` public input it receives against the order it's
//    actually processing. Forgetting that check would let a valid
//    solvency proof for order A be replayed against order B.
//
// 4. BALANCE BINDING: earlier versions of this circuit proved a claim
//    about an arbitrary private witness with no tie to any real balance —
//    a prover could supply any `collateral` value they liked. This is now
//    closed at the circuit level via a Poseidon commitment: the circuit
//    takes a public `commitment` input and a private `nonce`, and proves
//    `Poseidon(collateral, nonce) == commitment` in addition to the
//    threshold check. This only means something if `commitment` is
//    genuinely traceable back to the user's real funds — see note 6 below
//    for how that's actually done (TEE-issued commitments, checked
//    against an in-memory provenance record — closed, not a design
//    sketch anymore).
//
// 5. RANGE SAFETY — CORRECTED AFTER A REAL TEST FAILURE, not just reasoned
//    about in advance. The original version of this circuit claimed
//    GreaterEqThan(64) alone would make an out-of-range `collateral`
//    (>= 2^64) unsatisfiable, on the theory that its internal Num2Bits(65)
//    call range-checks the inputs. That claim was WRONG, and a local
//    `npm test` run caught it directly: GreaterEqThan computes
//    `diff = threshold + 2^64 - (collateral + 1)` and range-checks that
//    *combined difference*, not `collateral` in isolation. For
//    collateral = 2^64 and threshold = 1,000,000, diff = 999,999, which
//    fits fine — so the proof went through. On reflection this isn't
//    actually a security hole in the sense of "false things get proven":
//    2^64 >= 1,000,000 is arithmetically true, so the circuit answered a
//    true statement. But it does mean `collateral` could carry a huge,
//    out-of-range value while still producing a valid proof — which is
//    exactly the kind of case a range check should independently rule
//    out, regardless of whether this specific instance was "safe."
//    Fix: explicit Num2Bits(bits) calls directly on `collateral` and
//    `threshold` below force each one, independently, to actually
//    decompose into `bits` bits — Num2Bits's `lc1 === in` constraint
//    (see node_modules/circomlib/circuits/bitify.circom) makes this
//    unsatisfiable for any value >= 2^bits, with no dependence on what
//    the other input happens to be.
//
// 6. UPDATE — closed, via a different design than originally sketched
//    below. The commitment is NOT written on-chain at deposit time.
//    Instead, the TEE extension issues commitments on demand
//    (GET_SOLVENCY_COMMITMENT, see internal/extension/solvency.go),
//    computed directly from its own authoritative balance state
//    (pkg/balance — the same source of truth used for deposits,
//    withdrawals, and matching), and tracks what it issued in memory
//    (issuedCommitmentRecord). PLACE_ORDER checks a submitted proof's
//    commitment against that record — real user, real token, not
//    expired — before trusting it. This works because issuance and
//    verification happen in the same trusted process; it would NOT be
//    sufficient if a different, non-issuing party needed to verify a
//    commitment independently (e.g. an on-chain contract) — for that,
//    the TEE also signs an attestation over each commitment
//    (PackAttestationMessage in pkg/solvency/commitment.go), which
//    exists and works but isn't checked by anything yet, since nothing
//    external needs it yet. See BUILD_NOTES.md for the full history,
//    including why an on-chain deposit-time commitment write (the
//    original plan sketched in an earlier version of this note) was
//    reconsidered as unnecessary infrastructure for what this system
//    actually needed.
//
//    Original note, kept for context on what was initially planned:
//    "For `commitment` to mean anything, `InstructionSender.sol`'s
//    deposit flow (or a dedicated commitment-registry contract) needs
//    to store Poseidon(collateral, nonce) at deposit time, keyed to the
//    depositor's address." — superseded by the above.
template Solvency(bits) {
    signal input collateral; // private
    signal input threshold;  // public
    signal input orderId;    // public (binding only — see note 3 above)
    signal input nonce;      // private — chosen by the user, never revealed
    signal input commitment; // public — must equal Poseidon(collateral, nonce)

    signal output valid;

    // Independent range checks — see note 5. Without these, only the
    // *combined* comparison below is constrained, which is not the same
    // guarantee as bounding each input on its own.
    component collateralRange = Num2Bits(bits);
    collateralRange.in <== collateral;
    component thresholdRange = Num2Bits(bits);
    thresholdRange.in <== threshold;

    component gte = GreaterEqThan(bits);
    gte.in[0] <== collateral;
    gte.in[1] <== threshold;

    // Balance-binding check — see note 4.
    component hasher = Poseidon(2);
    hasher.inputs[0] <== collateral;
    hasher.inputs[1] <== nonce;
    hasher.out === commitment;

    valid <== gte.out;
    valid === 1;
}

// Public input order in the generated verifier's calldata is:
// [valid (output, always public), threshold, orderId, commitment]
// — circom places a template's output signals first, followed by the
// signals listed in {public [...]}, in the order listed. Confirm this
// against the actual generated verifier's comment header once compiled —
// don't assume this ordering is authoritative until it's been run through
// `circom --r1cs --wasm --sym` and inspected, since this hasn't been
// compiled in this sandbox (see circuits/README.md's verification status).
component main {public [threshold, orderId, commitment]} = Solvency(64);
