package extension

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"strings"
	"time"

	"veil/internal/config"
	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// pricePrecision is the multiplier applied to human-readable prices before they
// are stored in the TEE. This allows 6 decimal places of price precision
// (0.000001) — enough headroom for sub-cent assets (e.g. FLR ~$0.008) to have
// a meaningful spread, since 1 raw tick = $0.000001. price*quantity is divided
// back by this factor wherever a quote-token amount is computed.
// The frontend (frontend/src/lib/price.ts PRICE_PRECISION) and stress test
// (tools/pkg/stress/scaling.go PricePrecision) must use this same constant.
const pricePrecision = 1_000_000

// appendBounded appends v to s and trims the head so len(s) <= maxLen.
func appendBounded[T any](s []T, v T, maxLen int) []T {
	s = append(s, v)
	if maxLen > 0 && len(s) > maxLen {
		s = s[len(s)-maxLen:]
	}
	return s
}

// safeMulDiv returns (a*b)/c. Returns ok=false if a*b overflows uint64.
// Callers should treat ok=false as a hard failure; the operands cannot be
// processed without losing precision or wrapping.
func safeMulDiv(a, b, c uint64) (uint64, bool) {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return 0, false
	}
	if c == 0 {
		return 0, false
	}
	return lo / c, true
}

// mulPrice computes (quantity * price) / pricePrecision with overflow
// detection. Panics on overflow because every caller is downstream of
// calculateHold, which vets quantity*price at order-placement time via
// safeMulDiv. Reaching mulPrice with overflowing operands therefore indicates
// an invariant violation (a resting order whose Q*P now wraps uint64), which
// is far worse to silently absorb than to crash on. The previous behavior was
// to use plain `*` which silently wrapped and produced wrong quote amounts.
func mulPrice(quantity, price uint64) uint64 {
	r, ok := safeMulDiv(quantity, price, pricePrecision)
	if !ok {
		panic(fmt.Sprintf("price math overflow: quantity=%d * price=%d / %d wraps uint64 (calculateHold should have rejected at placement)",
			quantity, price, pricePrecision))
	}
	return r
}

