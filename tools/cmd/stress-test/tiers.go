package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"veil/tools/pkg/stress"
)

// PersonaMix is how many traders of each type to spawn.
type PersonaMix struct {
	MarketMakers     int // limit orders around mid
	AggressiveTakers int // market orders
	RandomWalkers    int // random limits/markets
	Whales           int // occasional big orders
	Flickers         int // place + cancel
}

func (m PersonaMix) Total() int {
	return m.MarketMakers + m.AggressiveTakers + m.RandomWalkers + m.Whales + m.Flickers
}

// Tier bundles the stock configurations. All price / quantity fields are in
// HUMAN units (whole quote-per-base for prices, whole base-tokens for qty);
// BuildAssignments scales them to raw TEE units via the provided Scaling.
//
// Per-persona cadence fields default to zero, in which case each persona uses
// its own fast built-in default — tune them only for the "day" soak profile
// where longer pauses matter.
type Tier struct {
	Name        string
	Mix         PersonaMix
	Duration    time.Duration
	BaseMid     uint64        // quote per base, human
	BaseSpread  uint64        // quote per base, human
	WalkerLow   uint64        // quote per base, human
	WalkerHigh  uint64        // quote per base, human
	MMRefresh   time.Duration // 0 = persona default (2s)
	TakerPause  time.Duration // 0 = persona default (500ms)
	WalkerPause time.Duration // 0 = persona default (500ms-2s random)
	WhalePause  time.Duration // 0 = persona default (30s)

	// --- Oracle-mode fields (optional; used by btc-day / eth-day) ---

	// PriceSymbol is the CoinGecko id ("bitcoin", "ethereum", …). When set,
	// BuildAssignments constructs a CoinGecko oracle and wires it into every
	// persona; spread/walker-bounds then use the *Bps fields below and ignore
	// the absolute BaseSpread / WalkerLow / WalkerHigh. When empty, static
	// pricing is used (every existing L1–L5 tier and `day`).
	PriceSymbol string

	SpreadBps     uint64 // MM & flicker relative spread, e.g. 100 = 1%
	WalkerLowBps  uint64 // walker low bound below live mid, e.g. 100 = 1%
	WalkerHighBps uint64 // walker high bound above live mid

	// Per-persona qty bounds expressed in *thousandths* of a base token. When
	// non-zero, these override the hardcoded 1–5 / 1–3 / etc. human-unit qty
	// defaults. Lets tiers for high-priced assets (BTC) place sub-unit
	// quantities without pulling in floats.
	MMQtyMilliMin, MMQtyMilliMax         uint64
	TakerQtyMilliMin, TakerQtyMilliMax   uint64
	WalkerQtyMilliMin, WalkerQtyMilliMax uint64
}

