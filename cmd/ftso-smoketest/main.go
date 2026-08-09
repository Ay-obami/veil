// Command ftso-smoketest exercises pkg/oracle.FTSOPriceOracle against a live
// FTSOv2 contract, without starting the full extension server. Use this to
// answer exactly one question: does getFeedById() actually return sane data
// for the feed names we're assuming exist (see BUILD_NOTES.md's open item
// on "XRP/USD" being unconfirmed).
//
// Usage:
//
//	go run ./cmd/ftso-smoketest -rpc https://coston2-api.flare.network/ext/C/rpc \
//	    -address 0xC4e9c78EA53db782E28f28Fdf80BaF59336B304d \
//	    -feeds "FLR/USD,BTC/USD,ETH/USD,XRP/USD"
//
// A working feed prints its resolved price band. A missing/misnamed feed
// will show up as ok=false, or an error from the eth_call — either way you
// find out in seconds rather than by starting the whole extension and
// grepping startup logs.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"veil/pkg/oracle"
)

func main() {
	rpcURL := flag.String("rpc", "https://coston2-api.flare.network/ext/C/rpc", "Flare RPC endpoint")
	ftsoAddr := flag.String("address", oracle.FtsoV2ProxyCoston2, "FtsoV2 proxy address")
	feedsFlag := flag.String("feeds", "FLR/USD,BTC/USD,ETH/USD,XRP/USD", "comma-separated feed names to test")
	toleranceBps := flag.Uint64("tolerance-bps", 100, "band tolerance in basis points")
	flag.Parse()

	feedNames := strings.Split(*feedsFlag, ",")
	// PriceBand() takes a pair name, not a feed name directly, so map each
	// feed to a synthetic 1:1 pair for this standalone test only.
	pairFeedNames := make(map[string]string, len(feedNames))
	for _, f := range feedNames {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		pairFeedNames[f] = f // pair name == feed name here, smoke-test only
	}

	fmt.Printf("RPC:      %s\n", *rpcURL)
	fmt.Printf("FtsoV2:   %s\n", *ftsoAddr)
	fmt.Printf("Feeds:    %v\n", feedNames)
	fmt.Println(strings.Repeat("-", 50))

	priceOracle, err := oracle.NewFTSOPriceOracle(
		*rpcURL,
		common.HexToAddress(*ftsoAddr),
		pairFeedNames,
		*toleranceBps,
		0, // no caching — we want a fresh read every time for this test
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED to construct oracle: %v\n", err)
		os.Exit(1)
	}

	failures := 0
	for _, f := range feedNames {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		start := time.Now()
		low, high, ok := priceOracle.PriceBand(f)
		elapsed := time.Since(start)
		if !ok {
			fmt.Printf("%-10s FAILED (ok=false — feed missing, stale, or eth_call error; add fmt logging inside pkg/oracle/ftso.go's readFeed to see the underlying error if this happens)  [%s]\n", f, elapsed)
			failures++
			continue
		}
		// tick units are 1e6-scaled (pricePrecision), so divide back down
		// to a human-readable decimal for this printout only.
		fmt.Printf("%-10s OK   band=[%.6f, %.6f]  [%s]\n", f, float64(low)/1_000_000, float64(high)/1_000_000, elapsed)
	}

	fmt.Println(strings.Repeat("-", 50))
	if failures > 0 {
		fmt.Printf("%d/%d feeds failed\n", failures, len(feedNames))
		os.Exit(1)
	}
	fmt.Println("all feeds OK")
}
