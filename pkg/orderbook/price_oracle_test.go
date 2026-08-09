package orderbook

import "testing"

// fakeOracle is a test double for PriceOracle with a fixed band, or no
// answer at all when ok is false (simulating a stale/unavailable feed).
type fakeOracle struct {
	low, high uint64
	ok        bool
}

func (f fakeOracle) PriceBand(pair string) (uint64, uint64, bool) {
	return f.low, f.high, f.ok
}

func TestMatch_RejectedOutsideBand(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")
	// Resting ask at 100 is above the oracle's band [80, 95] — should not fill.
	ob.SetPriceOracle(fakeOracle{low: 80, high: 95, ok: true})

	sell := &Order{ID: "1", Owner: "alice", Pair: "FXRP/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 10}
	if _, err := ob.PlaceLimitOrder(sell); err != nil {
		t.Fatal(err)
	}

	buy := &Order{ID: "2", Owner: "bob", Pair: "FXRP/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}
	matches, err := ob.PlaceLimitOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches (price outside band), got %d", len(matches))
	}
	// Both orders should now be resting, since the in-band check blocked the fill.
	bids, asks := ob.Depth()
	if len(bids) != 1 || len(asks) != 1 {
		t.Fatalf("expected 1 resting bid and 1 resting ask, got bids=%d asks=%d", len(bids), len(asks))
	}
}

func TestMatch_AcceptedInsideBand(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")
	ob.SetPriceOracle(fakeOracle{low: 90, high: 110, ok: true})

	sell := &Order{ID: "1", Owner: "alice", Pair: "FXRP/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 10}
	ob.PlaceLimitOrder(sell)

	buy := &Order{ID: "2", Owner: "bob", Pair: "FXRP/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}
	matches, err := ob.PlaceLimitOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (price inside band), got %d", len(matches))
	}
	if matches[0].Price != 100 || matches[0].Quantity != 10 {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
}

func TestMatch_UnavailableOracleFailsOpen(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")
	// ok=false simulates a stale/unreachable feed — the documented behavior
	// is fail-open (don't block trading on an oracle outage).
	ob.SetPriceOracle(fakeOracle{ok: false})

	sell := &Order{ID: "1", Owner: "alice", Pair: "FXRP/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 10}
	ob.PlaceLimitOrder(sell)

	buy := &Order{ID: "2", Owner: "bob", Pair: "FXRP/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}
	matches, err := ob.PlaceLimitOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (oracle unavailable => fail-open), got %d", len(matches))
	}
}

func TestMatch_NoOracleUnchangedBehavior(t *testing.T) {
	// No SetPriceOracle call at all — mirrors every pre-existing test in
	// orderbook_test.go. Confirms the base fce-orderbook behavior is
	// untouched when the oracle feature isn't opted into.
	ob := NewOrderBook("FLR/USDT")

	sell := &Order{ID: "1", Owner: "alice", Pair: "FLR/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 10}
	ob.PlaceLimitOrder(sell)

	buy := &Order{ID: "2", Owner: "bob", Pair: "FLR/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}
	matches, err := ob.PlaceLimitOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
}

func TestMatch_PartialBandStopsSweep(t *testing.T) {
	// Two resting asks: 100 (in band) and 200 (out of band). A buy sweeping
	// the book should fill against the 100 level and then stop — not skip
	// over the out-of-band level to find more liquidity, since price-time
	// priority means anything past an out-of-band price is worse, not just
	// differently priced.
	ob := NewOrderBook("FXRP/USDT")
	ob.SetPriceOracle(fakeOracle{low: 50, high: 150, ok: true})

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Pair: "FXRP/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 5})
	ob.PlaceLimitOrder(&Order{ID: "2", Owner: "carol", Pair: "FXRP/USDT", Side: Sell, Type: Limit, Price: 200, Quantity: 5})

	buy := &Order{ID: "3", Owner: "bob", Pair: "FXRP/USDT", Side: Buy, Type: Limit, Price: 200, Quantity: 10}
	matches, err := ob.PlaceLimitOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match against the in-band level only, got %d", len(matches))
	}
	if matches[0].Quantity != 5 || matches[0].Price != 100 {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
	if buy.Remaining != 5 {
		t.Fatalf("expected 5 remaining (blocked by out-of-band level), got %d", buy.Remaining)
	}
}
