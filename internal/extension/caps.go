package extension

// In-memory size caps. The orderbook is a demo: histories are lossy by design.
// Tests can override these (var, not const).
var (
	MaxMatchesPerPair       = 400 // ring per pair
	MaxLevelsPerSide        = 200 // distinct price levels per side, per pair
	MaxOrdersPerUser        = 100 // open orders per user (PLACE_ORDER rejected over)
	MaxUserHistoryMatches   = 200 // ring per user
	MaxUserHistoryOrders    = 200 // ring per user
	MaxUserDepositsHistory  = 200
	MaxUserWithdrawsHistory = 200
	MaxCandlesPerTF         = 1000 // ring per (pair, timeframe)
	DefaultBookMatchLimit   = 100  // GET_BOOK_STATE default match cap
	DefaultCandleLimit      = 240  // GET_CANDLES default
)
