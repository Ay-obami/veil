// Package solvency implements the off-chain half of Veil's ZK solvency
// proof flow: computing a Poseidon commitment over a user's real balance
// and packing the message the TEE signs to attest that commitment.
//
// This is the piece that closes the gap documented in
// circuits/solvency.circom notes 4 and 6, and in BUILD_NOTES.md: the
// circuit can prove `Poseidon(collateral, nonce) == commitment`, but until
// something ties `commitment` to a real balance, that's unconstrained math
// with no anchor to actual funds. Here, the TEE — which already
// authoritatively tracks real balances via pkg/balance, and is already the
// trust anchor for withdrawals via ECDSA signature (see
// internal/extension/withdraw.go) — computes the commitment directly from
// its own balance state and signs an attestation over it. A verifier
// (on-chain or off-chain) that trusts the TEE's signing key can then trust
// that `commitment` genuinely corresponds to a real balance at the time of
// issuance, without needing separate on-chain commitment storage.
//
// VERIFICATION STATUS: the Poseidon computation
// (github.com/iden3/go-iden3-crypto/poseidon) was cross-checked in the
// environment this was built in against circomlibjs's output for the same
// inputs (collateral=1500000, nonce=42) — both produced the identical
// field element (12360375947920094242516870753702830388276488141210735978590714515247575931361).
// That specific cross-check is real and gives confidence the Go and
// circuit-side hash agree. What is NOT yet verified: this package has not
// been compiled inside the main veil module in the sandbox this was
// written in (go-ethereum's Go>=1.24 requirement blocks that here, as with
// pkg/oracle/ftso.go before it) — run `go build ./... && go test ./...`
// locally to confirm.
package solvency

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/iden3/go-iden3-crypto/poseidon"
)

// MaxNonceBits bounds the randomly-generated nonce so it comfortably fits
// as a circuit input alongside a 64-bit collateral value (see
// circuits/solvency.circom) without needing special-case handling. 128
// bits of randomness is far more than needed to avoid collisions for any
// realistic number of commitments issued.
const MaxNonceBits = 128

// Commitment holds everything a user needs, off-chain, to later generate a
// ZK solvency proof: the real balance the TEE attested (informational —
// the user already knows their own balance), the private nonce, the
// resulting public commitment, and the TEE's signature over an attestation
// message binding (user, token, commitment, issuedAt).
type Commitment struct {
	Balance    uint64
	Nonce      *big.Int
	Commitment *big.Int
	IssuedAt   int64
}

// NewCommitment computes Poseidon(balance, nonce) for a freshly-generated
// random nonce. balance should come directly from the TEE's own
// authoritative balance state (e.g. pkg/balance.Manager.Get(...).Available)
// — this function does not look up balances itself, to keep it testable
// independent of the balance manager.
func NewCommitment(balance uint64, issuedAt int64) (*Commitment, error) {
	nonce, err := randomNonce()
	if err != nil {
		return nil, fmt.Errorf("solvency: generating nonce: %w", err)
	}
	return NewCommitmentWithNonce(balance, nonce, issuedAt)
}

// NewCommitmentWithNonce computes Poseidon(balance, nonce) for a caller-
// supplied nonce. Exposed separately from NewCommitment so tests can use
// deterministic nonces instead of random ones.
func NewCommitmentWithNonce(balance uint64, nonce *big.Int, issuedAt int64) (*Commitment, error) {
	if nonce == nil {
		return nil, fmt.Errorf("solvency: nonce must not be nil")
	}
	hash, err := poseidon.Hash([]*big.Int{new(big.Int).SetUint64(balance), nonce})
	if err != nil {
		return nil, fmt.Errorf("solvency: computing poseidon hash: %w", err)
	}
	return &Commitment{
		Balance:    balance,
		Nonce:      nonce,
		Commitment: hash,
		IssuedAt:   issuedAt,
	}, nil
}

// randomNonce returns a cryptographically random nonce in [0, 2^MaxNonceBits).
func randomNonce() (*big.Int, error) {
	max := new(big.Int).Lsh(big.NewInt(1), MaxNonceBits)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// PackAttestationMessage returns abi.encodePacked(user, token, commitment,
// issuedAt) as raw bytes — the exact message the TEE sign server hashes
// (keccak256) and signs (EIP-191-prefixed), mirroring
// internal/extension/withdraw.go's packWithdrawalMessage so the same
// on-chain _recoverSigner-style verification pattern can be reused for
// commitment attestations. commitment is encoded as a left-padded 32-byte
// big-endian value (bytes32), matching how Solidity would receive it.
//
// NOT YET WIRED: nothing calls _recoverSigner against this message on-chain
// yet — see BUILD_NOTES.md. This function exists so that integration can
// be added without re-deriving the exact byte layout later.
func PackAttestationMessage(user, token common.Address, commitment *big.Int, issuedAt int64) ([]byte, error) {
	if commitment == nil {
		return nil, fmt.Errorf("solvency: commitment must not be nil")
	}
	if commitment.Sign() < 0 {
		return nil, fmt.Errorf("solvency: commitment must not be negative")
	}

	commitmentBytes := commitment.Bytes()
	if len(commitmentBytes) > 32 {
		return nil, fmt.Errorf("solvency: commitment does not fit in 32 bytes (got %d)", len(commitmentBytes))
	}

	buf := make([]byte, 0, 20+20+32+32)
	buf = append(buf, user.Bytes()...)
	buf = append(buf, token.Bytes()...)

	commitmentPadded := make([]byte, 32)
	copy(commitmentPadded[32-len(commitmentBytes):], commitmentBytes)
	buf = append(buf, commitmentPadded...)

	issuedAtBytes := make([]byte, 32)
	new(big.Int).SetInt64(issuedAt).FillBytes(issuedAtBytes)
	buf = append(buf, issuedAtBytes...)

	return buf, nil
}
