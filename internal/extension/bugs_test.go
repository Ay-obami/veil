package extension

// Tests in this file are adversarial: they assert the CORRECT/expected behavior
// for findings raised by reviewers. A FAILING test means the finding is
// confirmed; a passing test means the finding was wrong or already mitigated.

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"veil/pkg/balance"
	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

const (
	testBaseHex  = "0x1111111111111111111111111111111111111111"
	testQuoteHex = "0x2222222222222222222222222222222222222222"
	testPair     = "FLR/USDT"
)

func mkAddr(i int) string { return strings.ToLower(fmt.Sprintf("0x%040d", i+1)) }

// ---------------------------------------------------------------------------
// C1 — appendBounded retains the original backing array (does it bound cap)?
// ---------------------------------------------------------------------------

func TestC1_AppendBoundedBackingArrayBound(t *testing.T) {
	const maxLen = 10
	const writes = 10000

	var s []int
	maxCap := 0
	for i := 0; i < writes; i++ {
		s = appendBounded(s, i, maxLen)
		if cap(s) > maxCap {
			maxCap = cap(s)
		}
	}
	if len(s) != maxLen {
		t.Fatalf("len: got %d, want %d", len(s), maxLen)
	}

	// Leak hypothesis: cap grows unboundedly because trim doesn't copy.
	// CORRECT behavior: cap stays within a bounded constant factor of maxLen.
	t.Logf("after %d appends with maxLen=%d: final len=%d cap=%d, peak cap=%d", writes, maxLen, len(s), cap(s), maxCap)
	if maxCap > maxLen*4 {
		t.Errorf("CONFIRMED C1: peak cap=%d exceeded 4*maxLen=%d (heap retention)", maxCap, maxLen*4)
	}
}

// ---------------------------------------------------------------------------
// C4 — uint64 overflow in Quantity * Price / pricePrecision
// ---------------------------------------------------------------------------

func TestC4_PlaceOrderRejectsOverflow(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	e := newTestExtension(testPair, base, quote)

	user := mkAddr(0)
	if err := e.balances.Deposit(user, quote, math.MaxUint64); err != nil {
		t.Fatal(err)
	}

	// Pick (price, qty) so (price * qty) wraps to a NON-zero value.
	// (2^32 + 1)^2 = 2^64 + 2^33 + 1; mod 2^64 = 2^33 + 1 = 8589934593.
	price := uint64(1)<<32 + 1
	qty := uint64(1)<<32 + 1
	wrappedProduct := price * qty // explicit wrap behavior we depend on
	if wrappedProduct == 0 {
		t.Fatalf("test setup: product wrapped to zero, pick different operands")
	}
	wrappedHold := wrappedProduct / pricePrecision

	body, _ := json.Marshal(types.PlaceOrderRequest{
		Sender: user, Pair: testPair, Side: orderbook.Buy, Type: orderbook.Limit,
		Price: price, Quantity: qty,
	})
	ar := e.processPlaceOrder(teetypes.Action{}, &instruction.DataFixed{}, body)

	// CORRECT behavior: reject with overflow error before holding anything.
	if ar.Status == 1 {
		held := e.balances.Get(user, quote).Held
		t.Errorf("CONFIRMED C4: order accepted with overflowing q*p; held=%d (vs wrapped %d, vs true q*p ≈ 1.85e19 which is meaningless)",
			held, wrappedHold)
		return
	}
	if !strings.Contains(strings.ToLower(ar.Log), "overflow") {
		t.Logf("rejected with reason (not overflow): %s", ar.Log)
	}
}

// ---------------------------------------------------------------------------
// C5 — restart loses balances; on-chain deposits become unrecoverable
// ---------------------------------------------------------------------------

func TestC5_RestartPreservesDepositedBalance(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	persistPath := filepath.Join(t.TempDir(), "balances.json")

	e1 := newTestExtension(testPair, base, quote)
	if err := e1.balances.SetPersistPath(persistPath); err != nil {
		t.Fatalf("SetPersistPath e1: %v", err)
	}

	user := mkAddr(0)
	if err := e1.balances.Deposit(user, quote, 1000); err != nil {
		t.Fatal(err)
	}
	if got := e1.balances.Get(user, quote).Available; got != 1000 {
		t.Fatalf("pre-restart balance: got %d, want 1000", got)
	}

	// Simulate a process restart by constructing a fresh Extension that loads
	// the persisted snapshot.
	e2 := newTestExtension(testPair, base, quote)
	if err := e2.balances.SetPersistPath(persistPath); err != nil {
		t.Fatalf("SetPersistPath e2: %v", err)
	}

	got := e2.balances.Get(user, quote).Available
	if got != 1000 {
		t.Errorf("post-restart Available=%d, want 1000 (deposit not persisted)", got)
	}
}

