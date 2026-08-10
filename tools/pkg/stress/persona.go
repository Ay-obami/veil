package stress

import (
	"fmt"
	"math/rand"
	"time"
)

// Action is what a persona wants to do next. The runner translates this into
// Trader calls.
type Action struct {
	Kind     string // "place" | "cancel"
	Pair     string
	Side     string // "buy" | "sell"
	Type     string // "limit" | "market"
	Price    uint64 // on-wire (pre-multiplied by PricePrecision; see scaling.go)
	Quantity uint64
}

// Persona generates the next action for a trader. Must be safe for single-
// goroutine use (each trader has its own persona instance).
type Persona interface {
	Name() string
	NextAction(r *rand.Rand) Action
	PauseAfter(r *rand.Rand) time.Duration
}

// --- Market maker: posts both sides of the book around a mid. Always limit. ---

// MarketMakerConfig. If Oracle is set, each NextAction reads the live mid from
// it and — when SpreadBps > 0 — computes the spread as a percentage of that
// live mid (mid * SpreadBps / 10000). This lets an MM track a moving market
// without restarting. When Oracle is nil, static MidPrice + absolute Spread
// are used (the original behavior).
type MarketMakerConfig struct {
	Pair      string
	MidPrice  uint64
	Spread    uint64 // absolute; used when SpreadBps == 0 or Oracle is nil
	SpreadBps uint64 // relative; used when > 0 and Oracle is set
	QtyMin    uint64
	QtyMax    uint64
	Refresh   time.Duration
	Oracle    PriceOracle
}

type marketMaker struct {
	cfg      MarketMakerConfig
	phase    int    // 0,1 = initial place-buy / place-sell; then alternating cancel/place
	nextSide string // which side the next steady-state place will use
}

func NewMarketMaker(cfg MarketMakerConfig) Persona { return &marketMaker{cfg: cfg} }
func (m *marketMaker) Name() string                { return "market_maker" }

// NextAction implements a requote cycle so the MM never accumulates stale quotes:
//
//	phase 0 → place buy
//	phase 1 → place sell      (book now has one bid + one ask from this MM)
//	phase 2 → cancel oldest
//	phase 3 → place (alternating side)
//	phase 4 → cancel oldest
//	phase 5 → place (alternating side)
//	...
//
// Steady-state resting-order count stays at 1–2 regardless of run length.
func (m *marketMaker) NextAction(r *rand.Rand) Action {
	switch m.phase {
	case 0:
		m.phase = 1
		m.nextSide = "buy" // after startup, first steady-state place is a fresh buy
		return m.buildPlace(r, "buy")
	case 1:
		m.phase = 2
		return m.buildPlace(r, "sell")
	}
	if m.phase%2 == 0 {
		// cancel the oldest tracked order
		m.phase++
		return Action{Kind: "cancel", Pair: m.cfg.Pair}
	}
	// odd steady-state phase: place, alternating side
	side := m.nextSide
	if side == "buy" {
		m.nextSide = "sell"
	} else {
		m.nextSide = "buy"
	}
	m.phase++
	return m.buildPlace(r, side)
}

func (m *marketMaker) buildPlace(r *rand.Rand, side string) Action {
	mid, spread := resolveMidSpread(m.cfg.Oracle, m.cfg.MidPrice, m.cfg.Spread, m.cfg.SpreadBps)
	half := spread / 2
	var price uint64
	if side == "sell" {
		price = mid + half + uint64(r.Intn(int(half)+1))
	} else {
		price = mid - half - uint64(r.Intn(int(half)+1))
	}
	qty := m.cfg.QtyMin + uint64(r.Intn(int(m.cfg.QtyMax-m.cfg.QtyMin+1)))
	return Action{Kind: "place", Pair: m.cfg.Pair, Side: side, Type: "limit", Price: price, Quantity: qty}
}

// resolveMidSpread picks the live mid/spread if an oracle is wired in,
// otherwise falls back to the static pair. Kept as a package-level helper so
// MM and Flicker share one definition.
func resolveMidSpread(oracle PriceOracle, staticMid, staticSpread, spreadBps uint64) (mid, spread uint64) {
	mid = staticMid
	spread = staticSpread
	if oracle != nil {
		live := oracle.Mid()
		if live == 0 {
			// An oracle was wired in but it reports no price. Falling back to
			// staticMid would silently quote against an arbitrary number
			// (e.g. 100 from the `day` tier). Bail loudly instead — this is a
			// real outage, not a transient blip the runtime poll-loop already
			// papers over by holding the last good mid.
			panic(fmt.Sprintf("oracle %s reports mid=0; refusing to fall back to staticMid=%d", oracle.Symbol(), staticMid))
		}
		mid = live
		if spreadBps > 0 {
			spread = mid * spreadBps / 10000
			if spread == 0 {
				spread = 1 // never produce a zero spread
			}
		}
	}
	return mid, spread
}
func (m *marketMaker) PauseAfter(r *rand.Rand) time.Duration {
	if m.cfg.Refresh == 0 {
		return 2 * time.Second
	}
	jitter := time.Duration(r.Int63n(int64(m.cfg.Refresh / 2)))
	return m.cfg.Refresh - m.cfg.Refresh/4 + jitter
}

// --- Aggressive taker: market orders that cross the book. ---

type TakerConfig struct {
	Pair           string
	QtyMin, QtyMax uint64
	Pause          time.Duration
}

type taker struct{ cfg TakerConfig }

