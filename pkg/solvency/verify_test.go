package solvency

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// artifactsDir locates circuits/artifacts relative to this test file,
// independent of the working directory `go test` is invoked from.
func artifactsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "circuits", "artifacts"))
	if err != nil {
		t.Fatalf("resolving artifacts dir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("circuits/artifacts not found at %s (%v) — skipping, this test needs the repo's real generated artifacts", dir, err)
	}
	return dir
}

func loadRealFixture(t *testing.T) (*Proof, []string, []byte) {
	t.Helper()
	dir := artifactsDir(t)

	proofBytes, err := os.ReadFile(filepath.Join(dir, "example", "proof.json"))
	if err != nil {
		t.Fatalf("reading example proof.json: %v", err)
	}
	var proof Proof
	if err := json.Unmarshal(proofBytes, &proof); err != nil {
		t.Fatalf("unmarshaling example proof.json: %v", err)
	}

	publicBytes, err := os.ReadFile(filepath.Join(dir, "example", "public.json"))
	if err != nil {
		t.Fatalf("reading example public.json: %v", err)
	}
	var publicSignals []string
	if err := json.Unmarshal(publicBytes, &publicSignals); err != nil {
		t.Fatalf("unmarshaling example public.json: %v", err)
	}

	vk, err := LoadVerificationKey(filepath.Join(dir, "verification_key.json"))
	if err != nil {
		t.Fatalf("loading verification key: %v", err)
	}

	return &proof, publicSignals, vk
}

// TestVerifyProof_RealFixture_Valid confirms Go-side verification accepts
// the repo's actual committed example proof — the same proof documented
// in circuits/artifacts/README.md and cross-verified via snarkjs in
// BUILD_NOTES.md. This is not a synthetic test double; it's the real
// artifact a prover would actually produce.
func TestVerifyProof_RealFixture_Valid(t *testing.T) {
	proof, publicSignals, vk := loadRealFixture(t)

	if err := VerifyProof(proof, publicSignals, vk); err != nil {
		t.Fatalf("expected the real example proof to verify, got error: %v", err)
	}
}

// TestVerifyProof_RealFixture_TamperedOrderID mirrors the exact tamper
// test performed manually against snarkjs in this session (see
// BUILD_NOTES.md) — changing PublicSignalOrderIDIdx must make
// verification fail. This is the case that matters most operationally:
// it's what stops a proof generated for one order from being replayed
// against a different one, once the orderId/nonce binding is actually
// enforced by the caller (see internal/extension/solvency.go).
func TestVerifyProof_RealFixture_TamperedOrderID(t *testing.T) {
	proof, publicSignals, vk := loadRealFixture(t)

	tampered := make([]string, len(publicSignals))
	copy(tampered, publicSignals)
	tampered[PublicSignalOrderIDIdx] = "999999"

	if err := VerifyProof(proof, tampered, vk); err == nil {
		t.Fatal("expected verification to fail for a tampered orderId, but it succeeded")
	}
}

// TestVerifyProof_RealFixture_TamperedCommitment confirms tampering with
// the commitment (not just orderId) is also caught — the other half of
// what this proof is supposed to bind.
func TestVerifyProof_RealFixture_TamperedCommitment(t *testing.T) {
	proof, publicSignals, vk := loadRealFixture(t)

	tampered := make([]string, len(publicSignals))
	copy(tampered, publicSignals)
	tampered[PublicSignalCommitmentIdx] = "1"

	if err := VerifyProof(proof, tampered, vk); err == nil {
		t.Fatal("expected verification to fail for a tampered commitment, but it succeeded")
	}
}

func TestVerifyProof_RejectsWrongSignalCount(t *testing.T) {
	proof, publicSignals, vk := loadRealFixture(t)

	if err := VerifyProof(proof, publicSignals[:2], vk); err == nil {
		t.Fatal("expected an error for the wrong number of public signals, but got none")
	}
}

func TestVerifyProof_RejectsNilProof(t *testing.T) {
	_, publicSignals, vk := loadRealFixture(t)

	if err := VerifyProof(nil, publicSignals, vk); err == nil {
		t.Fatal("expected an error for a nil proof, but got none")
	}
}

func TestLoadVerificationKey_RejectsMissingFile(t *testing.T) {
	if _, err := LoadVerificationKey("/nonexistent/path/verification_key.json"); err == nil {
		t.Fatal("expected an error for a missing file, but got none")
	}
}

func TestLoadVerificationKey_RejectsInvalidJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-json-*.json")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	if _, err := f.WriteString("this is not json"); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()

	if _, err := LoadVerificationKey(f.Name()); err == nil {
		t.Fatal("expected an error for invalid JSON, but got none")
	}
}