// TestC5_RestartReleasesHeldOnLoad verifies the load-time migration: any Held
// balance is moved back to Available, since the orders that held the funds
// are gone after a restart.
func TestC5_RestartReleasesHeldOnLoad(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	persistPath := filepath.Join(t.TempDir(), "balances.json")

	e1 := newTestExtension(testPair, base, quote)
	if err := e1.balances.SetPersistPath(persistPath); err != nil {
		t.Fatal(err)
	}

	user := mkAddr(0)
	_ = e1.balances.Deposit(user, quote, 1000)
	_ = e1.balances.Hold(user, quote, 400)
	bal := e1.balances.Get(user, quote)
	if bal.Available != 600 || bal.Held != 400 {
		t.Fatalf("pre-restart: got Available=%d Held=%d, want 600/400", bal.Available, bal.Held)
	}

	e2 := newTestExtension(testPair, base, quote)
	if err := e2.balances.SetPersistPath(persistPath); err != nil {
		t.Fatal(err)
	}
	bal = e2.balances.Get(user, quote)
	if bal.Available != 1000 || bal.Held != 0 {
		t.Errorf("post-restart: got Available=%d Held=%d, want 1000/0 (Held migrated to Available)", bal.Available, bal.Held)
	}
}

// ---------------------------------------------------------------------------
// C8 — buy at improved price leaves dust in Held
// ---------------------------------------------------------------------------

func TestC8_BuyAtImprovedPriceFullyReleasesHeld(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	e := newTestExtension(testPair, base, quote)

	seller := mkAddr(0)
	buyer := mkAddr(1)
	if err := e.balances.Deposit(seller, base, 1000); err != nil {
		t.Fatal(err)
	}
	if err := e.balances.Deposit(buyer, quote, 1_000_000); err != nil {
		t.Fatal(err)
	}

	// Seller asks at 95_000_000 (~$95 at pricePrecision=1_000_000).
	placeOrder(t, e, types.PlaceOrderRequest{
		Sender: seller, Pair: testPair, Side: orderbook.Sell, Type: orderbook.Limit,
		Price: 95_000_000, Quantity: 1000,
	})
	// Buyer bids at 110_000_000 — improvement of 15_000_000 raw per unit (~$15).
	resp := placeOrder(t, e, types.PlaceOrderRequest{
		Sender: buyer, Pair: testPair, Side: orderbook.Buy, Type: orderbook.Limit,
		Price: 110_000_000, Quantity: 1000,
	})
	if resp.Status != "filled" {
		t.Fatalf("status: got %s, want filled", resp.Status)
	}

	bal := e.balances.Get(buyer, quote)
	// CORRECT behavior: fully filled limit buy releases everything.
	if bal.Held != 0 {
		t.Errorf("CONFIRMED C8: buy filled at improved price leaves Held=%d (expected 0)", bal.Held)
	}
}

// ---------------------------------------------------------------------------
// H1 — user-keyed balance map is unbounded across distinct users
// ---------------------------------------------------------------------------

func TestH1_BalanceManagerEvictsEmptyUsers(t *testing.T) {
	bm := balance.NewManager()
	addr := common.HexToAddress(testBaseHex)

	const N = 5000
	for i := 0; i < N; i++ {
		u := mkAddr(i)
		if err := bm.Deposit(u, addr, 1); err != nil {
			t.Fatal(err)
		}
		if err := bm.Withdraw(u, addr, 1); err != nil {
			t.Fatal(err)
		}
	}
	if got := bm.UserCount(); got != N {
		t.Fatalf("pre-evict count: got %d, want %d", got, N)
	}

	removed := bm.EvictEmpty()
	if removed != N {
		t.Errorf("EvictEmpty returned %d, want %d", removed, N)
	}
	if got := bm.UserCount(); got != 0 {
		t.Errorf("post-evict count: got %d, want 0", got)
	}
}

// TestH1_EvictEmptyKeepsActiveUsers verifies that users with non-zero balances
// or non-zero held amounts are NOT evicted.
func TestH1_EvictEmptyKeepsActiveUsers(t *testing.T) {
	bm := balance.NewManager()
	addr := common.HexToAddress(testBaseHex)

	// User A has Available, user B has Held only, user C is fully empty.
	a := mkAddr(0)
	b := mkAddr(1)
	c := mkAddr(2)
	_ = bm.Deposit(a, addr, 100)
	_ = bm.Deposit(b, addr, 100)
	_ = bm.Hold(b, addr, 100) // moves to Held, leaves Available=0
	_ = bm.Deposit(c, addr, 1)
	_ = bm.Withdraw(c, addr, 1)

	if got := bm.UserCount(); got != 3 {
		t.Fatalf("pre-evict: got %d, want 3", got)
	}
	removed := bm.EvictEmpty()
	if removed != 1 {
		t.Errorf("EvictEmpty: got %d removed, want 1", removed)
	}
	if got := bm.UserCount(); got != 2 {
		t.Errorf("post-evict: got %d, want 2 (A and B retained)", got)
	}
}
