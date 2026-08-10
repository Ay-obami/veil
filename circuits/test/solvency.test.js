// Tests for solvency.circom, using circom_tester's actual API (verified
// against the installed node_modules/circom_tester README and source in
// this repo — not written from memory), plus circomlibjs for computing
// real Poseidon commitments off-chain, matching what a real prover/relayer
// would do before generating a witness.
//
// VERIFICATION HISTORY: the first local `npm test` run against real
// `circom` (not this sandbox — see BUILD_NOTES.md) caught a real bug: an
// out-of-range `collateral` value produced a valid proof because
// GreaterEqThan only range-checks the combined difference between its two
// inputs, not either one independently. Fixed with explicit Num2Bits calls
// — see solvency.circom notes 5 and the corresponding test below. This
// circuit has since grown a Poseidon commitment binding (notes 4 and 6);
// those tests are new and have NOT yet been run against real circom in
// this sandbox for the same environment reason as before (no working
// circom binary here). Run `npm test` locally to confirm.
const chai = require("chai");
const path = require("path");
const wasm_tester = require("circom_tester").wasm;
const { buildPoseidon } = require("circomlibjs");

const assert = chai.assert;

describe("Solvency circuit", function () {
  this.timeout(120000);

  let circuit;
  let poseidon;

  // commitment(collateral, nonce) computes Poseidon(collateral, nonce) the
  // same way solvency.circom's hasher component does, as a decimal string
  // suitable for a circuit input. This is what a real caller (frontend,
  // relayer, or the deposit flow once note 6 is implemented) would run
  // off-chain to produce the public `commitment` value.
  function commitment(collateral, nonce) {
    const hash = poseidon([collateral, nonce]);
    return poseidon.F.toObject(hash).toString();
  }

  before(async function () {
    poseidon = await buildPoseidon();
    circuit = await wasm_tester(path.join(__dirname, "..", "solvency.circom"), {
      include: path.join(__dirname, "..", "node_modules"),
    });
  });

  it("accepts collateral strictly greater than threshold, with a correct commitment", async function () {
    const nonce = 42;
    const collateral = 1_500_000; // 1.5 in 1e6-tick units
    const w = await circuit.calculateWitness({
      collateral,
      threshold: 1_000_000, // 1.0
      orderId: 42,
      nonce,
      commitment: commitment(collateral, nonce),
    });
    await circuit.checkConstraints(w);
    await circuit.assertOut(w, { valid: 1 });
  });

  it("accepts collateral exactly equal to threshold (boundary)", async function () {
    const nonce = 7;
    const collateral = 1_000_000;
    const w = await circuit.calculateWitness({
      collateral,
      threshold: 1_000_000,
      orderId: 42,
      nonce,
      commitment: commitment(collateral, nonce),
    });
    await circuit.checkConstraints(w);
    await circuit.assertOut(w, { valid: 1 });
  });

  it("rejects collateral below threshold — witness generation itself should fail", async function () {
    // This is the important negative case, per solvency.circom's design
    // note 1: an insufficiently-collateralized witness should be
    // UNSATISFIABLE, not merely produce valid=0. calculateWitness is
    // expected to throw here. If it doesn't throw, that's a real bug in
    // the circuit (the valid===1 constraint isn't doing its job) — this
    // test failing to see an exception should be treated as a circuit
    // defect, not a test-writing bug.
    const nonce = 1;
    const collateral = 500_000; // 0.5
    let threw = false;
    try {
      await circuit.calculateWitness({
        collateral,
        threshold: 1_000_000, // 1.0 — insufficient
        orderId: 42,
        nonce,
        commitment: commitment(collateral, nonce),
      });
    } catch (e) {
      threw = true;
    }
    assert.isTrue(
      threw,
      "expected calculateWitness to throw for collateral < threshold (constraint should be unsatisfiable), but it succeeded"
    );
  });

  it("rejects an out-of-range collateral value (>= 2^64)", async function () {
    // Exercises note 5 in solvency.circom: the explicit Num2Bits(64) range
    // check on `collateral` should make this unsatisfiable, independent
    // of both the valid===1 check and the commitment check. This is the
    // exact case that caught a real bug on the first local test run
    // before the note-5 fix — see the file header above.
    const nonce = 1;
    const collateral = (1n << 64n).toString(); // exactly 2^64, one past the max
    let threw = false;
    try {
      await circuit.calculateWitness({
        collateral,
        threshold: 1_000_000,
        orderId: 42,
        nonce,
        commitment: commitment(1n << 64n, nonce),
      });
    } catch (e) {
      threw = true;
    }
    assert.isTrue(
      threw,
      "expected calculateWitness to throw for collateral >= 2^64 (range constraint should be unsatisfiable), but it succeeded"
    );
  });

  it("rejects a commitment that doesn't match Poseidon(collateral, nonce)", async function () {
    // Exercises note 4/6: even if collateral genuinely satisfies the
    // threshold and is in range, a mismatched commitment must make the
    // witness unsatisfiable — otherwise a prover could claim any
    // collateral value regardless of what commitment is actually on file.
    let threw = false;
    try {
      await circuit.calculateWitness({
        collateral: 2_000_000,
        threshold: 1_000_000,
        orderId: 42,
        nonce: 42,
        commitment: commitment(2_000_000, 999), // wrong nonce -> wrong commitment
      });
    } catch (e) {
      threw = true;
    }
    assert.isTrue(
      threw,
      "expected calculateWitness to throw for a commitment that doesn't match Poseidon(collateral, nonce), but it succeeded"
    );
  });

  it("orderId is a free public input, not constrained by the circuit itself", async function () {
    // Confirms note 3: the circuit doesn't care what orderId is — binding
    // it to a specific order is the CALLER's job (see solvency.circom).
    // This test exists so that assumption is explicit and checked, not
    // just asserted in a comment.
    const nonce = 5;
    const collateral = 2_000_000;
    const c = commitment(collateral, nonce);
    const w1 = await circuit.calculateWitness({
      collateral,
      threshold: 1_000_000,
      orderId: 1,
      nonce,
      commitment: c,
    });
    const w2 = await circuit.calculateWitness({
      collateral,
      threshold: 1_000_000,
      orderId: 999999,
      nonce,
      commitment: c,
    });
    await circuit.checkConstraints(w1);
    await circuit.checkConstraints(w2);
    await circuit.assertOut(w1, { valid: 1 });
    await circuit.assertOut(w2, { valid: 1 });
  });
});
