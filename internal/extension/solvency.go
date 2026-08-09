package extension

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"veil/pkg/orderbook"
	"veil/pkg/solvency"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// SolvencyCommitmentTTL bounds how long an issued commitment can back a
// proof before it's treated as stale. A balance can change (withdrawal,
// another order) after a commitment is issued but before a proof using it
// is submitted — this is the freshness window that limits how out-of-date
// an accepted proof's underlying balance claim can be. Not a security
// property of the ZK proof itself (that only proves internal consistency
// with a commitment); this is what keeps the commitment meaningful.
const SolvencyCommitmentTTL = 5 * time.Minute

// issuedCommitmentRecord is what e.issuedCommitments stores per commitment
// — enough to check, at proof-verification time, that a commitment a
// proof references was genuinely issued by this TEE, for the right user
// and token, and hasn't gone stale. This is the piece that closes the gap
// noted in BUILD_NOTES.md: previously, processPlaceOrder trusted whatever
// `commitment` value a submitted proof's public signals contained, with
// nothing confirming it corresponded to a real GET_SOLVENCY_COMMITMENT
// issuance at all.
//
// Deliberately NOT single-use / consumed on a successful check: the same
// commitment can legitimately back proofs for more than one order within
// its TTL (e.g. a market maker placing several orders off one balance
// snapshot). That's safe because this registry is not what enforces real
// fund custody — e.balances.Hold(...) (called separately, using the
// user's actual current balance, not anything from the proof) is what
// prevents overcommitment. This registry only answers "did the TEE really
// attest this commitment, for this user and token, recently" — a
// provenance check, not a spending check.
type issuedCommitmentRecord struct {
	User     string
	Token    common.Address
	Balance  uint64
	IssuedAt int64
}

// processGetSolvencyCommitment handles GET_SOLVENCY_COMMITMENT direct
// instructions. This is new for Veil — not present in the base
// fce-orderbook fork. It closes (the off-chain half of) the balance-
// binding gap documented in circuits/solvency.circom notes 4 and 6:
// rather than trusting an arbitrary `collateral` witness, a user gets a
// commitment computed by the TEE from its own authoritative balance state
// (pkg/balance — the same source of truth used for deposits, withdrawals,
// and order matching), signed the same way withdrawal authorizations are
// (see processWithdraw / signWithTEE in withdraw.go).
//
// The response's Nonce field must be kept private by the caller — it's a
// required witness for the ZK proof (see circuits/solvency.circom) and
// revealing it doesn't strictly break the commitment's hiding property on
// its own, but there's no reason to publish it, so callers shouldn't.
//
// The issued commitment is recorded in e.issuedCommitments so a later
// PLACE_ORDER referencing it can be checked against a real issuance — see
// verifySolvencyProofForOrder and issuedCommitmentRecord's doc comment.
// The returned Signature is still not verified anywhere on-chain — that
// remains open (see BUILD_NOTES.md) — but within this process, the
// in-memory record now serves the same "was this really attested"
// purpose without needing a signature round-trip, since issuance and
// verification happen in the same trusted process.
func (e *Extension) processGetSolvencyCommitment(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.GetSolvencyCommitmentRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
	}

	user := strings.ToLower(req.Sender)
	if user == "" {
		return buildResult(action, df, nil, 0, fmt.Errorf("sender address is required"))
	}

	// Read the real, authoritative balance — the same balance manager used
	// for deposits, withdrawals, and order-matching holds. This is the
	// value the commitment binds to; nothing here is user-supplied.
	bal := e.balances.Get(user, req.Token)

	issuedAt := time.Now().Unix()
	c, err := solvency.NewCommitment(bal.Available, issuedAt)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("computing commitment: %w", err))
	}

	userAddr := common.HexToAddress(req.Sender)
	attestationMsg, err := solvency.PackAttestationMessage(userAddr, req.Token, c.Commitment, issuedAt)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("packing attestation message: %w", err))
	}

	sig, err := e.signWithTEE(attestationMsg)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("signing commitment attestation: %w", err))
	}

	e.solvencyMu.Lock()
	e.issuedCommitments[c.Commitment.String()] = issuedCommitmentRecord{
		User:     user,
		Token:    req.Token,
		Balance:  bal.Available,
		IssuedAt: issuedAt,
	}
	e.solvencyMu.Unlock()

	logger.Infof("solvency commitment issued for %s (token %s): %x", user, req.Token.Hex(), sig[:8])

	resp := types.GetSolvencyCommitmentResponse{
		Token:      req.Token,
		Balance:    bal.Available,
		Nonce:      c.Nonce.String(),
		Commitment: c.Commitment.String(),
		IssuedAt:   issuedAt,
		Signature:  sig,
	}
	data, _ := json.Marshal(resp)
	return buildResult(action, df, data, 1, nil)
}