// processPlaceOrder handles PLACE_ORDER direct instructions.
//
// Lock policy: the entire critical section — per-user cap check, hold,
// orderbook insert, match processing, residual release, tracking, and
// eviction — runs under e.mu.Lock. This makes place + register + evict
// atomic w.r.t. concurrent place_order/cancel/eviction (see C2).
//
// Lock order: e.mu  →  ob.mu (inside ob.* calls)  →  balances.mu (inside e.balances.*).
// Nothing in pkg/orderbook or pkg/balance ever calls back into Extension,
// so this ordering cannot deadlock.
func (e *Extension) processPlaceOrder(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.PlaceOrderRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
	}

	user := strings.ToLower(req.Sender)
	if user == "" {
		return buildResult(action, df, nil, 0, fmt.Errorf("sender address is required"))
	}

	pairConfig, ok := e.pairs[req.Pair]
	if !ok {
		return buildResult(action, df, nil, 0, fmt.Errorf("unknown trading pair: %s", req.Pair))
	}
	ob, ok := e.orderbooks[req.Pair]
	if !ok {
		return buildResult(action, df, nil, 0, fmt.Errorf("orderbook not found for pair: %s", req.Pair))
	}

	// Stateless solvency-proof check — see verifySolvencyProofForOrder's
	// comment on why nonce replay checking is deliberately NOT done here.
	// expectedToken mirrors calculateHold's own side-based determination
	// (pair.QuoteToken for Buy, pair.BaseToken for Sell) — computed here
	// independently because calculateHold needs a fully-constructed
	// *orderbook.Order, including an ID that doesn't exist yet at this
	// point (order IDs are assigned after this check, not before — see
	// the SolvencyProof field's doc comment in pkg/types/types.go for the
	// same class of sequencing issue on the client side).
	expectedToken := pairConfig.QuoteToken
	if req.Side == orderbook.Sell {
		expectedToken = pairConfig.BaseToken
	}
	solvencyVerified, err := e.verifySolvencyProofForOrder(req, expectedToken)
	if err != nil {
		return buildResult(action, df, nil, 0, err)
	}

	order := &orderbook.Order{
		ID:               e.nextOrderID(),
		Owner:            user,
		Pair:             req.Pair,
		Side:             req.Side,
		Type:             req.Type,
		Price:            req.Price,
		Quantity:         req.Quantity,
		Timestamp:        time.Now().UnixNano(),
		SolvencyVerified: solvencyVerified,
	}

	holdToken, holdAmount, err := e.calculateHold(user, pairConfig, order)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("calculating hold: %w", err))
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Solvency-proof nonce replay check. Deliberately inside the same
	// locked section as order registration below (see this function's top
	// comment on lock policy) — consuming the nonce and registering the
	// order need to be atomic w.r.t. concurrent PLACE_ORDER calls, or two
	// requests racing on the same proof could both slip through.
	if solvencyVerified {
		nonce := solvencyNonceFromRequest(req)
		if _, used := e.usedSolvencyNonces[nonce]; used {
			return buildResult(action, df, nil, 0, fmt.Errorf("solvency proof nonce already used — a proof can only back one order"))
		}
		e.usedSolvencyNonces[nonce] = time.Now()
	}

	// Per-user open-order cap. Reject before holding funds.
	if len(e.userOrders[user]) >= MaxOrdersPerUser {
		return buildResult(action, df, nil, 0, fmt.Errorf("too many open orders (max %d)", MaxOrdersPerUser))
	}

	if err := e.balances.Hold(user, holdToken, holdAmount); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("insufficient balance: %w", err))
	}

	var matches []orderbook.Match
	switch req.Type {
	case orderbook.Limit:
		matches, err = ob.PlaceLimitOrder(order)
	case orderbook.Market:
		matches, err = ob.PlaceMarketOrder(order)
	default:
		_ = e.balances.Release(user, holdToken, holdAmount)
		return buildResult(action, df, nil, 0, fmt.Errorf("invalid order type: %s", req.Type))
	}

	if err != nil {
		_ = e.balances.Release(user, holdToken, holdAmount)
		return buildResult(action, df, nil, 0, fmt.Errorf("placing order: %w", err))
	}

	for _, m := range matches {
		e.processMatch(m, pairConfig)
	}

	// For market orders, release any unused held amount.
	if req.Type == orderbook.Market {
		filled := totalFilled(matches, order)
		if filled < holdAmount {
			_ = e.balances.Release(user, holdToken, holdAmount-filled)
		}
	}

	// For limit buys, the hold was sized at the limit price but transfers happen
	// at resting prices (≤ limit). Release the difference back to the buyer so it
	// doesn't get stuck in Held forever. Also covers division-precision dust
	// (Σ floor(q_i*p_i/N) ≤ floor(Q*P/N)).
	if req.Type == orderbook.Limit && order.Side == orderbook.Buy && len(matches) > 0 {
		var totalTransferred uint64
		for _, m := range matches {
			totalTransferred += mulPrice(m.Quantity, m.Price)
		}
		filledQty := order.Quantity - order.Remaining
		holdedForFilled := mulPrice(filledQty, order.Price)
		if holdedForFilled > totalTransferred {
			_ = e.balances.Release(user, holdToken, holdedForFilled-totalTransferred)
		}
	}

	status := "resting"
	if order.Remaining == 0 {
		status = "filled"
	} else if len(matches) > 0 {
		status = "partial"
	}
	respRemaining := order.Remaining

	// Track the order if it's resting.
	if order.Remaining > 0 {
		e.orders[order.ID] = req.Pair
		e.userOrders[user] = append(e.userOrders[user], order.ID)
	}
	e.history.orders[user] = appendBounded(e.history.orders[user], *order, MaxUserHistoryOrders)

	// Enforce per-side level cap. Held under e.mu so the evicted refund + tracking
	// cleanup is atomic w.r.t. any concurrent place/cancel.
	if order.Remaining > 0 {
		if evicted := ob.EvictExcessLevels(MaxLevelsPerSide); len(evicted) > 0 {
			e.handleEvictions(req.Pair, pairConfig, evicted)
		}
	}

	resp := types.PlaceOrderResponse{
		OrderID:   order.ID,
		Status:    status,
		Matches:   matches,
		Remaining: respRemaining,
	}
	data, _ := json.Marshal(resp)

	logger.Infof("order placed: %s %s %s %s price=%d qty=%d matches=%d remaining=%d",
		order.ID, req.Pair, req.Side, req.Type, req.Price, req.Quantity, len(matches), respRemaining)

	return buildResult(action, df, data, 1, nil)
}

