package orderbook

import (
	"sync"
	"testing"
)

func TestPlaceLimitBuy_NoMatch(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	order := &Order{ID: "1", Owner: "alice", Pair: "FLR/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}

	matches, err := ob.PlaceLimitOrder(order)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(matches))
	}
	if order.Remaining != 10 {
		t.Fatalf("expected remaining 10, got %d", order.Remaining)
	}

	bids, asks := ob.Depth()
	if len(bids) != 1 {
		t.Fatalf("expected 1 bid level, got %d", len(bids))
	}
	if len(asks) != 0 {
		t.Fatalf("expected 0 ask levels, got %d", len(asks))
	}
}

func TestPlaceLimitSell_NoMatch(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	order := &Order{ID: "1", Owner: "alice", Pair: "FLR/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 10}

	matches, err := ob.PlaceLimitOrder(order)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(matches))
	}

	bids, asks := ob.Depth()
	if len(bids) != 0 {
		t.Fatalf("expected 0 bid levels, got %d", len(bids))
	}
	if len(asks) != 1 {
		t.Fatalf("expected 1 ask level, got %d", len(asks))
	}
}

func TestLimitOrder_ExactMatch(t *testing.T) {
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
	m := matches[0]
	if m.Price != 100 || m.Quantity != 10 {
		t.Fatalf("expected price=100 qty=10, got price=%d qty=%d", m.Price, m.Quantity)
	}
	if m.BuyOrderID != "2" || m.SellOrderID != "1" {
		t.Fatalf("expected buy=2 sell=1, got buy=%s sell=%s", m.BuyOrderID, m.SellOrderID)
	}

	bids, asks := ob.Depth()
	if len(bids) != 0 || len(asks) != 0 {
		t.Fatalf("expected empty book, got %d bids %d asks", len(bids), len(asks))
	}
}

func TestLimitOrder_PriceImprovement(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	// Ask at 95.
	sell := &Order{ID: "1", Owner: "alice", Pair: "FLR/USDT", Side: Sell, Type: Limit, Price: 95, Quantity: 10}
	ob.PlaceLimitOrder(sell)

	// Buy at 100 -- should execute at the resting price (95).
	buy := &Order{ID: "2", Owner: "bob", Pair: "FLR/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}
	matches, _ := ob.PlaceLimitOrder(buy)

	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Price != 95 {
		t.Fatalf("expected execution at resting price 95, got %d", matches[0].Price)
	}
}

func TestLimitOrder_PartialFill(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	sell := &Order{ID: "1", Owner: "alice", Pair: "FLR/USDT", Side: Sell, Type: Limit, Price: 100, Quantity: 5}
	ob.PlaceLimitOrder(sell)

	buy := &Order{ID: "2", Owner: "bob", Pair: "FLR/USDT", Side: Buy, Type: Limit, Price: 100, Quantity: 10}
	matches, _ := ob.PlaceLimitOrder(buy)

	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Quantity != 5 {
		t.Fatalf("expected fill qty 5, got %d", matches[0].Quantity)
	}
	if buy.Remaining != 5 {
		t.Fatalf("expected buy remaining 5, got %d", buy.Remaining)
	}

	// Remainder should rest on the book.
	bids, _ := ob.Depth()
	if len(bids) != 1 {
		t.Fatalf("expected 1 bid level, got %d", len(bids))
	}
	if bids[0].Quantity != 5 {
		t.Fatalf("expected resting qty 5, got %d", bids[0].Quantity)
	}
}

func TestLimitOrder_MultiLevelSweep(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	// Place asks at 100 and 101.
	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Sell, Price: 100, Quantity: 5})
	ob.PlaceLimitOrder(&Order{ID: "2", Owner: "alice", Side: Sell, Price: 101, Quantity: 5})

	// Buy 8 at 101 -- should sweep both levels.
	buy := &Order{ID: "3", Owner: "bob", Side: Buy, Price: 101, Quantity: 8}
	matches, _ := ob.PlaceLimitOrder(buy)

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Price != 100 || matches[0].Quantity != 5 {
		t.Fatalf("expected first match price=100 qty=5, got price=%d qty=%d", matches[0].Price, matches[0].Quantity)
	}
	if matches[1].Price != 101 || matches[1].Quantity != 3 {
		t.Fatalf("expected second match price=101 qty=3, got price=%d qty=%d", matches[1].Price, matches[1].Quantity)
	}
}

func TestMarketOrder_FullFill(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Sell, Price: 100, Quantity: 10})

	buy := &Order{ID: "2", Owner: "bob", Side: Buy, Type: Market, Quantity: 10}
	matches, err := ob.PlaceMarketOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Quantity != 10 {
		t.Fatalf("expected 1 match qty=10, got %d matches", len(matches))
	}
}

func TestMarketOrder_NoLiquidity(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	buy := &Order{ID: "1", Owner: "bob", Side: Buy, Type: Market, Quantity: 10}
	_, err := ob.PlaceMarketOrder(buy)
	if err != ErrNoLiquidity {
		t.Fatalf("expected ErrNoLiquidity, got %v", err)
	}
}

func TestMarketOrder_PartialFill(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Sell, Price: 100, Quantity: 5})

	buy := &Order{ID: "2", Owner: "bob", Side: Buy, Type: Market, Quantity: 10}
	matches, err := ob.PlaceMarketOrder(buy)
	if err != nil {
		t.Fatal(err)
	}
	// Market order fills 5, discards remainder.
	if len(matches) != 1 || matches[0].Quantity != 5 {
		t.Fatalf("expected 1 match qty=5, got %d matches", len(matches))
	}
}

