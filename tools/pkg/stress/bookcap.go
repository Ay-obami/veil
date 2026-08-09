package stress

import "sync"

// BookCap enforces a per-side maximum on resting limit orders across an
// entire stress-test run (one soak script == one pair). When a successful
// place would push a side past Max, the oldest order in that side's FIFO is
// returned for the caller to cancel. Safe for concurrent use across the
// per-trader assignment goroutines.
//
// BookCap holds order IDs only; it doesn't know which orders have already
// filled or been cancelled by the persona's own cycle. Persona-driven cancels
// should call Remove so we don't later evict an entry that's no longer on
// the book.
type BookCap struct {
	mu   sync.Mutex
	max  int
	bids []queuedOrder
	asks []queuedOrder
}

type queuedOrder struct {
	orderID string
	trader  *Trader
}

// NewBookCap returns a cap with the given per-side maximum. max <= 0 disables
// the cap (Push always returns nil, Remove is a no-op).
func NewBookCap(max int) *BookCap {
	return &BookCap{max: max}
}

// Push records a newly-resting order. If the side's FIFO now exceeds Max, the
// front (oldest) entry is removed and returned so the caller can cancel it.
func (bc *BookCap) Push(side, orderID string, t *Trader) *queuedOrder {
	if bc == nil || bc.max <= 0 {
		return nil
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	q := &bc.bids
	if side == "sell" {
		q = &bc.asks
	}
	*q = append(*q, queuedOrder{orderID: orderID, trader: t})
	if len(*q) > bc.max {
		oldest := (*q)[0]
		*q = (*q)[1:]
		return &oldest
	}
	return nil
}

// Remove drops orderID from whichever side's FIFO holds it. Used after a
// persona-initiated cancel succeeds so we don't double-cancel later.
func (bc *BookCap) Remove(orderID string) {
	if bc == nil || bc.max <= 0 {
		return
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.bids = removeID(bc.bids, orderID)
	bc.asks = removeID(bc.asks, orderID)
}

func removeID(q []queuedOrder, id string) []queuedOrder {
	for i := range q {
		if q[i].orderID == id {
			return append(q[:i], q[i+1:]...)
		}
	}
	return q
}
