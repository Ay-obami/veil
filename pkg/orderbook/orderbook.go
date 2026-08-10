package orderbook

import (
	"container/list"
	"sync"
	"time"
)

// PriceOracle supplies the acceptable clearing-price band for a trading pair,
// expressed in the same raw uint64 tick units as Order.Price (see pricePrecision
// in internal/extension/handlers.go). A match whose price falls outside
// [low, high] is rejected before it executes.
//
// This is the hook a Flare FTSO-backed implementation plugs into (see
// pkg/oracle/ftso.go) — the matching engine itself stays oracle-agnostic so it
// can still be unit-tested with a fake, and so the base fce-orderbook behavior
// is unchanged when no oracle is set (nil PriceOracle disables the check).
type PriceOracle interface {
	// PriceBand returns the current [low, high] band for pair. ok=false means
	// no band is available (e.g. feed stale or pair not tracked) and the
	// caller should treat the check as inconclusive, not as "no bound."
	PriceBand(pair string) (low, high uint64, ok bool)
}

// OrderBook is a price-time-priority matching engine for a single trading pair.
type OrderBook struct {
	mu     sync.RWMutex
	pair   string
	bids   *OrderSide  // descending: highest bid first
	asks   *OrderSide  // ascending: lowest ask first
	oracle PriceOracle // optional; nil disables the price-band check
}

// NewOrderBook creates an empty OrderBook for the given trading pair.
// No price oracle is attached — use SetPriceOracle to enable FTSO-band
// enforcement. Existing callers (including tests) are unaffected.
func NewOrderBook(pair string) *OrderBook {
	return &OrderBook{
		pair: pair,
		bids: newOrderSide(true),  // descending
		asks: newOrderSide(false), // ascending
	}
}

// SetPriceOracle attaches (or replaces) the oracle used to bound match prices.
// Passing nil disables the check. Call during setup, not concurrently with
// in-flight matching.
func (ob *OrderBook) SetPriceOracle(oracle PriceOracle) {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	ob.oracle = oracle
}

// priceInBand reports whether price is within the oracle's current band for
// ob.pair. If no oracle is attached, or the oracle can't answer right now,
// priceInBand returns true — the check is opt-in and fail-open, so a feed
// outage degrades to "no band enforcement" rather than halting the book.
// (Trade-off worth calling out in the submission writeup: fail-open was
// chosen so a temporary FTSO hiccup can't freeze trading; a production
// version would likely want this configurable, or fail-closed with alerting.)
func (ob *OrderBook) priceInBand(price uint64) bool {
	if ob.oracle == nil {
		return true
	}
	low, high, ok := ob.oracle.PriceBand(ob.pair)
	if !ok {
		return true
	}
	return price >= low && price <= high
}

// PlaceLimitOrder validates and processes a limit order.
// The order is matched against the opposite side; any unfilled remainder rests on the book.
func (ob *OrderBook) PlaceLimitOrder(order *Order) ([]Match, error) {
	if err := validateOrder(order, false); err != nil {
		return nil, err
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	order.Remaining = order.Quantity
	var matches []Match

	switch order.Side {
	case Buy:
		matches = ob.matchBuy(order, order.Price)
		if order.Remaining > 0 {
			ob.bids.Add(order)
		}
	case Sell:
		matches = ob.matchSell(order, order.Price)
		if order.Remaining > 0 {
			ob.asks.Add(order)
		}
	}

	return matches, nil
}

// PlaceMarketOrder validates and processes a market order.
// The remainder is discarded after matching; ErrNoLiquidity is returned if nothing matched.
func (ob *OrderBook) PlaceMarketOrder(order *Order) ([]Match, error) {
	if err := validateOrder(order, true); err != nil {
		return nil, err
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	order.Remaining = order.Quantity
	var matches []Match

	switch order.Side {
	case Buy:
		matches = ob.matchBuy(order, 0) // price=0 means no price limit: match any ask
	case Sell:
		matches = ob.matchSell(order, 0) // price=0 means no price limit: match any bid
	}

	if len(matches) == 0 {
		return nil, ErrNoLiquidity
	}

	return matches, nil
}

// CancelOrder removes an order from the book after verifying the caller is the owner.
func (ob *OrderBook) CancelOrder(orderID, owner string) (*Order, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	if ob.bids.Has(orderID) {
		order := ob.bids.Remove(orderID)
		if order.Owner != owner {
			// Put it back — ownership check failed.
			ob.bids.Add(order)
			return nil, ErrNotOwner
		}
		return order, nil
	}

	if ob.asks.Has(orderID) {
		order := ob.asks.Remove(orderID)
		if order.Owner != owner {
			ob.asks.Add(order)
			return nil, ErrNotOwner
		}
		return order, nil
	}

	return nil, ErrOrderNotFound
}

// Depth returns a snapshot of aggregated price levels for both sides.
func (ob *OrderBook) Depth() (bids, asks []PriceLevel) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.bids.Depth(), ob.asks.Depth()
}

// EvictExcessLevels trims each side down to at most maxLevelsPerSide price levels
// by removing the worst-priced levels first. Returns all evicted orders so the
// caller can refund holds and clean up tracking.
func (ob *OrderBook) EvictExcessLevels(maxLevelsPerSide int) []*Order {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	if maxLevelsPerSide <= 0 {
		return nil
	}

	var evicted []*Order
	for ob.bids.LevelCount() > maxLevelsPerSide {
		evicted = append(evicted, ob.bids.EvictWorstLevel()...)
	}
	for ob.asks.LevelCount() > maxLevelsPerSide {
		evicted = append(evicted, ob.asks.EvictWorstLevel()...)
	}
	return evicted
}

// GetOrder returns a pointer to the resting order with the given ID, on either side.
// Returns nil if the order is not currently resting.
func (ob *OrderBook) GetOrder(orderID string) *Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	if o := ob.bids.GetOrder(orderID); o != nil {
		return o
	}
	return ob.asks.GetOrder(orderID)
}