// verifySolvencyProofForOrder checks a PLACE_ORDER request's optional ZK
// solvency proof, if present. Three checks, in order:
//
//  1. The stateless cryptographic check (Groth16 verification itself).
//  2. The commitment-provenance check: the proof's `commitment` public
//     signal must correspond to a real, unexpired GET_SOLVENCY_COMMITMENT
//     issuance for this exact user and expectedToken (see
//     issuedCommitmentRecord's doc comment for why this isn't the same
//     thing as checking a TEE signature, and why that's an acceptable
//     substitute within a single process).
//
// The nonce replay check (a THIRD, separate concern — anti-replay of the
// proof itself, not provenance of the commitment it references) is
// deliberately NOT done here — it needs e.mu and is done by the caller
// inside its own locked critical section (see processPlaceOrder). This
// function only validates and verifies; it consumes nothing.
//
// expectedToken is the asset the order will actually need — pair.QuoteToken
// for a buy, pair.BaseToken for a sell (mirrors calculateHold's own
// determination, computed independently here since calculateHold isn't
// callable yet at this point — it takes a *fully constructed* order,
// including an ID that doesn't exist until after this check passes).
//
// Returns (verified, error). verified is true only if a proof was present
// AND valid — false with a nil error means no proof was attached at all,
// which is a normal, allowed case (the feature is opt-in per order, not
// mandatory). A non-nil error means a proof WAS attached but is invalid,
// disabled, malformed, or references a commitment this TEE can't vouch
// for — callers must reject the whole order in that case, not silently
// place it as if no proof had been given.
func (e *Extension) verifySolvencyProofForOrder(req types.PlaceOrderRequest, expectedToken common.Address) (verified bool, err error) {
	hasProof := req.SolvencyProof != nil
	hasSignals := len(req.SolvencyPublicSignals) > 0
	if !hasProof && !hasSignals {
		return false, nil // no proof attached — allowed, not an error
	}
	if hasProof != hasSignals {
		return false, fmt.Errorf("solvencyProof and solvencyPublicSignals must both be present or both absent")
	}

	if e.verificationKey == nil {
		return false, fmt.Errorf("solvency proof verification is disabled on this extension — remove solvencyProof/solvencyPublicSignals, or contact the operator")
	}

	if err := solvency.VerifyProof(req.SolvencyProof, req.SolvencyPublicSignals, e.verificationKey); err != nil {
		return false, fmt.Errorf("solvency proof did not verify: %w", err)
	}

	// Cryptographic validity alone only proves internal consistency
	// (collateral >= threshold AND Poseidon(collateral, nonce) ==
	// commitment) — it says nothing about whether `commitment` came from
	// this TEE's own balance state. Check that separately.
	if len(req.SolvencyPublicSignals) != solvency.PublicSignalsLen {
		return false, fmt.Errorf("solvency proof has %d public signals, expected %d", len(req.SolvencyPublicSignals), solvency.PublicSignalsLen)
	}
	commitment := req.SolvencyPublicSignals[solvency.PublicSignalCommitmentIdx]
	user := strings.ToLower(req.Sender)

	e.solvencyMu.Lock()
	record, found := e.issuedCommitments[commitment]
	e.solvencyMu.Unlock()

	if !found {
		return false, fmt.Errorf("solvency proof references a commitment this TEE never issued — request one via GET_SOLVENCY_COMMITMENT first")
	}
	if record.User != user {
		return false, fmt.Errorf("solvency proof's commitment was issued to a different user")
	}
	if record.Token != expectedToken {
		return false, fmt.Errorf("solvency proof's commitment was issued for token %s, but this order needs %s", record.Token.Hex(), expectedToken.Hex())
	}
	if time.Since(time.Unix(record.IssuedAt, 0)) > SolvencyCommitmentTTL {
		return false, fmt.Errorf("solvency proof's commitment expired (issued more than %s ago) — request a fresh one", SolvencyCommitmentTTL)
	}

	// Provenance alone still isn't enough: nothing above confirms the
	// proof's `threshold` (what was actually proven >=) bears any
	// relation to what THIS order needs. Without this check, a proof
	// generated with a trivially small threshold (e.g. "1") would pass
	// every check above while being attached to an arbitrarily large
	// order — a real gap, caught while wiring up the frontend rather than
	// designed in from the start; see BUILD_NOTES.md.
	//
	// Restricted to limit orders: a market order's fill amount depends on
	// available liquidity at execution time, not a value fixable in
	// advance, so there's no single "required amount" to check a
	// pre-generated proof's threshold against. Reject proofs on market
	// orders outright rather than silently skip this check for them.
	if req.Type != orderbook.Limit {
		return false, fmt.Errorf("solvency proofs are only supported for limit orders (market order fill amounts aren't fixed in advance)")
	}
	var requiredAmount uint64
	if req.Side == orderbook.Buy {
		var ok bool
		requiredAmount, ok = safeMulDiv(req.Quantity, req.Price, pricePrecision)
		if !ok {
			return false, fmt.Errorf("overflow computing required amount: quantity * price exceeds uint64")
		}
	} else {
		requiredAmount = req.Quantity
	}
	threshold, err := strconv.ParseUint(req.SolvencyPublicSignals[solvency.PublicSignalThresholdIdx], 10, 64)
	if err != nil {
		return false, fmt.Errorf("solvency proof's threshold signal is not a valid uint64: %w", err)
	}
	if threshold < requiredAmount {
		return false, fmt.Errorf("solvency proof's threshold (%d) is less than what this order actually requires (%d)", threshold, requiredAmount)
	}

	return true, nil
}

// solvencyNonceFromRequest extracts the anti-replay nonce from a request's
// public signals — see the SolvencyProof field's doc comment in
// pkg/types/types.go for why this reuses the orderId slot rather than
// requiring a separate field. Callers must only call this after
// verifySolvencyProofForOrder has confirmed the signals are present and
// well-formed.
func solvencyNonceFromRequest(req types.PlaceOrderRequest) string {
	return req.SolvencyPublicSignals[solvency.PublicSignalOrderIDIdx]
}