func tierByName(name string) (Tier, error) {
	switch strings.ToUpper(name) {
	case "L1":
		return Tier{Name: "L1", Mix: PersonaMix{1, 1, 1, 0, 0}, Duration: 60 * time.Second, BaseMid: 100, BaseSpread: 2, WalkerLow: 80, WalkerHigh: 120}, nil
	case "L2":
		return Tier{Name: "L2", Mix: PersonaMix{2, 3, 4, 1, 0}, Duration: 5 * time.Minute, BaseMid: 100, BaseSpread: 2, WalkerLow: 80, WalkerHigh: 120}, nil
	case "L3":
		return Tier{Name: "L3", Mix: PersonaMix{5, 15, 25, 3, 2}, Duration: 10 * time.Minute, BaseMid: 100, BaseSpread: 3, WalkerLow: 70, WalkerHigh: 130}, nil
	case "L4":
		return Tier{Name: "L4", Mix: PersonaMix{10, 60, 100, 20, 10}, Duration: 15 * time.Minute, BaseMid: 100, BaseSpread: 5, WalkerLow: 50, WalkerHigh: 150}, nil
	case "L5":
		return Tier{Name: "L5", Mix: PersonaMix{20, 150, 250, 50, 30}, Duration: 30 * time.Second, BaseMid: 100, BaseSpread: 10, WalkerLow: 20, WalkerHigh: 180}, nil
	case "DAY":
		// Soak profile: simulate a quiet but active trading day. Low throughput
		// (~10 orders/min) and all traders Persistent so the run continues until
		// SIGTERM/SIGINT. Balance-neutral by design (MMs post both sides, takers
		// cross both sides), so traders don't drift toward zero over hours.
		//
		// Walker bounds are tight (±1% of mid) so its limit orders can't sit
		// deep out-of-band if a run crashes before sweep completes — prevents
		// a stale bargain-priced order pool from polluting later runs.
		return Tier{
			Name: "day", Mix: PersonaMix{2, 2, 1, 0, 0},
			Duration: 0, BaseMid: 100, BaseSpread: 2,
			WalkerLow: 99, WalkerHigh: 101,
			MMRefresh: 20 * time.Second, TakerPause: 45 * time.Second, WalkerPause: 60 * time.Second,
		}, nil
	case "BTC-DAY":
		// Soak profile tracking the live BTC/USD price via CoinGecko. Same
		// cadence as `day`; price floats with the real market, MM spread is
		// ±1% of the live mid, qty 0.005–0.1 BTC per order so a $100k deposit
		// lasts many hours at $60–100k/BTC.
		// Walker bounds are tighter than MM spread (50 vs 100bps) so walker
		// limit orders never cross MM quotes — prevents the book from being
		// drained by self-fills against the MM at live prices.
		return Tier{
			Name: "btc-day", Mix: PersonaMix{4, 2, 1, 0, 0},
			Duration:    0,
			PriceSymbol: "bitcoin", SpreadBps: 100, WalkerLowBps: 50, WalkerHighBps: 50,
			MMRefresh: 20 * time.Second, TakerPause: 45 * time.Second, WalkerPause: 60 * time.Second,
			MMQtyMilliMin: 5, MMQtyMilliMax: 100,
			TakerQtyMilliMin: 5, TakerQtyMilliMax: 100,
			WalkerQtyMilliMin: 5, WalkerQtyMilliMax: 100,
		}, nil
	case "ETH-DAY":
		// Soak profile tracking live ETH/USD. qty 0.1–1 ETH. Walker bounds
		// tighter than MM spread for the same reason as btc-day.
		return Tier{
			Name: "eth-day", Mix: PersonaMix{4, 2, 1, 0, 0},
			Duration:    0,
			PriceSymbol: "ethereum", SpreadBps: 100, WalkerLowBps: 50, WalkerHighBps: 50,
			MMRefresh: 20 * time.Second, TakerPause: 45 * time.Second, WalkerPause: 60 * time.Second,
			MMQtyMilliMin: 100, MMQtyMilliMax: 1000,
			TakerQtyMilliMin: 100, TakerQtyMilliMax: 1000,
			WalkerQtyMilliMin: 100, WalkerQtyMilliMax: 1000,
		}, nil
	case "FLR-DAY":
		// Soak profile tracking live FLR/USD via CoinGecko. With
		// pricePrecision=1_000_000 (1 raw tick = $0.000001) FLR's ~$0.008 has
		// plenty of headroom for a real spread; walker bounds stay at ±5%
		// because at the lower precision the hi/lo integer round used to
		// collapse to a single tick — kept wider for safety, not strictly
		// needed now. qty 1000-5000 FLR per order (~$8-$40 at $0.008) keeps
		// the soak balance-neutral over a multi-hour run.
		return Tier{
			Name: "flr-day", Mix: PersonaMix{4, 2, 1, 0, 0},
			Duration:    0,
			PriceSymbol: "flare-networks", SpreadBps: 100, WalkerLowBps: 500, WalkerHighBps: 500,
			MMRefresh: 20 * time.Second, TakerPause: 45 * time.Second, WalkerPause: 60 * time.Second,
			MMQtyMilliMin: 1_000_000, MMQtyMilliMax: 5_000_000,
			TakerQtyMilliMin: 1_000_000, TakerQtyMilliMax: 5_000_000,
			WalkerQtyMilliMin: 1_000_000, WalkerQtyMilliMax: 5_000_000,
		}, nil
	default:
		return Tier{}, fmt.Errorf("unknown tier %q (want L1..L5, day, btc-day, eth-day, flr-day)", name)
	}
}

// ParseMixOverride reads "mm:2,taker:3,walker:5,whale:1,flicker:1".
func ParseMixOverride(s string) (PersonaMix, error) {
	var m PersonaMix
	if s == "" {
		return m, nil
	}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return m, fmt.Errorf("bad pair %q", part)
		}
		n, err := strconv.Atoi(kv[1])
		if err != nil {
			return m, fmt.Errorf("bad count in %q: %w", part, err)
		}
		switch strings.ToLower(kv[0]) {
		case "mm":
			m.MarketMakers = n
		case "taker":
			m.AggressiveTakers = n
		case "walker":
			m.RandomWalkers = n
		case "whale":
			m.Whales = n
		case "flicker":
			m.Flickers = n
		default:
			return m, fmt.Errorf("unknown persona %q", kv[0])
		}
	}
	return m, nil
}

