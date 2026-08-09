package solvency

import (
	"encoding/json"
	"fmt"
	"os"

	rapidsnarktypes "github.com/iden3/go-rapidsnark/types"
	"github.com/iden3/go-rapidsnark/verifier"
)

// PublicSignalsLen is the number of public signals solvency.circom's
// verifier expects, and their fixed order — confirmed empirically by
// generating a real proof in this session, not assumed from the circuit's
// declaration order (see circuits/README.md and
// contracts/SolvencyVerifier.sol's header for the same confirmation on the
// Solidity side).
const PublicSignalsLen = 4

const (
	PublicSignalValidIdx      = 0 // circuit output; 1 for any proof that verifies
	PublicSignalThresholdIdx  = 1
	PublicSignalOrderIDIdx    = 2 // see note in internal/extension/solvency.go on what this actually binds to
	PublicSignalCommitmentIdx = 3
)

// Proof mirrors snarkjs's proof.json output shape directly — deliberately
// not renamed/restructured, so a proof.json produced by
// `snarkjs groth16 prove` (or by a frontend using snarkjs.js) can be
// unmarshaled into this struct with no field mapping.
type Proof struct {
	PiA      []string   `json:"pi_a"`
	PiB      [][]string `json:"pi_b"`
	PiC      []string   `json:"pi_c"`
	Protocol string     `json:"protocol"`
	Curve    string     `json:"curve,omitempty"`
}

// VerifyProof verifies a Groth16 proof against the given verification key
// bytes (the raw JSON content of a verification_key.json, e.g. from
// circuits/artifacts/verification_key.json — see LoadVerificationKey).
// publicSignals must have length PublicSignalsLen, in the fixed order
// documented above. Returns nil if and only if the proof is valid for
// exactly these public signals — a caller must not skip checking that
// publicSignals actually contains the values it expects (e.g. the right
// orderId/nonce) before or after calling this; VerifyProof only confirms
// the proof is internally valid for whatever signals it's given.
func VerifyProof(proof *Proof, publicSignals []string, verificationKey []byte) error {
	if proof == nil {
		return fmt.Errorf("solvency: proof must not be nil")
	}
	if len(publicSignals) != PublicSignalsLen {
		return fmt.Errorf("solvency: expected %d public signals, got %d", PublicSignalsLen, len(publicSignals))
	}

	zkProof := rapidsnarktypes.ZKProof{
		Proof: &rapidsnarktypes.ProofData{
			A:        proof.PiA,
			B:        proof.PiB,
			C:        proof.PiC,
			Protocol: proof.Protocol,
		},
		PubSignals: publicSignals,
	}

	if err := verifier.VerifyGroth16(zkProof, verificationKey); err != nil {
		return fmt.Errorf("solvency: proof verification failed: %w", err)
	}
	return nil
}

// LoadVerificationKey reads a verification_key.json file from disk (the
// raw JSON bytes VerifyProof expects). Kept as a thin wrapper — the actual
// key is deliberately NOT go:embed'd into this package: Go's go:embed
// cannot reach outside its own package directory (no `../`), and
// circuits/artifacts/verification_key.json legitimately lives under
// circuits/, not pkg/solvency/, so duplicating it here would create a
// second copy that could silently drift out of sync with the one actually
// used to generate contracts/SolvencyVerifier.sol. Loading by path at
// startup (mirroring how internal/config already loads
// config/coston2/pairs.json by path, with an env var override) keeps
// there being exactly one copy of the real key.
func LoadVerificationKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("solvency: reading verification key at %s: %w", path, err)
	}
	// Sanity-check it's at least well-formed JSON before returning —
	// fails fast and clearly rather than deep inside verifier.VerifyGroth16
	// on the first proof anyone submits.
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("solvency: verification key at %s is not valid JSON: %w", path, err)
	}
	return data, nil
}
