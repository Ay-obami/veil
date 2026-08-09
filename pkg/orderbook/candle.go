package orderbook

import "fmt"

// Timeframe is a candle bucket size in seconds.
type Timeframe int64

const (
	TF1m  Timeframe = 60
	TF5m  Timeframe = 5 * 60
	TF15m Timeframe = 15 * 60
	TF1h  Timeframe = 60 * 60
	TF4h  Timeframe = 4 * 60 * 60
	TF1d  Timeframe = 24 * 60 * 60
)

// Timeframes is the canonical list, in ascending order.
var Timeframes = []Timeframe{TF1m, TF5m, TF15m, TF1h, TF4h, TF1d}

// String returns the canonical label (matches frontend tokens).
func (tf Timeframe) String() string {
	switch tf {
	case TF1m:
		return "1m"
	case TF5m:
		return "5m"
	case TF15m:
		return "15m"
	case TF1h:
		return "1h"
	case TF4h:
		return "4h"
	case TF1d:
		return "1D"
	default:
		return fmt.Sprintf("%ds", int64(tf))
	}
}

// ParseTimeframe parses a label into a Timeframe. Empty input defaults to 1m.
func ParseTimeframe(s string) (Timeframe, error) {
	switch s {
	case "", "1m":
		return TF1m, nil
	case "5m":
		return TF5m, nil
	case "15m":
		return TF15m, nil
	case "1h":
		return TF1h, nil
	case "4h":
		return TF4h, nil
	case "1D", "1d":
		return TF1d, nil
	default:
		return 0, fmt.Errorf("unknown timeframe: %q", s)
	}
}

// Candle is one OHLCV bucket. OpenTime is the bucket start in unix seconds.
// Price/Volume use the same raw uint64 units as Match.Price / Match.Quantity.
type Candle struct {
	OpenTime int64  `json:"openTime"`
	Open     uint64 `json:"open"`
	High     uint64 `json:"high"`
	Low      uint64 `json:"low"`
	Close    uint64 `json:"close"`
	Volume   uint64 `json:"volume"`
	Trades   uint32 `json:"trades"`
}

// Seconds returns the match timestamp in unix seconds. The on-wire timestamp is
// nanoseconds (from time.Now().UnixNano()); this is a typed accessor for
// candle bucketing math.
func (m Match) Seconds() int64 {
	return m.Timestamp / 1_000_000_000
}
