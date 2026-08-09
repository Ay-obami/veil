/**
 * solvencyProof.ts — generates a ZK solvency proof entirely in the
 * browser, using snarkjs against the real circuit artifacts committed at
 * public/zk/ (copied from circuits/artifacts/ — see that directory's
 * README for how they were produced and BUILD_NOTES.md for the full
 * pipeline history).
 *
 * This never sends collateral, nonce, or any private witness data to any
 * server — proof generation happens locally; only the resulting proof and
 * its public signals ever leave the browser.
 */

import * as snarkjs from "snarkjs";
import type { SolvencyProof } from "./orderbook";

// Served from frontend/public/zk/ — see that directory; Vite serves
// public/ contents at the site root, so these paths resolve at runtime
// regardless of dev vs. production build.
const WASM_URL = "/zk/solvency.wasm";
const ZKEY_URL = "/zk/solvency_final.zkey";

/**
 * Circuit input field names — must match solvency.circom's signal names
 * exactly (collateral, threshold, orderId, nonce, commitment). "orderId"
 * is the client-chosen anti-replay nonce, not an actual order ID — see
 * PlaceOrderReq's doc comment in orderbook.ts for why.
 */
export interface SolvencyCircuitInput {
  collateral: string | number;
  threshold: string | number;
  orderId: string | number;
  nonce: string | number;
  commitment: string | number;
  // Index signature so this satisfies snarkjs's CircuitSignals type
  // (`[signal: string]: SignalValueType`) — the named fields above are
  // the actual documented contract; this only exists to satisfy
  // fullProve()'s parameter type.
  [signal: string]: string | number;
}

export interface GeneratedSolvencyProof {
  proof: SolvencyProof;
  publicSignals: string[];
}

/**
 * Generates a random anti-replay value for the circuit's `orderId` input.
 * Must be unique per proof — the backend rejects a reused value (see
 * internal/extension's usedSolvencyNonces). Kept well within the field
 * size (circuit uses 64-bit range checks — see solvency.circom note 5) by
 * drawing from a 53-bit-safe JS integer range, which is comfortably within
 * that bound while staying safe to round-trip through JSON as a number if
 * ever needed.
 */
export function randomOrderNonce(): string {
  const arr = new Uint32Array(2);
  crypto.getRandomValues(arr);
  // Combine into a value comfortably under 2^53 (Number.MAX_SAFE_INTEGER)
  // and well under the circuit's 2^64 range check.
  const value = (BigInt(arr[0]) << 20n) | BigInt(arr[1] & 0xfffff);
  return value.toString();
}

/**
 * Generates a real Groth16 proof for the solvency circuit. This is the
 * slow, CPU-bound step (witness calculation + proving) — expect it to
 * take anywhere from tens of milliseconds to a few seconds depending on
 * the device; callers should show a loading state, not assume this
 * resolves instantly.
 *
 * Throws if the inputs don't satisfy the circuit's constraints (e.g.
 * collateral < threshold, or commitment doesn't match
 * Poseidon(collateral, nonce)) — snarkjs surfaces this as a witness
 * calculation error. Callers should show a clear "you don't have enough
 * balance to prove solvency for this order" message rather than a raw
 * exception, since that's overwhelmingly the likely real-world cause.
 */
export async function generateSolvencyProof(
  input: SolvencyCircuitInput
): Promise<GeneratedSolvencyProof> {
  const { proof, publicSignals } = await snarkjs.groth16.fullProve(
    input,
    WASM_URL,
    ZKEY_URL
  );
  return { proof: proof as SolvencyProof, publicSignals };
}
