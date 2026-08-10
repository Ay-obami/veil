package orderbook

import "testing"

// TestDepth_HidesFullyVerifiedLevel confirms a price level containing only
// SolvencyVerified orders doesn't appear in Depth() at all — not even as
// a zero-quantity entry, which would still leak that a hidden order
// exists at that exact price. See OrderSide.Depth()'s doc comment for the
// full reasoning; this is the actual mechanism behind Veil's dark-pool
// product pitch, previously entirely unimplemented despite
// Order.SolvencyVerified existing.
func TestDepth_HidesFullyVerifiedLevel(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Buy, Price: 100, Quantity: 10, SolvencyVerified: true})

	bids, _ := ob.Depth()
	if len(bids) != 0 {
		t.Fatalf("expected 0 bid levels (fully hidden), got %d: %+v", len(bids), bids)
	}
}

// TestDepth_MixedLevelShowsOnlyPublicPortion confirms a price level with
// both a verified and a non-verified order shows only the non-verified
// order's contribution — the level's existence is already public (from
// the visible order), but the hidden order's size stays hidden rather
// than inflating the displayed quantity.
func TestDepth_MixedLevelShowsOnlyPublicPortion(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Buy, Price: 100, Quantity: 10, SolvencyVerified: false})
	ob.PlaceLimitOrder(&Order{ID: "2", Owner: "bob", Side: Buy, Price: 100, Quantity: 1000, SolvencyVerified: true})

	bids, _ := ob.Depth()
	if len(bids) != 1 {
		t.Fatalf("expected 1 bid level, got %d", len(bids))
	}
	if bids[0].Quantity != 10 {
		t.Fatalf("expected displayed quantity 10 (public order only, hidden order's 1000 excluded), got %d", bids[0].Quantity)
	}
	if bids[0].OrderCount != 1 {
		t.Fatalf("expected displayed order count 1 (hidden order excluded), got %d", bids[0].OrderCount)
	}
}

// TestDepth_VerifiedOrdersStillMatchNormally is the property that matters
// most: hiding an order from Depth() must NOT hide it from the matching
// engine. A verified order must still fill exactly like a normal one —
// Depth() is a reporting-only exclusion (see OrderSide.Depth()'s doc
// comment on why matchBuy/matchSell don't go through Depth() at all).
func TestDepth_VerifiedOrdersStillMatchNormally(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")

	sell := &Order{ID: "1", Owner: "alice", Side: Sell, Price: 100, Quantity: 10, SolvencyVerified: true}
	ob.PlaceLimitOrder(sell)

	// Confirm it's genuinely invisible in Depth() first.
	_, asks := ob.Depth()
	if len(asks) != 0 {
		t.Fatalf("expected the verified sell to be hidden from Depth(), got %d ask levels", len(asks))
	}

	// But a buy at the same price must still match against it.
	buy := &Order{ID: "2", Owner: "bob", Side: Buy, Price: 100, Quantity: 10}
	matches, err := ob.PlaceLimitOrder(buy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected the hidden order to still match normally, got %d matches", len(matches))
	}
	if matches[0].SellOrderID != "1" || matches[0].Quantity != 10 {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
}

// TestDepth_AllPublicUnaffected is a regression guard: with no verified
// orders at all, Depth()'s output must be byte-for-byte what it was
// before this feature existed. Mirrors the pre-existing TestDepth almost
// exactly, on purpose.
func TestDepth_AllPublicUnaffected(t *testing.T) {
	ob := NewOrderBook("FXRP/USDT")

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Buy, Price: 99, Quantity: 5})
	ob.PlaceLimitOrder(&Order{ID: "2", Owner: "alice", Side: Buy, Price: 99, Quantity: 3})
	ob.PlaceLimitOrder(&Order{ID: "3", Owner: "alice", Side: Buy, Price: 100, Quantity: 10})

	bids, _ := ob.Depth()
	if len(bids) != 2 {
		t.Fatalf("expected 2 bid levels, got %d", len(bids))
	}
	if bids[0].Price != 100 || bids[0].Quantity != 10 || bids[0].OrderCount != 1 {
		t.Fatalf("unexpected bid level 0: %+v", bids[0])
	}
	if bids[1].Price != 99 || bids[1].Quantity != 8 || bids[1].OrderCount != 2 {
		t.Fatalf("unexpected bid level 1: %+v", bids[1])
	}
}
