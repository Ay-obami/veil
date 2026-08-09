package extension

// Concurrency tests intended to catch races and state-leak bugs raised by reviewers.
// Run with `go test -race ./internal/extension/...`. A race-detector hit fails the test.

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// ---------------------------------------------------------------------------
// C3 — nextOrderID is a non-atomic global counter
// ---------------------------------------------------------------------------

func TestC3_NextOrderIDConcurrentUniqueness(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	e := newTestExtension(testPair, base, quote)

	const N = 5000
	const G = 32 // goroutines

	var collisions atomic.Int64
	var mu sync.Mutex
	seen := make(map[string]struct{}, N*G)

	var wg sync.WaitGroup
	wg.Add(G)
	for g := 0; g < G; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < N; i++ {
				id := e.nextOrderID()
				mu.Lock()
				if _, dup := seen[id]; dup {
					collisions.Add(1)
				}
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if c := collisions.Load(); c > 0 {
		t.Errorf("CONFIRMED C3: %d duplicate IDs across %d concurrent calls", c, N*G)
	}
	// -race may have already failed the test by here on the unsynchronized counter increment.
}

// ---------------------------------------------------------------------------
// C7 — *Order aliasing race between handleEvictions and history readers
// ---------------------------------------------------------------------------

// processExportHistory marshals resp.Matches/Orders OUTSIDE the RLock.
// Eviction writes ev.Remaining=0 on a *Order pointer that's also in history.orders.
// If the marshal happens to dereference the same pointer concurrently, -race fires.
func TestC7_HistoryVsEvictionDataRace(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	e := newTestExtension(testPair, base, quote)

	prev := MaxLevelsPerSide
	MaxLevelsPerSide = 5
	t.Cleanup(func() { MaxLevelsPerSide = prev })

	const Users = 50
	for i := 0; i < Users; i++ {
		u := mkAddr(i)
		if err := e.balances.Deposit(u, base, 100_000); err != nil {
			t.Fatal(err)
		}
	}
	// Seed a history entry for user 0 so ExportHistory has something to marshal.
	placeOrder(t, e, types.PlaceOrderRequest{
		Sender: mkAddr(0), Pair: testPair, Side: orderbook.Sell, Type: orderbook.Limit,
		Price: 100, Quantity: 1,
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Reader: hammer ExportHistory for user 0.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			body, _ := json.Marshal(types.ExportHistoryRequest{Sender: mkAddr(0)})
			_ = e.processExportHistory(teetypes.Action{}, &instruction.DataFixed{}, body)
		}
	}()

	// Writers: place orders that trigger eviction (mutating ev.Remaining=0 on shared pointers).
	wg.Add(4)
	for w := 0; w < 4; w++ {
		go func(seed int) {
			defer wg.Done()
			i := seed
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = placeQuiet(e, types.PlaceOrderRequest{
					Sender: mkAddr(i % Users), Pair: testPair, Side: orderbook.Sell, Type: orderbook.Limit,
					Price:    uint64(100 + i),
					Quantity: 1,
				})
				i += 4
			}
		}(w)
	}

	// Run for a fixed budget; -race will fail the test on any data race detected.
	deadline := make(chan struct{})
	go func() {
		// Tight budget — race detector tends to fire fast or not at all.
		const iters = 50000
		for i := 0; i < iters; i++ {
			if i%1000 == 999 {
				// give scheduler a chance
			}
		}
		close(deadline)
	}()
	<-deadline
	close(stop)
	wg.Wait()
}

// placeQuiet calls processPlaceOrder ignoring the response (used in race goroutines
// that shouldn't fail the test for benign errors like "too many open orders").
func placeQuiet(e *Extension, req types.PlaceOrderRequest) error {
	body, _ := json.Marshal(req)
	ar := e.processPlaceOrder(teetypes.Action{}, &instruction.DataFixed{}, body)
	if ar.Status == 1 {
		return nil
	}
	return fmt.Errorf("status=%d log=%s", ar.Status, ar.Log)
}

// ---------------------------------------------------------------------------
// C2 — race between order placement and eviction; tracking can become inconsistent
// ---------------------------------------------------------------------------

// Spam concurrent PLACE_ORDERs that trigger eviction. After things settle, every
// e.orders[id] entry MUST correspond to an order still on the book.
func TestC2_PlaceVsEvictTrackingConsistency(t *testing.T) {
	base := common.HexToAddress(testBaseHex)
	quote := common.HexToAddress(testQuoteHex)
	e := newTestExtension(testPair, base, quote)

	prev := MaxLevelsPerSide
	MaxLevelsPerSide = 10
	t.Cleanup(func() { MaxLevelsPerSide = prev })

	const G = 32
	const PerG = 30

	// Provision enough balance per user.
	for i := 0; i < G; i++ {
		if err := e.balances.Deposit(mkAddr(i), base, 1_000_000); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(G)
	for g := 0; g < G; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < PerG; i++ {
				_ = placeQuiet(e, types.PlaceOrderRequest{
					Sender:   mkAddr(g),
					Pair:     testPair,
					Side:     orderbook.Sell,
					Type:     orderbook.Limit,
					Price:    uint64(100 + (g*PerG+i)%200),
					Quantity: 1,
				})
			}
		}(g)
	}
	wg.Wait()

	// Invariant: every tracked id should be on the book.
	e.mu.RLock()
	defer e.mu.RUnlock()
	stale := 0
	for id, p := range e.orders {
		ob := e.orderbooks[p]
		if ob == nil || ob.GetOrder(id) == nil {
			stale++
		}
	}
	if stale > 0 {
		t.Errorf("CONFIRMED C2: %d entries in e.orders not on book (place/evict race leak)", stale)
	}

	// Cross-check: every userOrders entry should map to a tracked id.
	mismatched := 0
	for u, ids := range e.userOrders {
		for _, id := range ids {
			if _, ok := e.orders[id]; !ok {
				mismatched++
				_ = u
			}
		}
	}
	if mismatched > 0 {
		t.Errorf("CONFIRMED C2: %d userOrders entries missing from e.orders", mismatched)
	}
}
