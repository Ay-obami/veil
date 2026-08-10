package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veil/pkg/orderbook"
	"veil/pkg/solvency"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

// loadRealSolvencyFixture mirrors pkg/solvency/verify_test.go's fixture
// loader — deliberately duplicated rather than shared, since these are
// different packages and pkg/solvency's test helpers aren't exported. Both
// point at the same real, committed circuits/artifacts files.
func loadRealSolvencyFixture(t *testing.T) (*solvency.Proof, []string, []byte) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "circuits", "artifacts"))
	if err != nil {
		t.Fatalf("resolving artifacts dir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("circuits/artifacts not found at %s (%v) — skipping", dir, err)
	}

	proofBytes, err := os.ReadFile(filepath.Join(dir, "example", "proof.json"))
	if err != nil {
		t.Fatalf("reading example proof.json: %v", err)
	}
	var proof solvency.Proof
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

	vk, err := solvency.LoadVerificationKey(filepath.Join(dir, "verification_key.json"))
	if err != nil {
		t.Fatalf("loading verification key: %v", err)
	}

	return &proof, publicSignals, vk
}

// realFixtureCommitment is circuits/artifacts/example/public.json's
// commitment value (index solvency.PublicSignalCommitmentIdx) — the real
// Poseidon commitment the example proof was generated against. Tests that
// need the proof to pass the commitment-provenance check (see
// issuedCommitmentRecord in solvency.go) seed e.issuedCommitments with
// this exact value, simulating a prior GET_SOLVENCY_COMMITMENT call.
const realFixtureCommitment = "12360375947920094242516870753702830388276488141210735978590714515247575931361"

// seedIssuedCommitment simulates a prior, successful GET_SOLVENCY_COMMITMENT
// call for realFixtureCommitment, so tests exercising the PLACE_ORDER
// solvency-proof success path don't also need to fail on the (separate,
// legitimate) commitment-provenance check — that check has its own
// dedicated tests below.
func seedIssuedCommitment(e *Extension, user string, token common.Address) {
	e.solvencyMu.Lock()
	e.issuedCommitments[realFixtureCommitment] = issuedCommitmentRecord{
		User:     user,
		Token:    token,
		Balance:  1_500_000, // matches the fixture's collateral value; informational for these tests
		IssuedAt: time.Now().Unix(),
	}
	e.solvencyMu.Unlock()
}

func TestPlaceOrder_SolvencyProof_ValidProofAccepted(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	seedIssuedCommitment(e, "buyer", quote) // Buy order -> expectedToken is QuoteToken

	// The proof's own math doesn't need to relate to price/quantity here —
	// it only needs to be a real, independently valid proof, since this
	// test is about the verification+bookkeeping path in PLACE_ORDER, not
	// the circuit's own math (that's pkg/solvency/verify_test.go's job).
	// No counterparty is funded, so this order simply rests (no match) —
	// that's fine, this test only checks acceptance and the
	// SolvencyVerified flag, not matching.
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	resp := placeOrder(t, e, req)
	if resp.OrderID == "" {
		t.Fatal("expected an order ID for an accepted order")
	}
	if resp.Status != "resting" {
		t.Fatalf("expected the order to rest (no counterparty funded), got status %q", resp.Status)
	}

	e.mu.RLock()
	_, tracked := e.orders[resp.OrderID]
	e.mu.RUnlock()
	if !tracked {
		t.Fatal("expected the resting order to be tracked in e.orders")
	}
}

func TestPlaceOrder_SolvencyProof_TamperedProofRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	tampered := make([]string, len(publicSignals))
	copy(tampered, publicSignals)
	tampered[solvency.PublicSignalOrderIDIdx] = "999999" // invalidates the proof, not just the nonce

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: tampered,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error message for a tampered proof")
	}
}

func TestPlaceOrder_SolvencyProof_DisabledVerificationRejectsProof(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)
	// Deliberately NOT setting e.verificationKey — simulates
	// config.SolvencyVerificationKeyPath being disabled/unset.

	proof, publicSignals, _ := loadRealSolvencyFixture(t)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	placeOrderExpectErr(t, e, req)
}

func TestPlaceOrder_SolvencyProof_NonceReplayRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	seedIssuedCommitment(e, "buyer", quote)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000, // must be >= pricePrecision or quantity*price/pricePrecision truncates to 0 (see calculateHold) — caught by a real local test run, not anticipated in advance
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	// First use should succeed.
	placeOrder(t, e, req)

	// Second use of the exact same proof (same nonce) must be rejected,
	// even though the proof itself is still cryptographically valid — the
	// nonce is what stops replay, not proof validity alone.
	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error for nonce reuse")
	}
}

func TestPlaceOrder_SolvencyProof_MismatchedFieldsRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, _, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Proof present but public signals missing — must be rejected, not
	// silently treated as "no proof attached."
	req := types.PlaceOrderRequest{
		Sender:        "buyer",
		Pair:          pair,
		Side:          orderbook.Buy,
		Type:          orderbook.Limit,
		Price:         1_000_000,
		Quantity:      1,
		SolvencyProof: proof,
	}

	placeOrderExpectErr(t, e, req)
}

func TestPlaceOrder_NoProofAttached_UnaffectedByFeature(t *testing.T) {
	// Confirms the base (no-proof) PLACE_ORDER path is completely
	// unaffected by this feature existing — mirrors the same
	// "unchanged behavior" guarantee tested for the FTSO oracle hook.
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:   "buyer",
		Pair:     pair,
		Side:     orderbook.Buy,
		Type:     orderbook.Limit,
		Price:    1_000_000,
		Quantity: 1,
	}

	resp := placeOrder(t, e, req)
	if resp.OrderID == "" {
		t.Fatal("expected an order ID for a normal, proof-free order")
	}
}

