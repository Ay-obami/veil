package orderbook

import (
	"fmt"
	"testing"
)

func placeRestingSell(t *testing.T, ob *OrderBook, id string, price uint64) {
	t.Helper()
	o := &Order{ID: id, Owner: "alice", Pair: "FLR/USDT", Side: Sell, Type: Limit, Price: price, Quantity: 5}
	if matches, err := ob.PlaceLimitOrder(o); err != nil {
		t.Fatalf("place sell %s: %v", id, err)
	} else if len(matches) != 0 {
		t.Fatalf("place sell %s should not match: got %d matches", id, len(matches))
	}
}

func placeRestingBuy(t *testing.T, ob *OrderBook, id string, price uint64) {
	t.Helper()
	o := &Order{ID: id, Owner: "alice", Pair: "FLR/USDT", Side: Buy, Type: Limit, Price: price, Quantity: 5}
	if matches, err := ob.PlaceLimitOrder(o); err != nil {
		t.Fatalf("place buy %s: %v", id, err)
	} else if len(matches) != 0 {
		t.Fatalf("place buy %s should not match: got %d matches", id, len(matches))
	}
}

func TestEvictExcessLevels_AsksEvictHighestPrice(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	// Asks: best (lowest) = 100, worst (highest) = 104.
	for i, price := range []uint64{100, 101, 102, 103, 104} {
		placeRestingSell(t, ob, fmt.Sprintf("a%d", i), price)
	}

	evicted := ob.EvictExcessLevels(3)
	if len(evicted) != 2 {
		t.Fatalf("evicted: got %d, want 2", len(evicted))
	}
	got := []uint64{evicted[0].Price, evicted[1].Price}
	if got[0] != 104 || got[1] != 103 {
		t.Fatalf("evicted prices: got %v, want [104 103]", got)
	}

	_, asks := ob.Depth()
	if len(asks) != 3 {
		t.Fatalf("remaining ask levels: got %d, want 3", len(asks))
	}
	for _, lvl := range asks {
		if lvl.Price >= 103 {
			t.Fatalf("worst-priced level should be evicted, got %d still present", lvl.Price)
		}
	}
}

func TestEvictExcessLevels_BidsEvictLowestPrice(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	// Bids: best (highest) = 104, worst (lowest) = 100.
	for i, price := range []uint64{100, 101, 102, 103, 104} {
		placeRestingBuy(t, ob, fmt.Sprintf("b%d", i), price)
	}

	evicted := ob.EvictExcessLevels(3)
	if len(evicted) != 2 {
		t.Fatalf("evicted: got %d, want 2", len(evicted))
	}
	got := []uint64{evicted[0].Price, evicted[1].Price}
	if got[0] != 100 || got[1] != 101 {
		t.Fatalf("evicted prices: got %v, want [100 101]", got)
	}

	bids, _ := ob.Depth()
	if len(bids) != 3 {
		t.Fatalf("remaining bid levels: got %d, want 3", len(bids))
	}
	for _, lvl := range bids {
		if lvl.Price <= 101 {
			t.Fatalf("worst-priced level should be evicted, got %d still present", lvl.Price)
		}
	}
}

func TestEvictExcessLevels_NoOpWhenUnderCap(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	for i, price := range []uint64{100, 101} {
		placeRestingSell(t, ob, fmt.Sprintf("a%d", i), price)
	}
	if evicted := ob.EvictExcessLevels(5); len(evicted) != 0 {
		t.Fatalf("expected no eviction, got %d", len(evicted))
	}
	_, asks := ob.Depth()
	if len(asks) != 2 {
		t.Fatalf("levels should be unchanged: got %d, want 2", len(asks))
	}
}

func TestEvictExcessLevels_DrainsEntireQueueAtLevel(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	// Two orders at the same (worst) price level — eviction must drop both.
	placeRestingSell(t, ob, "a1", 100)
	placeRestingSell(t, ob, "a2", 105) // worst
	placeRestingSell(t, ob, "a3", 105) // worst, same level

	evicted := ob.EvictExcessLevels(1)
	if len(evicted) != 2 {
		t.Fatalf("evicted: got %d, want 2", len(evicted))
	}
	for _, o := range evicted {
		if o.Price != 105 {
			t.Fatalf("evicted price: got %d, want 105", o.Price)
		}
	}
	if got := ob.GetOrder("a2"); got != nil {
		t.Fatal("evicted order a2 should not be retrievable")
	}
	if got := ob.GetOrder("a3"); got != nil {
		t.Fatal("evicted order a3 should not be retrievable")
	}
	if got := ob.GetOrder("a1"); got == nil {
		t.Fatal("non-evicted order a1 should still be retrievable")
	}
}

func TestEvictExcessLevels_VolumeAndCountDrained(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	for i, price := range []uint64{100, 101, 102, 103} {
		placeRestingSell(t, ob, fmt.Sprintf("a%d", i), price)
	}
	evicted := ob.EvictExcessLevels(2)
	if len(evicted) != 2 {
		t.Fatalf("evicted: got %d, want 2", len(evicted))
	}
	if ob.asks.LevelCount() != 2 {
		t.Fatalf("levels: got %d, want 2", ob.asks.LevelCount())
	}
	if ob.asks.count != 2 {
		t.Fatalf("count: got %d, want 2", ob.asks.count)
	}
	// Each remaining order has Remaining=5; volume should be 10.
	if ob.asks.volume != 10 {
		t.Fatalf("volume: got %d, want 10", ob.asks.volume)
	}
	if len(ob.asks.orders) != 2 {
		t.Fatalf("orders map size: got %d, want 2", len(ob.asks.orders))
	}
}

func TestGetOrder_BothSides(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	placeRestingBuy(t, ob, "b1", 99)
	placeRestingSell(t, ob, "a1", 101)

	if got := ob.GetOrder("b1"); got == nil || got.Side != Buy {
		t.Fatalf("GetOrder(b1): got %v, want Buy order", got)
	}
	if got := ob.GetOrder("a1"); got == nil || got.Side != Sell {
		t.Fatalf("GetOrder(a1): got %v, want Sell order", got)
	}
	if got := ob.GetOrder("missing"); got != nil {
		t.Fatalf("GetOrder(missing): got %v, want nil", got)
	}

	// After cancel the order should not be retrievable.
	if _, err := ob.CancelOrder("b1", "alice"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := ob.GetOrder("b1"); got != nil {
		t.Fatal("GetOrder(b1) after cancel should be nil")
	}
}

func TestEvictExcessLevels_ZeroOrNegativeCapNoOp(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	placeRestingSell(t, ob, "a1", 100)
	if evicted := ob.EvictExcessLevels(0); len(evicted) != 0 {
		t.Fatalf("expected no eviction for cap=0, got %d", len(evicted))
	}
	if evicted := ob.EvictExcessLevels(-1); len(evicted) != 0 {
		t.Fatalf("expected no eviction for cap=-1, got %d", len(evicted))
	}
}