// BuildAssignments converts a tier + mix into concrete Assignment objects.
// All human quantities/prices from the tier are scaled to raw TEE units here
// using sc. Market makers are always Persistent; if duration is 0 (perpetual),
// ALL traders become Persistent.
//
// oracle is optional. When non-nil it overrides the tier's static BaseMid and
// every persona reads the live mid on each action; spread/walker bounds use
// the tier's *Bps fields. When nil, static pricing applies.
func BuildAssignments(tier Tier, traders []*stress.Trader, pair string, duration time.Duration, sc stress.Scaling, oracle stress.PriceOracle) []stress.Assignment {
	out := make([]stress.Assignment, 0, len(traders))
	idx := 0
	add := func(n int, persona stress.Persona, role stress.TraderRole) {
		for k := 0; k < n && idx < len(traders); k++ {
			out = append(out, stress.Assignment{Trader: traders[idx], Persona: persona, Role: role})
			idx++
		}
	}
	mkRole := stress.Ephemeral
	if duration == 0 {
		mkRole = stress.Persistent
	}
	mmRefresh := tier.MMRefresh
	if mmRefresh == 0 {
		mmRefresh = 3 * time.Second
	}

	// Static price/spread only matter when there's no oracle.
	mid := sc.ScalePrice(tier.BaseMid)
	spread := sc.ScalePrice(tier.BaseSpread)
	walkerLow := sc.ScalePrice(tier.WalkerLow)
	walkerHigh := sc.ScalePrice(tier.WalkerHigh)

	// Qty bounds: prefer per-tier milli overrides when set, else fall back
	// to the original whole-token defaults.
	mmQtyMin, mmQtyMax := scaleQtyRange(sc, tier.MMQtyMilliMin, tier.MMQtyMilliMax, 1, 5)
	takerQtyMin, takerQtyMax := scaleQtyRange(sc, tier.TakerQtyMilliMin, tier.TakerQtyMilliMax, 1, 3)
	walkerQtyMin, walkerQtyMax := scaleQtyRange(sc, tier.WalkerQtyMilliMin, tier.WalkerQtyMilliMax, 1, 10)
	whaleQtyMin, whaleQtyMax := sc.ScaleQty(50), sc.ScaleQty(200)
	flickerQtyMin, flickerQtyMax := sc.ScaleQty(1), sc.ScaleQty(3)

	add(tier.Mix.MarketMakers, stress.NewMarketMaker(stress.MarketMakerConfig{
		Pair: pair, MidPrice: mid, Spread: spread, SpreadBps: tier.SpreadBps,
		QtyMin: mmQtyMin, QtyMax: mmQtyMax, Refresh: mmRefresh, Oracle: oracle,
	}), stress.Persistent)
	add(tier.Mix.AggressiveTakers, stress.NewAggressiveTaker(stress.TakerConfig{
		Pair: pair, QtyMin: takerQtyMin, QtyMax: takerQtyMax, Pause: tier.TakerPause,
	}), mkRole)
	add(tier.Mix.RandomWalkers, stress.NewRandomWalker(stress.WalkerConfig{
		Pair: pair, PriceMin: walkerLow, PriceMax: walkerHigh,
		LowBps: tier.WalkerLowBps, HighBps: tier.WalkerHighBps,
		QtyMin: walkerQtyMin, QtyMax: walkerQtyMax, Pause: tier.WalkerPause, Oracle: oracle,
	}), mkRole)
	add(tier.Mix.Whales, stress.NewWhale(stress.WhaleConfig{
		Pair: pair, QtyMin: whaleQtyMin, QtyMax: whaleQtyMax, Price: mid, Pause: tier.WhalePause, Oracle: oracle,
	}), mkRole)
	add(tier.Mix.Flickers, stress.NewFlicker(stress.FlickerConfig{
		Pair: pair, MidPrice: mid, Spread: spread, SpreadBps: tier.SpreadBps,
		QtyMin: flickerQtyMin, QtyMax: flickerQtyMax, Oracle: oracle,
	}), mkRole)
	return out
}

// scaleQtyRange picks milli-overrides when set, otherwise scales the whole-
// token defaults.
func scaleQtyRange(sc stress.Scaling, milliMin, milliMax, defaultMin, defaultMax uint64) (uint64, uint64) {
	if milliMin > 0 && milliMax > 0 && milliMax >= milliMin {
		return sc.ScaleQtyMilli(milliMin), sc.ScaleQtyMilli(milliMax)
	}
	return sc.ScaleQty(defaultMin), sc.ScaleQty(defaultMax)
}