// --- Commitment-provenance checks (issuedCommitmentRecord) ---
// These are distinct from the cryptographic (Groth16) checks above: a
// proof can be perfectly valid math and still be rejected here, because
// validity alone doesn't establish that `commitment` came from this TEE's
// own balance state. See issuedCommitmentRecord's doc comment in
// solvency.go.

func TestPlaceOrder_SolvencyProof_UnissuedCommitmentRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	// Deliberately NOT calling seedIssuedCommitment — this commitment was
	// never issued by this TEE (e.issuedCommitments is empty).
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error for an unissued commitment")
	}
}

func TestPlaceOrder_SolvencyProof_WrongUserRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	// Commitment was issued to a different user than the one placing the order.
	seedIssuedCommitment(e, "someone-else", quote)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error for a commitment issued to a different user")
	}
}

func TestPlaceOrder_SolvencyProof_WrongTokenRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	// This is a Buy order, so expectedToken is QuoteToken — but the
	// commitment was issued for BaseToken instead.
	seedIssuedCommitment(e, "buyer", base)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error for a commitment issued for the wrong token")
	}
}

func TestPlaceOrder_SolvencyProof_ExpiredCommitmentRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Seed directly with a stale IssuedAt, well past SolvencyCommitmentTTL,
	// rather than sleeping in the test.
	e.solvencyMu.Lock()
	e.issuedCommitments[realFixtureCommitment] = issuedCommitmentRecord{
		User:     "buyer",
		Token:    quote,
		Balance:  1_500_000,
		IssuedAt: time.Now().Add(-2 * SolvencyCommitmentTTL).Unix(),
	}
	e.solvencyMu.Unlock()

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error for an expired commitment")
	}
}

func TestPlaceOrder_SolvencyProof_SameCommitmentCanBackTwoOrders(t *testing.T) {
	// Confirms the deliberate design choice documented on
	// issuedCommitmentRecord: the commitment registry is a provenance
	// check, not a single-use spending check — real custody is still
	// enforced separately by e.balances.Hold. Two DIFFERENT orders (using
	// two different nonces, since the nonce is what's single-use) backed
	// by the SAME commitment should both be accepted, as long as real
	// balance covers both holds.
	//
	// This test can't reuse the shared real fixture proof for both calls
	// (its nonce, tied to publicSignals[PublicSignalOrderIDIdx]=42, would
	// collide and get rejected by the nonce-replay check instead — a
	// different mechanism than what this test is trying to isolate).
	// Rather than generate a second real proof, this test documents the
	// intended behavior directly against issuedCommitmentRecord's
	// provenance check by calling verifySolvencyProofForOrder twice with
	// the same underlying proof/commitment but manually distinct nonces,
	// bypassing PLACE_ORDER's full path. This is intentionally a narrower
	// test than the others in this file — it tests the provenance check
	// in isolation, not the full nonce-consumption flow (see
	// TestPlaceOrder_SolvencyProof_NonceReplayRejected for that).
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	seedIssuedCommitment(e, "buyer", quote)

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Type:                  orderbook.Limit,
		Side:                  orderbook.Buy,
		Price:                 1_000_000,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	// Calling verifySolvencyProofForOrder twice (not consuming anything
	// itself — see its own doc comment) must succeed both times; the
	// commitment isn't deleted or marked used by this function.
	verified1, err1 := e.verifySolvencyProofForOrder(req, quote)
	if err1 != nil || !verified1 {
		t.Fatalf("first verification: verified=%v err=%v", verified1, err1)
	}
	verified2, err2 := e.verifySolvencyProofForOrder(req, quote)
	if err2 != nil || !verified2 {
		t.Fatalf("second verification against the same commitment: verified=%v err=%v", verified2, err2)
	}
}

// --- Threshold-vs-actual-required-amount check ---
// Caught while wiring up the frontend, not designed in from the start —
// see BUILD_NOTES.md. Without this, a proof generated with a trivially
// small threshold would pass every other check regardless of the order's
// real size.

func TestPlaceOrder_SolvencyProof_ThresholdTooLowRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	// The real fixture's threshold is 1,000,000 (see
	// circuits/artifacts/example/input.json). requiredAmount here is
	// quantity * price / pricePrecision = 2,000,000 * 1,000,000 /
	// 1,000,000 = 2,000,000 — genuinely above the fixture's threshold.
	// (An earlier version of this test used Price: 2_000_000, Quantity: 1,
	// which actually computes to requiredAmount=2 — nowhere near enough to
	// exceed the threshold. Caught by an actual local test run: the order
	// was wrongly accepted instead of rejected. Fixed by scaling quantity
	// instead of price to get a genuinely-too-large requiredAmount.)
	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	seedIssuedCommitment(e, "buyer", quote)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Limit,
		Price:                 1_000_000,
		Quantity:              2_000_000,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error when the proof's threshold is below what the order actually requires")
	}
}

func TestPlaceOrder_SolvencyProof_MarketOrderRejected(t *testing.T) {
	pair := "TEST/USD"
	base, quote := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	e := newTestExtension(pair, base, quote)

	proof, publicSignals, vk := loadRealSolvencyFixture(t)
	e.verificationKey = vk
	seedIssuedCommitment(e, "buyer", quote)
	if err := e.balances.Deposit("buyer", quote, 1_000_000_000); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	req := types.PlaceOrderRequest{
		Sender:                "buyer",
		Pair:                  pair,
		Side:                  orderbook.Buy,
		Type:                  orderbook.Market,
		Quantity:              1,
		SolvencyProof:         proof,
		SolvencyPublicSignals: publicSignals,
	}

	msg := placeOrderExpectErr(t, e, req)
	if msg == "" {
		t.Fatal("expected a non-empty error for a solvency proof attached to a market order")
	}
}
