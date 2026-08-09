// Package types contains request/response types for the orderbook extension.
package types

import (
	"veil/pkg/balance"
	"veil/pkg/orderbook"
	"veil/pkg/solvency"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// --- Deposit (on-chain instruction) ---

type DepositRequest struct {
	Token  common.Address `json:"token"`
	Amount uint64         `json:"amount"`
}

type DepositResponse struct {
	Token     common.Address `json:"token"`
	Amount    uint64         `json:"amount"`
	Available uint64         `json:"available"`
}

// --- Withdraw (on-chain instruction) ---

type WithdrawRequest struct {
	Token   common.Address `json:"token"`
	Amount  uint64         `json:"amount"`
	Address common.Address `json:"address"`
}

type WithdrawResponse struct {
	Token        common.Address `json:"token"`
	Amount       uint64         `json:"amount"`
	To           common.Address `json:"to"`
	WithdrawalID common.Hash    `json:"withdrawalId"`
	Signature    hexutil.Bytes  `json:"signature"`
	Available    uint64         `json:"available"`
}

// --- Place Order (direct instruction) ---

type PlaceOrderRequest struct {
	Sender   string              `json:"sender"`
	Pair     string              `json:"pair"`
	Side     orderbook.Side      `json:"side"`
	Type     orderbook.OrderType `json:"type"`
	Price    uint64              `json:"price"`
	Quantity uint64              `json:"quantity"`

	// SolvencyProof is optional. New for Veil — not in the base
	// fce-orderbook. When present, both fields are required together and
	// the order is only accepted if the proof verifies (see
	// internal/extension/solvency.go's handling in processPlaceOrder).
	// SolvencyPublicSignals[pkg/solvency.PublicSignalOrderIDIdx] doubles as
	// a client-chosen anti-replay nonce — see that file's comments for why
	// it can't be the server-assigned order ID (order IDs don't exist yet
	// at the time a proof has to be generated).
	SolvencyProof         *solvency.Proof `json:"solvencyProof,omitempty"`
	SolvencyPublicSignals []string        `json:"solvencyPublicSignals,omitempty"`
}

type PlaceOrderResponse struct {
	OrderID   string            `json:"orderId"`
	Status    string            `json:"status"` // "filled", "partial", "resting"
	Matches   []orderbook.Match `json:"matches,omitempty"`
	Remaining uint64            `json:"remaining"`
}

// --- Cancel Order (direct instruction) ---

type CancelOrderRequest struct {
	Sender  string `json:"sender"`
	OrderID string `json:"orderId"`
}

type CancelOrderResponse struct {
	OrderID   string `json:"orderId"`
	Pair      string `json:"pair"`
	Side      string `json:"side"`
	Remaining uint64 `json:"remaining"`
}

// --- Get My State (direct instruction) ---

type GetMyStateRequest struct {
	Sender string `json:"sender"`
}

type GetMyStateResponse struct {
	Balances   map[common.Address]balance.TokenBalance `json:"balances"`
	OpenOrders []orderbook.Order                       `json:"openOrders"`
	Matches    []orderbook.Match                       `json:"matches"`
}

// --- Get Book State (direct instruction) ---
// Public orderbook depth + (optional) recent matches scoped to a single pair.
// Pair: when set, response includes the most recent matches for that pair.
// MatchLimit: cap on returned matches; default DefaultBookMatchLimit, max ring capacity.

type GetBookStateRequest struct {
	Sender     string `json:"sender,omitempty"`
	Pair       string `json:"pair,omitempty"`
	MatchLimit int    `json:"matchLimit,omitempty"`
}

// --- Get Candles (direct instruction) ---

type GetCandlesRequest struct {
	Sender    string `json:"sender,omitempty"`
	Pair      string `json:"pair"`
	Timeframe string `json:"timeframe"`
	Limit     int    `json:"limit,omitempty"`
}

type GetCandlesResponse struct {
	Pair      string             `json:"pair"`
	Timeframe string             `json:"timeframe"`
	Candles   []orderbook.Candle `json:"candles"`
}

// --- Get Solvency Commitment (direct instruction) ---
// Lets a user request a TEE-attested Poseidon commitment over their own
// real balance for a token, for use as the `commitment`/`nonce` witness in
// circuits/solvency.circom off-chain. See pkg/solvency for the commitment
// math and attestation message format, and BUILD_NOTES.md for the current
// integration status (the on-chain verification half is not yet wired).

type GetSolvencyCommitmentRequest struct {
	Sender string         `json:"sender"`
	Token  common.Address `json:"token"`
}

type GetSolvencyCommitmentResponse struct {
	Token      common.Address `json:"token"`
	Balance    uint64         `json:"balance"`    // the real balance the commitment was computed over — informational, the user already knows their own balance
	Nonce      string         `json:"nonce"`      // decimal string (fits in a JS/circuit-friendly big int) — PRIVATE, keep off-chain, needed as a circuit witness
	Commitment string         `json:"commitment"` // decimal string — Poseidon(balance, nonce), safe to make public
	IssuedAt   int64          `json:"issuedAt"`
	Signature  hexutil.Bytes  `json:"signature"` // TEE signature over PackAttestationMessage(user, token, commitment, issuedAt) — see pkg/solvency
}

// --- Export History (direct instruction) ---

type ExportHistoryRequest struct {
	Sender     string `json:"sender"`
	TargetUser string `json:"targetUser,omitempty"` // admin only
}

type ExportHistoryResponse struct {
	User        string                                  `json:"user"`
	Balances    map[common.Address]balance.TokenBalance `json:"balances"`
	Orders      []orderbook.Order                       `json:"orders"`
	Matches     []orderbook.Match                       `json:"matches"`
	Deposits    []DepositRecord                         `json:"deposits"`
	Withdrawals []WithdrawalRecord                      `json:"withdrawals"`
}

type DepositRecord struct {
	Token     common.Address `json:"token"`
	Amount    uint64         `json:"amount"`
	Timestamp int64          `json:"timestamp"`
}

type WithdrawalRecord struct {
	Token     common.Address `json:"token"`
	Amount    uint64         `json:"amount"`
	Address   common.Address `json:"address"`
	Timestamp int64          `json:"timestamp"`
}

// --- State (returned by GET_BOOK_STATE) ---

type State struct {
	Pairs      map[string]PairState `json:"pairs"`
	MatchCount int                  `json:"matchCount"`
	Matches    []orderbook.Match    `json:"matches,omitempty"`
}

type PairState struct {
	Bids []orderbook.PriceLevel `json:"bids"`
	Asks []orderbook.PriceLevel `json:"asks"`
}

// --- DO NOT MODIFY below this line. ---

// StateResponse is the envelope returned by GET /state.
type StateResponse struct {
	StateVersion common.Hash `json:"stateVersion"`
	State        State       `json:"state"`
}