func TestCancelOrder_Success(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	order := &Order{ID: "1", Owner: "alice", Side: Buy, Price: 100, Quantity: 10}
	ob.PlaceLimitOrder(order)

	cancelled, err := ob.CancelOrder("1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.ID != "1" || cancelled.Remaining != 10 {
		t.Fatalf("expected cancelled order 1 remaining 10, got %s remaining %d", cancelled.ID, cancelled.Remaining)
	}

	bids, _ := ob.Depth()
	if len(bids) != 0 {
		t.Fatalf("expected empty bids, got %d", len(bids))
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	_, err := ob.CancelOrder("nonexistent", "alice")
	if err != ErrOrderNotFound {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestCancelOrder_NotOwner(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	order := &Order{ID: "1", Owner: "alice", Side: Buy, Price: 100, Quantity: 10}
	ob.PlaceLimitOrder(order)

	_, err := ob.CancelOrder("1", "bob")
	if err != ErrNotOwner {
		t.Fatalf("expected ErrNotOwner, got %v", err)
	}
}

func TestDepth(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Buy, Price: 99, Quantity: 5})
	ob.PlaceLimitOrder(&Order{ID: "2", Owner: "alice", Side: Buy, Price: 99, Quantity: 3})
	ob.PlaceLimitOrder(&Order{ID: "3", Owner: "alice", Side: Buy, Price: 100, Quantity: 10})
	ob.PlaceLimitOrder(&Order{ID: "4", Owner: "alice", Side: Sell, Price: 101, Quantity: 7})

	bids, asks := ob.Depth()

	// Bids: 100 first (descending), then 99.
	if len(bids) != 2 {
		t.Fatalf("expected 2 bid levels, got %d", len(bids))
	}
	if bids[0].Price != 100 || bids[0].Quantity != 10 || bids[0].OrderCount != 1 {
		t.Fatalf("unexpected bid level 0: %+v", bids[0])
	}
	if bids[1].Price != 99 || bids[1].Quantity != 8 || bids[1].OrderCount != 2 {
		t.Fatalf("unexpected bid level 1: %+v", bids[1])
	}

	if len(asks) != 1 {
		t.Fatalf("expected 1 ask level, got %d", len(asks))
	}
	if asks[0].Price != 101 || asks[0].Quantity != 7 {
		t.Fatalf("unexpected ask level: %+v", asks[0])
	}
}

func TestPriceTimePriority(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	// Two sells at the same price -- first should fill first.
	ob.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Sell, Price: 100, Quantity: 5})
	ob.PlaceLimitOrder(&Order{ID: "2", Owner: "charlie", Side: Sell, Price: 100, Quantity: 5})

	buy := &Order{ID: "3", Owner: "bob", Side: Buy, Price: 100, Quantity: 5}
	matches, _ := ob.PlaceLimitOrder(buy)

	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SellOrderID != "1" {
		t.Fatalf("expected first sell (ID=1) to fill first, got %s", matches[0].SellOrderID)
	}
}

func TestMultiplePairs(t *testing.T) {
	ob1 := NewOrderBook("FLR/USDT")
	ob2 := NewOrderBook("BTC/USDT")

	ob1.PlaceLimitOrder(&Order{ID: "1", Owner: "alice", Side: Sell, Price: 100, Quantity: 10})
	ob2.PlaceLimitOrder(&Order{ID: "2", Owner: "alice", Side: Sell, Price: 50000, Quantity: 1})

	// Buy on FLR/USDT shouldn't affect BTC/USDT.
	buy := &Order{ID: "3", Owner: "bob", Side: Buy, Price: 100, Quantity: 10}
	matches, _ := ob1.PlaceLimitOrder(buy)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	_, asks := ob2.Depth()
	if len(asks) != 1 {
		t.Fatalf("BTC/USDT should still have 1 ask level, got %d", len(asks))
	}
}

func TestConcurrency(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")

	var wg sync.WaitGroup
	// Place many limit orders concurrently.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			order := &Order{
				ID:       string(rune('A'+id%26)) + string(rune('0'+id/26)),
				Owner:    "user",
				Side:     Buy,
				Price:    uint64(100 + id%10),
				Quantity: 1,
			}
			ob.PlaceLimitOrder(order)
		}(i)
	}
	wg.Wait()

	bids, _ := ob.Depth()
	total := 0
	for _, l := range bids {
		total += int(l.Quantity)
	}
	if total != 100 {
		t.Fatalf("expected total quantity 100, got %d", total)
	}
}

func TestValidation_InvalidSide(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	order := &Order{ID: "1", Owner: "alice", Side: "invalid", Price: 100, Quantity: 10}
	_, err := ob.PlaceLimitOrder(order)
	if err != ErrInvalidSide {
		t.Fatalf("expected ErrInvalidSide, got %v", err)
	}
}

func TestValidation_ZeroPrice(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	order := &Order{ID: "1", Owner: "alice", Side: Buy, Price: 0, Quantity: 10}
	_, err := ob.PlaceLimitOrder(order)
	if err != ErrInvalidPrice {
		t.Fatalf("expected ErrInvalidPrice, got %v", err)
	}
}

func TestValidation_ZeroQuantity(t *testing.T) {
	ob := NewOrderBook("FLR/USDT")
	order := &Order{ID: "1", Owner: "alice", Side: Buy, Price: 100, Quantity: 0}
	_, err := ob.PlaceLimitOrder(order)
	if err != ErrInvalidQuantity {
		t.Fatalf("expected ErrInvalidQuantity, got %v", err)
	}
}
