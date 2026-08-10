/**
 * orderbook.ts — typed wrappers for orderbook direct instructions.
 * Mirrors the request/response types in internal/extension and pkg/types.
 */

import { sendDirectAndPoll, type DirectProgress } from "./teeClient";

// --- Request types ---

export interface PlaceOrderReq {
  sender: string;
  pair: string;
  side: "buy" | "sell";
  type: "limit" | "market";
  price: number;
  quantity: number;

  /**
   * Optional. Both fields must be present together — see
   * lib/solvencyProof.ts for how these are generated, and
   * internal/extension/solvency.go for how the backend verifies them.
   * When present, publicSignals[2] doubles as a client-chosen anti-replay
   * nonce (NOT the order ID — order IDs don't exist yet when a proof has
   * to be generated; see that file's comments for why).
   */
  solvencyProof?: SolvencyProof;
  solvencyPublicSignals?: string[];
}

export interface CancelOrderReq {
  sender: string;
  orderId: string;
}

export interface GetMyStateReq {
  sender: string;
}

// --- Solvency proof (Veil-specific — not in the base fce-orderbook) ---

export interface GetSolvencyCommitmentReq {
  sender: string;
  token: string;
}

/**
 * Mirrors pkg/types.GetSolvencyCommitmentResponse exactly. `nonce` is a
 * required ZK witness and must stay off-chain/in-memory only — never log
 * or persist it. See circuits/solvency.circom and
 * internal/extension/solvency.go for the full design.
 */
export interface GetSolvencyCommitmentResp {
  token: string;
  balance: number;
  nonce: string;
  commitment: string;
  issuedAt: number;
  signature: string;
}

/**
 * Mirrors pkg/solvency.Proof exactly (same field names snarkjs's
 * groth16.fullProve() itself returns, so a generated proof can be attached
 * to a PlaceOrderReq with no remapping — see lib/solvencyProof.ts).
 */
export interface SolvencyProof {
  pi_a: string[];
  pi_b: string[][];
  pi_c: string[];
  protocol: string;
  curve?: string;
}

export interface GetBookStateReq {
  sender?: string;
  pair?: string;
  matchLimit?: number;
}

export interface GetCandlesReq {
  sender?: string;
  pair: string;
  timeframe: string;
  limit?: number;
}

// --- Response types ---

export interface Match {
  price: number;
  quantity: number;
}

export interface PlaceOrderResp {
  orderId: string;
  status: "resting" | "filled" | "partial";
  remaining: number;
  matches: Match[];
}

export interface CancelOrderResp {
  orderId: string;
  remaining: number;
}

export interface TokenBalance {
  available: number;
  held: number;
}

export interface OpenOrder {
  id: string;
  pair: string;
  side: "buy" | "sell";
  price: number;
  remaining: number;
  timestamp?: number;
  /** True if this order was placed with a verified ZK solvency proof —
   * see lib/solvencyProof.ts. Only ever true for the caller's own orders;
   * GET_BOOK_STATE's public depth never reveals this for other users'
   * orders (see pkg/orderbook.OrderSide.Depth()'s doc comment — verified
   * orders are excluded from that response entirely, not merely flagged). */
  solvencyVerified?: boolean;
}

export interface GetMyStateResp {
  balances: Record<string, TokenBalance>;
  openOrders: OpenOrder[];
  matches?: BookMatch[];
}

export interface PriceLevel {
  price: number;
  quantity: number;
}

export interface PairState {
  bids: PriceLevel[];
  asks: PriceLevel[];
}

export interface BookMatch {
  buyOrderId: string;
  sellOrderId: string;
  buyOwner: string;
  sellOwner: string;
  pair: string;
  price: number;
  quantity: number;
  timestamp: number;
}

export interface BookStateResp {
  state: {
    pairs: Record<string, PairState>;
    matchCount: number;
    /** Newest-first, scoped to the requested pair. Empty when no pair was passed. */
    matches?: BookMatch[];
  };
}

export interface ServerCandle {
  openTime: number; // unix seconds
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  trades: number;
}

export interface CandlesResp {
  pair: string;
  timeframe: string;
  candles: ServerCandle[]; // oldest-first
}

// --- API wrappers ---

export function placeOrder(
  req: PlaceOrderReq,
  progress?: DirectProgress
): Promise<PlaceOrderResp> {
  return sendDirectAndPoll<PlaceOrderResp>("PLACE_ORDER", req, progress);
}

export function cancelOrder(
  req: CancelOrderReq,
  progress?: DirectProgress
): Promise<CancelOrderResp> {
  return sendDirectAndPoll<CancelOrderResp>("CANCEL_ORDER", req, progress);
}

export function getMyState(sender: string): Promise<GetMyStateResp> {
  return sendDirectAndPoll<GetMyStateResp>("GET_MY_STATE", { sender });
}

/**
 * Requests a TEE-attested Poseidon commitment over the caller's real
 * balance for `token`. Must be called fresh (within
 * internal/extension/solvency.go's SolvencyCommitmentTTL, 5 minutes) before
 * generating a proof that references the returned commitment — see
 * lib/solvencyProof.ts.
 */
export function getSolvencyCommitment(
  req: GetSolvencyCommitmentReq
): Promise<GetSolvencyCommitmentResp> {
  return sendDirectAndPoll<GetSolvencyCommitmentResp>("GET_SOLVENCY_COMMITMENT", req);
}

export function getBookState(req: GetBookStateReq = {}): Promise<BookStateResp> {
  return sendDirectAndPoll<BookStateResp>("GET_BOOK_STATE", req);
}

export function getCandles(req: GetCandlesReq): Promise<CandlesResp> {
  return sendDirectAndPoll<CandlesResp>("GET_CANDLES", req);
}
