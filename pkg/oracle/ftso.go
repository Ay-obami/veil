// Package oracle provides Flare FTSO-backed implementations of
// orderbook.PriceOracle.
//
// VERIFICATION STATUS: this file has NOT been run against a live FTSOv2
// contract yet — the ABI call pattern (getFeedById(bytes21) returning
// (uint256 value, int8 decimals, uint64 timestamp)) is confirmed against
// flare-foundation/flare-foundry-starter's src/FtsoExample.sol and
// src/FtsoV2Consumer.sol. The Coston2 FtsoV2 proxy address below is taken
// directly from this repo's config/coston2/deployed-addresses.json. What is
// NOT yet confirmed: the exact feed name Flare uses for XRP (this file
// assumes "XRP/USD" — FAssets' FXRP is 1:1 backed by XRP, so pricing FXRP
// off the XRP/USD feed should be correct, but confirm the feed exists at
// https://dev.flare.network/ftso/feeds before relying on it). Treat
// FTSOPriceOracle as reviewed-but-untested until exercised against Coston2.
package oracle

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// FtsoV2ProxyCoston2 is the FtsoV2 proxy address on Coston2, taken from
// config/coston2/deployed-addresses.json (entry "FtsoV2"). Re-verify this
// on redeploys — Flare's periphery addresses can change between releases.
const FtsoV2ProxyCoston2 = "0xC4e9c78EA53db782E28f28Fdf80BaF59336B304d"

// getFeedByIdABI is the minimal ABI fragment for FtsoV2Interface.getFeedById,
// confirmed against flare-periphery/src/coston2/FtsoV2Interface.sol usage in
// flare-foundry-starter. Kept minimal deliberately — this package has no
// dependency on generated contract bindings.
const getFeedByIdABI = `[{
	"inputs":[{"internalType":"bytes21","name":"_feedId","type":"bytes21"}],
	"name":"getFeedById",
	"outputs":[
		{"internalType":"uint256","name":"_value","type":"uint256"},
		{"internalType":"int8","name":"_decimals","type":"int8"},
		{"internalType":"uint64","name":"_timestamp","type":"uint64"}
	],
	"stateMutability":"view",
	"type":"function"
}]`

// pricePrecision must match internal/extension/handlers.go's pricePrecision
// (the tick scale the orderbook's uint64 Order.Price is stored in). Duplicated
// here rather than imported to keep this package independent of the
// veil module's internal packages; keep the two in sync.
const pricePrecision = 1_000_000

// FTSOPriceOracle implements orderbook.PriceOracle by reading FTSOv2 feed
// values via eth_call and applying a symmetric tolerance band around the
// last observed price. It caches each feed's value for cacheTTL to avoid
// hitting the RPC endpoint on every match attempt.
type FTSOPriceOracle struct {
	client        *ethclient.Client
	ftsoV2Address common.Address
	abi           abi.ABI

	// pairFeedNames maps an orderbook pair (e.g. "FXRP/USDT") to the FTSO
	// feed name to read for its band (e.g. "XRP/USD"). Quote-side ("USDT")
	// is assumed ~1 USD and not separately fed — acceptable slippage for a
	// hackathon MVP with a single stable quote asset; revisit if a
	// non-USD-pegged quote asset is added.
	pairFeedNames map[string]string

	// toleranceBps is the allowed deviation from the FTSO price, in basis
	// points, on each side of the band (e.g. 100 = ±1%).
	toleranceBps uint64

	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]cachedBand
}

type cachedBand struct {
	low, high uint64
	fetchedAt time.Time
}

// NewFTSOPriceOracle constructs an oracle reading from rpcURL. pairFeedNames
// maps orderbook pair names to FTSO feed names (see struct doc).
// toleranceBps is the ± band width in basis points (100 = 1%). cacheTTL of 0
// disables caching (reads on every PriceBand call).
func NewFTSOPriceOracle(
	rpcURL string,
	ftsoV2Address common.Address,
	pairFeedNames map[string]string,
	toleranceBps uint64,
	cacheTTL time.Duration,
) (*FTSOPriceOracle, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("oracle: dial %s: %w", rpcURL, err)
	}
	parsedABI, err := abi.JSON(strings.NewReader(getFeedByIdABI))
	if err != nil {
		return nil, fmt.Errorf("oracle: parse ABI: %w", err)
	}
	return &FTSOPriceOracle{
		client:        client,
		ftsoV2Address: ftsoV2Address,
		abi:           parsedABI,
		pairFeedNames: pairFeedNames,
		toleranceBps:  toleranceBps,
		cacheTTL:      cacheTTL,
		cache:         make(map[string]cachedBand),
	}, nil
}

