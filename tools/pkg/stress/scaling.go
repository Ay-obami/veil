package stress

import "math/big"

// PricePrecision must match pricePrecision in internal/extension/handlers.go
// and PRICE_PRECISION in frontend/src/lib/price.ts. 6 decimal places of
// price precision (1 raw tick = 0.000001 quote-per-base) so sub-cent assets
// like FLR can have a meaningful book.
const PricePrecision uint64 = 1_000_000

// Scaling converts human-readable order parameters (tokens, quote-per-base
// price) into the raw integer units the TEE extension expects. Matches the
// frontend's usePlaceOrder.ts exactly so stress-test orders appear in the UI
// with the same semantics as a user-placed order.
type Scaling struct {
	BaseDecimals  uint8
	QuoteDecimals uint8
}

// ScaleQty converts a human base-token quantity (e.g. 5 FLR) to raw integer
// base-token units.
func (s Scaling) ScaleQty(human uint64) uint64 {
	return human * pow10U64(s.BaseDecimals)
}

// ScaleQtyMilli converts thousandths-of-base-token (e.g. 5 = 0.005 base tokens)
// to raw integer base-token units. Lets tiers express sub-unit quantities for
// high-priced assets like BTC without pulling in floats.
func (s Scaling) ScaleQtyMilli(milli uint64) uint64 {
	return milli * pow10U64(s.BaseDecimals) / 1000
}

// ScalePrice converts a human price (quote-per-base, e.g. 2 USDT/FLR) to the
// raw integer the TEE stores. See internal/extension/handlers.go pricePrecision
// and frontend/src/hooks/usePlaceOrder.ts for the matching forward/reverse math.
//
//	rawPrice = human * (10^quoteDecimals / 10^baseDecimals) * PricePrecision
func (s Scaling) ScalePrice(human uint64) uint64 {
	q := pow10U64(s.QuoteDecimals)
	b := pow10U64(s.BaseDecimals)
	return human * q / b * PricePrecision
}

// ScalePriceFloat is ScalePrice for a float input — used by price oracles that
// return fractional prices (e.g. CoinGecko gives USD as a float64). The
// conversion formula is the same; floats lose a little precision at very high
// magnitudes but BTC/ETH prices stay well inside float64's exact-integer range.
func (s Scaling) ScalePriceFloat(human float64) uint64 {
	q := float64(pow10U64(s.QuoteDecimals))
	b := float64(pow10U64(s.BaseDecimals))
	return uint64(human * q / b * float64(PricePrecision))
}

// ScaleBaseAmount converts a human amount of base tokens to raw units as *big.Int
// (for on-chain mint / approve / deposit calls).
func (s Scaling) ScaleBaseAmount(human uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(human), pow10Big(s.BaseDecimals))
}

// ScaleQuoteAmount is the quote-token counterpart of ScaleBaseAmount.
func (s Scaling) ScaleQuoteAmount(human uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(human), pow10Big(s.QuoteDecimals))
}

// ScaleBaseAmountU64 is ScaleBaseAmount returning uint64 — for the TEE-side
// DEPOSIT / GET_MY_STATE APIs which use uint64 throughout. Overflows silently
// for impractically large inputs (human >= 2^64 / 10^decimals).
func (s Scaling) ScaleBaseAmountU64(human uint64) uint64 {
	return human * pow10U64(s.BaseDecimals)
}

// ScaleQuoteAmountU64 is the quote-token counterpart of ScaleBaseAmountU64.
func (s Scaling) ScaleQuoteAmountU64(human uint64) uint64 {
	return human * pow10U64(s.QuoteDecimals)
}

func pow10U64(n uint8) uint64 {
	p := uint64(1)
	for i := uint8(0); i < n; i++ {
		p *= 10
	}
	return p
}

func pow10Big(n uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}
