package extension

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"veil/internal/config"
	"veil/pkg/balance"
	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// newTestExtension builds a minimal Extension wired up for one trading pair, with no HTTP server.
func newTestExtension(pair string, base, quote common.Address) *Extension {
	pairCfg := config.TradingPairConfig{Name: pair, BaseToken: base, QuoteToken: quote}
	e := &Extension{
		orderbooks:         map[string]*orderbook.OrderBook{pair: orderbook.NewOrderBook(pair)},
		balances:           balance.NewManager(),
		pairs:              map[string]config.TradingPairConfig{pair: pairCfg},
		matchesByPair:      map[string]*orderbook.Ring[orderbook.Match]{pair: orderbook.NewRing[orderbook.Match](MaxMatchesPerPair)},
		candles:            map[string]map[orderbook.Timeframe]*orderbook.Ring[orderbook.Candle]{pair: {}},
		orders:             make(map[string]string),
		userOrders:         make(map[string][]string),
		history:            newHistory(),
		admins:             make(map[string]bool),
		usedSolvencyNonces: make(map[string]time.Time),
		issuedCommitments:  make(map[string]issuedCommitmentRecord),
	}
	for _, tf := range orderbook.Timeframes {
		e.candles[pair][tf] = orderbook.NewRing[orderbook.Candle](MaxCandlesPerTF)
	}
	return e
}