// PriceBand implements orderbook.PriceOracle. Returns ok=false if the pair
// has no configured feed, or the on-chain read fails — callers (per
// orderbook.priceInBand) treat that as "no band available" and fail open,
// not as "reject everything."
func (o *FTSOPriceOracle) PriceBand(pair string) (low, high uint64, ok bool) {
	feedName, known := o.pairFeedNames[pair]
	if !known {
		return 0, 0, false
	}

	o.mu.Lock()
	if cached, found := o.cache[pair]; found && o.cacheTTL > 0 && time.Since(cached.fetchedAt) < o.cacheTTL {
		o.mu.Unlock()
		return cached.low, cached.high, true
	}
	o.mu.Unlock()

	value, decimals, err := o.readFeed(feedName)
	if err != nil {
		// Deliberately swallow the error here: PriceOracle's contract is
		// (low, high, ok), and a transient RPC failure should degrade to
		// "no band," not panic the matching engine. Callers that need to
		// observe/alert on failures should wrap this oracle and log there.
		return 0, 0, false
	}

	tick := feedValueToTick(value, decimals)
	band := tick * o.toleranceBps / 10_000
	low, high = tick-band, tick+band

	o.mu.Lock()
	o.cache[pair] = cachedBand{low: low, high: high, fetchedAt: time.Now()}
	o.mu.Unlock()

	return low, high, true
}

// readFeed calls getFeedById(EncodeFeedID(feedName)) via eth_call and returns
// the raw (value, decimals) pair as reported by FTSOv2.
func (o *FTSOPriceOracle) readFeed(feedName string) (*big.Int, int8, error) {
	feedID, err := EncodeFeedID(feedName)
	if err != nil {
		return nil, 0, err
	}

	callData, err := o.abi.Pack("getFeedById", feedID)
	if err != nil {
		return nil, 0, fmt.Errorf("oracle: pack getFeedById(%s): %w", feedName, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := o.client.CallContract(ctx, ethereum.CallMsg{
		To:   &o.ftsoV2Address,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("oracle: eth_call getFeedById(%s): %w", feedName, err)
	}

	out, err := o.abi.Unpack("getFeedById", result)
	if err != nil {
		return nil, 0, fmt.Errorf("oracle: unpack getFeedById(%s): %w", feedName, err)
	}
	if len(out) != 3 {
		return nil, 0, fmt.Errorf("oracle: unexpected getFeedById output length %d", len(out))
	}

	value, ok := out[0].(*big.Int)
	if !ok {
		return nil, 0, fmt.Errorf("oracle: unexpected type for _value: %T", out[0])
	}
	decimals, ok := out[1].(int8)
	if !ok {
		return nil, 0, fmt.Errorf("oracle: unexpected type for _decimals: %T", out[1])
	}

	return value, decimals, nil
}

// feedValueToTick rescales an FTSO (value, decimals) pair into the
// orderbook's fixed pricePrecision (1e6) uint64 tick units. FTSO feed
// decimals are typically small (single digits) and value comfortably fits
// uint64 after rescaling for any realistic USD-denominated price — no
// overflow handling beyond a plain conversion is attempted here.
func feedValueToTick(value *big.Int, decimals int8) uint64 {
	// tick = value * pricePrecision / 10^decimals
	scaled := new(big.Int).Mul(value, big.NewInt(pricePrecision))
	if decimals >= 0 {
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
		scaled.Div(scaled, divisor)
	} else {
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-decimals)), nil)
		scaled.Mul(scaled, multiplier)
	}
	return scaled.Uint64()
}

// EncodeFeedID converts a human-readable feed name (e.g. "XRP/USD") into the
// bytes21 identifier FTSOv2 expects: category byte 0x01, followed by the
// ASCII bytes of name, zero-padded to 21 bytes total. Pattern confirmed
// against flare-foundry-starter's hardcoded FLR/USD, BTC/USD, ETH/USD IDs
// (e.g. FLR/USD = 0x01464c522f55534400000000000000000000000000).
func EncodeFeedID(name string) ([21]byte, error) {
	var id [21]byte
	if len(name) > 20 {
		return id, fmt.Errorf("oracle: feed name %q too long for bytes21 encoding", name)
	}
	id[0] = 0x01
	copy(id[1:], []byte(name))
	return id, nil
}