// processCancelOrder handles CANCEL_ORDER direct instructions.
func (e *Extension) processCancelOrder(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.CancelOrderRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
	}

	user := strings.ToLower(req.Sender)
	if user == "" {
		return buildResult(action, df, nil, 0, fmt.Errorf("sender address is required"))
	}

	e.mu.Lock()
	pairName, ok := e.orders[req.OrderID]
	if !ok {
		e.mu.Unlock()
		return buildResult(action, df, nil, 0, fmt.Errorf("order not found: %s", req.OrderID))
	}

	ob, ok := e.orderbooks[pairName]
	if !ok {
		e.mu.Unlock()
		return buildResult(action, df, nil, 0, fmt.Errorf("orderbook not found for pair: %s", pairName))
	}
	e.mu.Unlock()

	cancelled, err := ob.CancelOrder(req.OrderID, user)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("cancelling order: %w", err))
	}

	pairConfig := e.pairs[pairName]
	releaseToken, releaseAmount := e.calculateRelease(pairConfig, cancelled)
	if releaseAmount > 0 {
		_ = e.balances.Release(user, releaseToken, releaseAmount)
	}

	e.mu.Lock()
	delete(e.orders, req.OrderID)
	e.removeUserOrder(user, req.OrderID)
	e.mu.Unlock()

	resp := types.CancelOrderResponse{
		OrderID:   cancelled.ID,
		Pair:      cancelled.Pair,
		Side:      string(cancelled.Side),
		Remaining: cancelled.Remaining,
	}
	data, _ := json.Marshal(resp)

	logger.Infof("order cancelled: %s pair=%s remaining=%d", cancelled.ID, pairName, cancelled.Remaining)

	return buildResult(action, df, data, 1, nil)
}

// processMatch handles a single match: transfers funds, records the trade, updates candles,
// and cleans up resting orders that were fully filled.
// Caller must hold e.mu.Lock().
func (e *Extension) processMatch(m orderbook.Match, pairConfig config.TradingPairConfig) {
	buyOwner := m.BuyOwner
	sellOwner := m.SellOwner

	quoteAmount := mulPrice(m.Quantity, m.Price)
	_ = e.balances.Transfer(buyOwner, sellOwner, pairConfig.QuoteToken, quoteAmount)
	_ = e.balances.Transfer(sellOwner, buyOwner, pairConfig.BaseToken, m.Quantity)

	// Per-pair recent-trades ring.
	if ring := e.matchesByPair[m.Pair]; ring != nil {
		ring.Push(m)
	}

	// Bounded per-user match history.
	if buyOwner != "" {
		e.history.matches[buyOwner] = appendBounded(e.history.matches[buyOwner], m, MaxUserHistoryMatches)
	}
	if sellOwner != "" && sellOwner != buyOwner {
		e.history.matches[sellOwner] = appendBounded(e.history.matches[sellOwner], m, MaxUserHistoryMatches)
	}

	// Roll the candle rings.
	e.updateCandles(m)

	// Clean up any resting order that was fully filled by this match.
	e.cleanupIfFilled(m.BuyOrderID, buyOwner, m.Pair)
	e.cleanupIfFilled(m.SellOrderID, sellOwner, m.Pair)
}

// updateCandles rolls each timeframe's ring for the pair.
// Caller must hold e.mu.Lock().
func (e *Extension) updateCandles(m orderbook.Match) {
	pair := e.candles[m.Pair]
	if pair == nil {
		return
	}
	secs := m.Seconds()
	for _, tf := range orderbook.Timeframes {
		ring := pair[tf]
		if ring == nil {
			continue
		}
		bucket := secs - (secs % int64(tf))
		last, ok := ring.Latest()
		if !ok || last.OpenTime != bucket {
			open := m.Price
			if ok {
				open = last.Close
			}
			high, low := open, open
			if m.Price > high {
				high = m.Price
			}
			if m.Price < low {
				low = m.Price
			}
			ring.Push(orderbook.Candle{
				OpenTime: bucket,
				Open:     open,
				High:     high,
				Low:      low,
				Close:    m.Price,
				Volume:   m.Quantity,
				Trades:   1,
			})
			continue
		}
		if m.Price > last.High {
			last.High = m.Price
		}
		if m.Price < last.Low {
			last.Low = m.Price
		}
		last.Close = m.Price
		last.Volume += m.Quantity
		last.Trades++
		ring.SetLatest(last)
	}
}