// matchBuy sweeps asks for a buy order.
// limitPrice == 0 means no price limit (market order).
func (ob *OrderBook) matchBuy(order *Order, limitPrice uint64) []Match {
	var matches []Match

	for order.Remaining > 0 {
		bestPrice, queue, ok := ob.asks.BestPrice()
		if !ok {
			break
		}
		// For limit buy: stop if the best ask is above our limit.
		if limitPrice > 0 && bestPrice > limitPrice {
			break
		}
		// Reject a clearing price outside the oracle's current band. This is
		// deliberately a "stop sweeping" break, not a skip-and-continue: the
		// book is price-time priority, so if the best available price is
		// out of band, worse prices further down the book are out of band
		// too. The order rests (or the market order returns ErrNoLiquidity)
		// rather than filling at a manipulated or stale price.
		if !ob.priceInBand(bestPrice) {
			break
		}

		matches = ob.fillFromQueue(order, queue, bestPrice, matches)

		// Remove empty price level.
		if queue.Len() == 0 {
			ob.asks.priceLevels.Remove(bestPrice)
		}
	}

	return matches
}

// matchSell sweeps bids for a sell order.
// limitPrice == 0 means no price limit (market order).
func (ob *OrderBook) matchSell(order *Order, limitPrice uint64) []Match {
	var matches []Match

	for order.Remaining > 0 {
		bestPrice, queue, ok := ob.bids.BestPrice()
		if !ok {
			break
		}
		// For limit sell: stop if the best bid is below our limit.
		if limitPrice > 0 && bestPrice < limitPrice {
			break
		}
		// See matchBuy: reject a clearing price outside the oracle's band.
		if !ob.priceInBand(bestPrice) {
			break
		}

		matches = ob.fillFromQueue(order, queue, bestPrice, matches)

		// Remove empty price level.
		if queue.Len() == 0 {
			ob.bids.priceLevels.Remove(bestPrice)
		}
	}

	return matches
}

// fillFromQueue fills the incoming order against a single price-level queue.
// Execution price is always the resting order's price.
func (ob *OrderBook) fillFromQueue(
	incoming *Order,
	queue *list.List,
	restingPrice uint64,
	matches []Match,
) []Match {
	for incoming.Remaining > 0 && queue.Len() > 0 {
		front := queue.Front()
		resting := front.Value.(*Order)

		fillQty := min64(incoming.Remaining, resting.Remaining)
		now := time.Now().UnixNano()

		var m Match
		if incoming.Side == Buy {
			m = Match{
				BuyOrderID:  incoming.ID,
				SellOrderID: resting.ID,
				BuyOwner:    incoming.Owner,
				SellOwner:   resting.Owner,
				Pair:        ob.pair,
				Price:       restingPrice,
				Quantity:    fillQty,
				Timestamp:   now,
			}
			// Remove the resting order from the side's order map accounting.
			ob.asks.volume -= fillQty
		} else {
			m = Match{
				BuyOrderID:  resting.ID,
				SellOrderID: incoming.ID,
				BuyOwner:    resting.Owner,
				SellOwner:   incoming.Owner,
				Pair:        ob.pair,
				Price:       restingPrice,
				Quantity:    fillQty,
				Timestamp:   now,
			}
			ob.bids.volume -= fillQty
		}

		matches = append(matches, m)
		incoming.Remaining -= fillQty
		resting.Remaining -= fillQty

		if resting.Remaining == 0 {
			// Remove fully filled resting order.
			queue.Remove(front)
			if incoming.Side == Buy {
				delete(ob.asks.orders, resting.ID)
				ob.asks.count--
			} else {
				delete(ob.bids.orders, resting.ID)
				ob.bids.count--
			}
		}
	}

	return matches
}

// validateOrder checks common invariants.
// isMarket=true skips the price > 0 check.
func validateOrder(order *Order, isMarket bool) error {
	if order.Side != Buy && order.Side != Sell {
		return ErrInvalidSide
	}
	if !isMarket && order.Price == 0 {
		return ErrInvalidPrice
	}
	if order.Quantity == 0 {
		return ErrInvalidQuantity
	}
	return nil
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