// placeOrder is a thin test-only invocation of processPlaceOrder that returns the parsed response.
func placeOrder(t *testing.T, e *Extension, req types.PlaceOrderRequest) types.PlaceOrderResponse {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	action := teetypes.Action{}
	df := &instruction.DataFixed{}
	ar := e.processPlaceOrder(action, df, body)
	if ar.Status != 1 {
		t.Fatalf("place order failed: %s", ar.Log)
	}
	var resp types.PlaceOrderResponse
	if err := json.Unmarshal(ar.Data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// placeOrderExpectErr returns the action result expecting Status=0 (error).
func placeOrderExpectErr(t *testing.T, e *Extension, req types.PlaceOrderRequest) string {
	t.Helper()
	body, _ := json.Marshal(req)
	ar := e.processPlaceOrder(teetypes.Action{}, &instruction.DataFixed{}, body)
	if ar.Status == 1 {
		t.Fatalf("expected error, got success: data=%s", string(ar.Data))
	}
	return ar.Log
}

func TestPlaceOrder_PerUserOpenOrderCap(t *testing.T) {
	pair := "FLR/USDT"
	base := common.HexToAddress("0x1111111111111111111111111111111111111111")
	quote := common.HexToAddress("0x2222222222222222222222222222222222222222")
	e := newTestExtension(pair, base, quote)

	user := "0xabcd0000000000000000000000000000000000ab"
	// Plenty of base token to seed many sells.
	if err := e.balances.Deposit(strings.ToLower(user), base, 1_000_000); err != nil {
		t.Fatal(err)
	}

	prev := MaxOrdersPerUser
	MaxOrdersPerUser = 3
	t.Cleanup(func() { MaxOrdersPerUser = prev })

	for i := 0; i < 3; i++ {
		placeOrder(t, e, types.PlaceOrderRequest{
			Sender: user, Pair: pair, Side: orderbook.Sell, Type: orderbook.Limit,
			Price: uint64(100 + i), Quantity: 5,
		})
	}
	// 4th must be rejected before any hold is taken.
	availBefore := e.balances.AvailableBalance(strings.ToLower(user), base)
	log := placeOrderExpectErr(t, e, types.PlaceOrderRequest{
		Sender: user, Pair: pair, Side: orderbook.Sell, Type: orderbook.Limit, Price: 110, Quantity: 5,
	})
	if !strings.Contains(log, "too many open orders") {
		t.Fatalf("error log: %s", log)
	}
	availAfter := e.balances.AvailableBalance(strings.ToLower(user), base)
	if availBefore != availAfter {
		t.Fatalf("balance changed on rejected order: before=%d after=%d", availBefore, availAfter)
	}
	if got := len(e.userOrders[strings.ToLower(user)]); got != 3 {
		t.Fatalf("userOrders: got %d, want 3", got)
	}
}

func TestPlaceOrder_LevelEvictionRefundsAndUpdatesTracking(t *testing.T) {
	pair := "FLR/USDT"
	base := common.HexToAddress("0x1111111111111111111111111111111111111111")
	quote := common.HexToAddress("0x2222222222222222222222222222222222222222")
	e := newTestExtension(pair, base, quote)

	prev := MaxLevelsPerSide
	MaxLevelsPerSide = 3
	t.Cleanup(func() { MaxLevelsPerSide = prev })

	// One sell per level, each user different so tracking is independent.
	mkUser := func(i int) string {
		return strings.ToLower(fmt.Sprintf("0x%040d", i+1))
	}
	const qty uint64 = 5
	for i := 0; i < 3; i++ {
		u := mkUser(i)
		if err := e.balances.Deposit(u, base, qty); err != nil {
			t.Fatal(err)
		}
		placeOrder(t, e, types.PlaceOrderRequest{
			Sender: u, Pair: pair, Side: orderbook.Sell, Type: orderbook.Limit,
			Price:    uint64(100 + i), // 100, 101, 102 (worst = 102)
			Quantity: qty,
		})
	}

	// 4th sell at the highest price (worst) → eviction will pick this one.
	worstUser := mkUser(99)
	if err := e.balances.Deposit(worstUser, base, qty); err != nil {
		t.Fatal(err)
	}
	resp := placeOrder(t, e, types.PlaceOrderRequest{
		Sender: worstUser, Pair: pair, Side: orderbook.Sell, Type: orderbook.Limit,
		Price: 200, Quantity: qty,
	})
	if resp.Status != "resting" {
		t.Fatalf("status: got %s, want resting (pre-eviction)", resp.Status)
	}

	// After eviction: book has 3 levels, the worst (200) is gone.
	_, asks := e.orderbooks[pair].Depth()
	if len(asks) != 3 {
		t.Fatalf("ask levels: got %d, want 3", len(asks))
	}
	for _, lvl := range asks {
		if lvl.Price == 200 {
			t.Fatalf("worst-priced level (200) should have been evicted")
		}
	}

	// The evicted user's hold has been refunded.
	bal := e.balances.Get(worstUser, base)
	if bal.Held != 0 {
		t.Fatalf("evicted user held: got %d, want 0", bal.Held)
	}
	if bal.Available != qty {
		t.Fatalf("evicted user available: got %d, want %d", bal.Available, qty)
	}

	// Tracking is cleared for the evicted order.
	if len(e.userOrders[worstUser]) != 0 {
		t.Fatalf("userOrders[evicted]: got %v, want empty", e.userOrders[worstUser])
	}
	for id, p := range e.orders {
		if p == pair {
			if e.orderbooks[pair].GetOrder(id) == nil {
				t.Fatalf("e.orders contains stale id %s (no longer on book)", id)
			}
		}
	}

	// The evicted order's history entry exists; per C7, history snapshots at place time.
	hist := e.history.orders[worstUser]
	if len(hist) != 1 {
		t.Fatalf("history: got %d entries, want 1", len(hist))
	}
	if hist[0].ID == "" || hist[0].Owner != worstUser {
		t.Fatalf("history entry malformed: %+v", hist[0])
	}

	// Surviving users still have their funds held.
	for i := 0; i < 3; i++ {
		u := mkUser(i)
		b := e.balances.Get(u, base)
		if b.Held != qty {
			t.Errorf("user %d held: got %d, want %d", i, b.Held, qty)
		}
	}
}

func TestProcessMatch_PerPairRingAndCandles(t *testing.T) {
	pair := "FLR/USDT"
	base := common.HexToAddress("0x1111111111111111111111111111111111111111")
	quote := common.HexToAddress("0x2222222222222222222222222222222222222222")
	e := newTestExtension(pair, base, quote)

	seller := "0x" + strings.Repeat("a", 40)
	buyer := "0x" + strings.Repeat("b", 40)
	if err := e.balances.Deposit(seller, base, 100); err != nil {
		t.Fatal(err)
	}
	if err := e.balances.Deposit(buyer, quote, 1_000_000); err != nil {
		t.Fatal(err)
	}

	// Price units are scaled by pricePrecision (1_000_000), so 100_000 = $0.1.
	// Quote hold = 10 * 100_000 / 1_000_000 = 1 unit, well above ErrZeroAmount.
	const matchPrice uint64 = 100_000
	placeOrder(t, e, types.PlaceOrderRequest{
		Sender: seller, Pair: pair, Side: orderbook.Sell, Type: orderbook.Limit, Price: matchPrice, Quantity: 10,
	})
	resp := placeOrder(t, e, types.PlaceOrderRequest{
		Sender: buyer, Pair: pair, Side: orderbook.Buy, Type: orderbook.Limit, Price: matchPrice, Quantity: 10,
	})

	if resp.Status != "filled" {
		t.Fatalf("status: got %s, want filled", resp.Status)
	}

	// Per-pair ring has the match.
	ring := e.matchesByPair[pair]
	if ring.Len() != 1 {
		t.Fatalf("pair ring len: got %d, want 1", ring.Len())
	}

	// Per-user history has it for both sides.
	if got := len(e.history.matches[seller]); got != 1 {
		t.Fatalf("seller match history: got %d, want 1", got)
	}
	if got := len(e.history.matches[buyer]); got != 1 {
		t.Fatalf("buyer match history: got %d, want 1", got)
	}

	// Candles updated for every timeframe.
	for _, tf := range orderbook.Timeframes {
		r := e.candles[pair][tf]
		if r.Len() != 1 {
			t.Errorf("%v candle ring len: got %d, want 1", tf, r.Len())
		}
		c, ok := r.Latest()
		if !ok {
			t.Errorf("%v candle missing", tf)
			continue
		}
		if c.Open != matchPrice || c.Close != matchPrice || c.Volume != 10 || c.Trades != 1 {
			t.Errorf("%v candle OHLCV: got %+v, want O=C=%d V=10 T=1", tf, c, matchPrice)
		}
	}

	// Both orders fully filled — neither should still be tracked.
	if len(e.orders) != 0 {
		t.Fatalf("e.orders: got %v, want empty", e.orders)
	}
	if len(e.userOrders[seller]) != 0 || len(e.userOrders[buyer]) != 0 {
		t.Fatalf("userOrders: seller=%v buyer=%v", e.userOrders[seller], e.userOrders[buyer])
	}
}

func TestPlaceOrder_HistoryBoundedToCap(t *testing.T) {
	pair := "FLR/USDT"
	base := common.HexToAddress("0x1111111111111111111111111111111111111111")
	quote := common.HexToAddress("0x2222222222222222222222222222222222222222")
	e := newTestExtension(pair, base, quote)

	prev := MaxUserHistoryOrders
	MaxUserHistoryOrders = 3
	t.Cleanup(func() { MaxUserHistoryOrders = prev })

	user := "0x" + strings.Repeat("c", 40)
	if err := e.balances.Deposit(user, base, 100); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		placeOrder(t, e, types.PlaceOrderRequest{
			Sender: user, Pair: pair, Side: orderbook.Sell, Type: orderbook.Limit,
			Price: uint64(100 + i), Quantity: 5,
		})
	}
	if got := len(e.history.orders[user]); got != 3 {
		t.Fatalf("history len: got %d, want 3 (cap)", got)
	}
}