func NewAggressiveTaker(cfg TakerConfig) Persona { return &taker{cfg: cfg} }
func (t *taker) Name() string                    { return "taker" }
func (t *taker) NextAction(r *rand.Rand) Action {
	side := "buy"
	if r.Intn(2) == 0 {
		side = "sell"
	}
	qty := t.cfg.QtyMin + uint64(r.Intn(int(t.cfg.QtyMax-t.cfg.QtyMin+1)))
	return Action{Kind: "place", Pair: t.cfg.Pair, Side: side, Type: "market", Quantity: qty}
}
func (t *taker) PauseAfter(r *rand.Rand) time.Duration {
	if t.cfg.Pause == 0 {
		return 500 * time.Millisecond
	}
	return t.cfg.Pause
}

// --- Random walker: any side, any type, random price within bounds. ---

// WalkerConfig. If Oracle is set and LowBps/HighBps > 0, the price bounds are
// computed around the live mid each call: [mid * (10000-LowBps) / 10000,
// mid * (10000+HighBps) / 10000]. Otherwise the static PriceMin/PriceMax are
// used verbatim.
type WalkerConfig struct {
	Pair               string
	PriceMin, PriceMax uint64
	LowBps, HighBps    uint64 // used with Oracle; e.g. 100 = 1%
	QtyMin, QtyMax     uint64
	Pause              time.Duration
	Oracle             PriceOracle
}

type walker struct{ cfg WalkerConfig }

func NewRandomWalker(cfg WalkerConfig) Persona { return &walker{cfg: cfg} }
func (w *walker) Name() string                 { return "walker" }
func (w *walker) NextAction(r *rand.Rand) Action {
	side := "buy"
	if r.Intn(2) == 0 {
		side = "sell"
	}
	typ := "limit"
	if r.Intn(3) == 0 {
		typ = "market"
	}
	qty := w.cfg.QtyMin + uint64(r.Intn(int(w.cfg.QtyMax-w.cfg.QtyMin+1)))
	a := Action{Kind: "place", Pair: w.cfg.Pair, Side: side, Type: typ, Quantity: qty}
	if typ == "limit" {
		lo, hi := w.cfg.PriceMin, w.cfg.PriceMax
		if w.cfg.Oracle != nil && w.cfg.LowBps > 0 && w.cfg.HighBps > 0 {
			if live := w.cfg.Oracle.Mid(); live > 0 {
				lo = live * (10000 - w.cfg.LowBps) / 10000
				hi = live * (10000 + w.cfg.HighBps) / 10000
			}
		}
		if hi <= lo {
			hi = lo + 1
		}
		a.Price = lo + uint64(r.Intn(int(hi-lo+1)))
	}
	return a
}
func (w *walker) PauseAfter(r *rand.Rand) time.Duration {
	if w.cfg.Pause == 0 {
		return time.Duration(500+r.Intn(1500)) * time.Millisecond
	}
	return w.cfg.Pause
}

// --- Whale: occasional large orders. ---

// WhaleConfig. Price is unused for market orders but kept for symmetry.
// Oracle, if set, is informational only (whale uses market orders).
type WhaleConfig struct {
	Pair           string
	QtyMin, QtyMax uint64
	Price          uint64
	Pause          time.Duration
	Oracle         PriceOracle
}

type whale struct{ cfg WhaleConfig }

func NewWhale(cfg WhaleConfig) Persona { return &whale{cfg: cfg} }
func (w *whale) Name() string          { return "whale" }
func (w *whale) NextAction(r *rand.Rand) Action {
	side := "buy"
	if r.Intn(2) == 0 {
		side = "sell"
	}
	qty := w.cfg.QtyMin + uint64(r.Intn(int(w.cfg.QtyMax-w.cfg.QtyMin+1)))
	return Action{Kind: "place", Pair: w.cfg.Pair, Side: side, Type: "market", Quantity: qty}
}
func (w *whale) PauseAfter(r *rand.Rand) time.Duration {
	if w.cfg.Pause == 0 {
		return 30 * time.Second
	}
	return w.cfg.Pause
}

// --- Flicker: places then cancels quickly. ---

type FlickerConfig struct {
	Pair                             string
	MidPrice, Spread, QtyMin, QtyMax uint64
	SpreadBps                        uint64
	Pause                            time.Duration
	Oracle                           PriceOracle
}

type flicker struct {
	cfg       FlickerConfig
	lastPlace bool
}

func NewFlicker(cfg FlickerConfig) Persona { return &flicker{cfg: cfg} }
func (f *flicker) Name() string            { return "flicker" }
func (f *flicker) NextAction(r *rand.Rand) Action {
	f.lastPlace = !f.lastPlace
	if !f.lastPlace {
		return Action{Kind: "cancel", Pair: f.cfg.Pair}
	}
	mid, spread := resolveMidSpread(f.cfg.Oracle, f.cfg.MidPrice, f.cfg.Spread, f.cfg.SpreadBps)
	side := "buy"
	half := spread / 2
	price := mid - half - uint64(r.Intn(int(half)+1))
	if r.Intn(2) == 0 {
		side = "sell"
		price = mid + half + uint64(r.Intn(int(half)+1))
	}
	qty := f.cfg.QtyMin + uint64(r.Intn(int(f.cfg.QtyMax-f.cfg.QtyMin+1)))
	return Action{Kind: "place", Pair: f.cfg.Pair, Side: side, Type: "limit", Price: price, Quantity: qty}
}
func (f *flicker) PauseAfter(r *rand.Rand) time.Duration {
	if f.cfg.Pause == 0 {
		return 200 * time.Millisecond
	}
	return f.cfg.Pause
}
