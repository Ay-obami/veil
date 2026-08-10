package solvency

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestNewCommitmentWithNonce_KnownVector locks in the exact cross-check
// performed in this session: github.com/iden3/go-iden3-crypto/poseidon's
// Hash([]*big.Int{1500000, 42}) was confirmed to produce the identical
// field element that circomlibjs computed for the same inputs during the
// ZK circuit work (see circuits/test/solvency.test.js and BUILD_NOTES.md).
// This test exists so that fact stays true — a future dependency bump that
// silently changed the Poseidon parameters would break this test, which is
// exactly the point: Go and circuit-side hashing MUST keep agreeing.
func TestNewCommitmentWithNonce_KnownVector(t *testing.T) {
	const expected = "12360375947920094242516870753702830388276488141210735978590714515247575931361"

	c, err := NewCommitmentWithNonce(1_500_000, big.NewInt(42), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Commitment.String() != expected {
		t.Fatalf("commitment mismatch:\n  got:      %s\n  expected: %s\n(if this legitimately changed, the ZK circuit's test vectors need updating too — this is a cross-system invariant, not just a unit test)", c.Commitment.String(), expected)
	}
}

func TestNewCommitmentWithNonce_Deterministic(t *testing.T) {
	c1, err := NewCommitmentWithNonce(2_000_000, big.NewInt(7), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c2, err := NewCommitmentWithNonce(2_000_000, big.NewInt(7), 999) // different issuedAt, same balance+nonce
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c1.Commitment.Cmp(c2.Commitment) != 0 {
		t.Fatalf("expected identical commitment for identical (balance, nonce), got %s vs %s", c1.Commitment, c2.Commitment)
	}
}

func TestNewCommitmentWithNonce_DifferentNonceDifferentCommitment(t *testing.T) {
	c1, err := NewCommitmentWithNonce(2_000_000, big.NewInt(1), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c2, err := NewCommitmentWithNonce(2_000_000, big.NewInt(2), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c1.Commitment.Cmp(c2.Commitment) == 0 {
		t.Fatalf("expected different commitments for different nonces, got the same value: %s", c1.Commitment)
	}
}

func TestNewCommitmentWithNonce_NilNonceRejected(t *testing.T) {
	if _, err := NewCommitmentWithNonce(1_000_000, nil, 0); err == nil {
		t.Fatal("expected an error for a nil nonce, got none")
	}
}

func TestNewCommitment_ProducesUsableNonce(t *testing.T) {
	c, err := NewCommitment(1_000_000, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Nonce == nil || c.Nonce.Sign() < 0 {
		t.Fatalf("expected a non-nil, non-negative nonce, got %v", c.Nonce)
	}
	// Re-deriving with the same random nonce must reproduce the same commitment.
	c2, err := NewCommitmentWithNonce(1_000_000, c.Nonce, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Commitment.Cmp(c2.Commitment) != 0 {
		t.Fatalf("re-deriving with the same nonce produced a different commitment")
	}
}

func TestPackAttestationMessage_Layout(t *testing.T) {
	user := common.HexToAddress("0x000000000000000000000000000000000000A1")
	token := common.HexToAddress("0x000000000000000000000000000000000000B2")
	commitment := big.NewInt(12345)
	issuedAt := int64(1700000000)

	msg, err := PackAttestationMessage(user, token, commitment, issuedAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// address(20) + address(20) + bytes32(32) + bytes32(32) = 104 bytes,
	// mirroring internal/extension/withdraw.go's packWithdrawalMessage
	// layout style (see that file's comment on why this shape is chosen —
	// keccak256'd and EIP-191-signed by the TEE sign server).
	const wantLen = 20 + 20 + 32 + 32
	if len(msg) != wantLen {
		t.Fatalf("expected packed message length %d, got %d", wantLen, len(msg))
	}

	if !bytesEqual(msg[0:20], user.Bytes()) {
		t.Errorf("user address not at expected offset")
	}
	if !bytesEqual(msg[20:40], token.Bytes()) {
		t.Errorf("token address not at expected offset")
	}
	// commitment occupies msg[40:72], big-endian, left-padded.
	gotCommitment := new(big.Int).SetBytes(msg[40:72])
	if gotCommitment.Cmp(commitment) != 0 {
		t.Errorf("commitment field mismatch: got %s, want %s", gotCommitment, commitment)
	}
	// issuedAt occupies msg[72:104].
	gotIssuedAt := new(big.Int).SetBytes(msg[72:104])
	if gotIssuedAt.Int64() != issuedAt {
		t.Errorf("issuedAt field mismatch: got %d, want %d", gotIssuedAt.Int64(), issuedAt)
	}
}

func TestPackAttestationMessage_RejectsNilCommitment(t *testing.T) {
	user := common.HexToAddress("0x00000000000000000000000000000000000001")
	token := common.HexToAddress("0x00000000000000000000000000000000000002")
	if _, err := PackAttestationMessage(user, token, nil, 0); err == nil {
		t.Fatal("expected an error for a nil commitment, got none")
	}
}

func TestPackAttestationMessage_RejectsNegativeCommitment(t *testing.T) {
	user := common.HexToAddress("0x00000000000000000000000000000000000001")
	token := common.HexToAddress("0x00000000000000000000000000000000000002")
	if _, err := PackAttestationMessage(user, token, big.NewInt(-1), 0); err == nil {
		t.Fatal("expected an error for a negative commitment, got none")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