// cleanupIfFilled drops orderID from active tracking if it's no longer resting on the book.
// Caller must hold e.mu.Lock().
func (e *Extension) cleanupIfFilled(orderID, owner, pair string) {
	if _, tracked := e.orders[orderID]; !tracked {
		return
	}
	ob, ok := e.orderbooks[pair]
	if !ok {
		delete(e.orders, orderID)
		if owner != "" {
			e.removeUserOrder(owner, orderID)
		}
		return
	}
	if ob.GetOrder(orderID) == nil {
		delete(e.orders, orderID)
		if owner != "" {
			e.removeUserOrder(owner, orderID)
		}
	}
}

// handleEvictions refunds held funds for evicted orders and clears them from tracking.
// Caller must hold e.mu.Lock(). The previous "ev.Remaining = 0" mutation was
// removed when history switched to value-typed Orders (see History docstring).
func (e *Extension) handleEvictions(pair string, pairConfig config.TradingPairConfig, evicted []*orderbook.Order) {
	for _, ev := range evicted {
		owner := strings.ToLower(ev.Owner)
		token, amt := e.calculateRelease(pairConfig, ev)
		if amt > 0 {
			_ = e.balances.Release(owner, token, amt)
		}
		delete(e.orders, ev.ID)
		e.removeUserOrder(owner, ev.ID)
		logger.Infof("evicted order %s pair=%s side=%s price=%d (cap=%d)",
			ev.ID, pair, ev.Side, ev.Price, MaxLevelsPerSide)
	}
}

// removeUserOrder removes an orderID from the user's order list.
// Caller must hold e.mu.Lock().
func (e *Extension) removeUserOrder(user, orderID string) {
	ids := e.userOrders[user]
	for i, id := range ids {
		if id == orderID {
			e.userOrders[user] = append(ids[:i], ids[i+1:]...)
			return
		}
	}
}

// calculateHold determines what token and how much to hold for an order.
func (e *Extension) calculateHold(user string, pair config.TradingPairConfig, order *orderbook.Order) (holdToken common.Address, holdAmount uint64, err error) {
	switch order.Side {
	case orderbook.Buy:
		holdToken = pair.QuoteToken
		if order.Type == orderbook.Market {
			holdAmount = e.balances.AvailableBalance(user, holdToken)
			if holdAmount == 0 {
				return holdToken, 0, fmt.Errorf("no available %s balance for market buy", holdToken.Hex())
			}
		} else {
			var ok bool
			holdAmount, ok = safeMulDiv(order.Quantity, order.Price, pricePrecision)
			if !ok {
				return holdToken, 0, fmt.Errorf("overflow: quantity * price exceeds uint64")
			}
		}
	case orderbook.Sell:
		holdToken = pair.BaseToken
		if order.Type == orderbook.Market {
			holdAmount = e.balances.AvailableBalance(user, holdToken)
			if holdAmount == 0 {
				return holdToken, 0, fmt.Errorf("no available %s balance for market sell", holdToken.Hex())
			}
		} else {
			holdAmount = order.Quantity
		}
	default:
		return holdToken, 0, fmt.Errorf("invalid side: %s", order.Side)
	}
	return holdToken, holdAmount, nil
}

// calculateRelease determines what token and how much to release for a cancelled order.
func (e *Extension) calculateRelease(pair config.TradingPairConfig, order *orderbook.Order) (common.Address, uint64) {
	switch order.Side {
	case orderbook.Buy:
		return pair.QuoteToken, mulPrice(order.Remaining, order.Price)
	case orderbook.Sell:
		return pair.BaseToken, order.Remaining
	default:
		return common.Address{}, 0
	}
}

// totalFilled calculates the total amount of the hold token that was actually used in matches.
func totalFilled(matches []orderbook.Match, order *orderbook.Order) uint64 {
	var total uint64
	for _, m := range matches {
		if order.Side == orderbook.Buy {
			total += mulPrice(m.Quantity, m.Price)
		} else {
			total += m.Quantity
		}
	}
	return total
}
