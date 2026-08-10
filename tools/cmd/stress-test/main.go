// stress-test runs parallel mock traders against a deployed orderbook extension.
//
// See docs/superpowers/plans/2026-04-20-orderbook-stress-test.md for design.
//
// Usage:
//
//	go run ./cmd/stress-test -tier=L2 -instructionSender=0x... -duration=5m
//	go run ./cmd/stress-test -tier=L3 -duration=0                 # perpetual
//	go run ./cmd/stress-test -tier=day -log-file=/tmp/soak.log    # multi-hour soak
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"veil/tools/pkg/configs"
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/stress"
	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/joho/godotenv"
)

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	instructionSenderF := flag.String("instructionSender", "", "InstructionSender contract address")
	tierF := flag.String("tier", "L1", "stress tier: L1..L5")
	tradersF := flag.Int("traders", 0, "override tier total (0 = use tier default)")
	durationF := flag.Duration("duration", 0, "0 = perpetual, else e.g. 10m")
	personaMixF := flag.String("persona-mix", "", "override: mm:2,taker:3,walker:5,whale:1,flicker:1")
	keysFileF := flag.String("keys", "./traders.json", "trader keys cache")
	fundPerTraderF := flag.String("fund-per-trader", "50000000000000000", "native wei per trader (default 0.05 FLR)")
	fundMinF := flag.String("fund-min", "10000000000000000", "top up if below this (default 0.01 FLR)")
	mintAmountF := flag.Uint64("mint", 1_000_000, "human tokens to mint per trader per token (scaled by decimals())")
	depositAmountF := flag.Uint64("deposit", 100_000, "human tokens to deposit per trader per token (scaled by decimals())")
	pairF := flag.String("pair", "FLR/USDT", "trading pair")
	logFileF := flag.String("log-file", "", "if set, duplicate all log output to this file (for tail -f on long runs)")
	priceSymbolF := flag.String("price-symbol", "", "CoinGecko asset id (e.g. bitcoin, ethereum); overrides tier's PriceSymbol")
	priceIntervalF := flag.Duration("price-interval", 60*time.Second, "price-oracle poll interval (clamped to >=30s)")
	priceVsCurrencyF := flag.String("price-vs-currency", "usd", "CoinGecko vs_currencies param")
	maxPerSideF := flag.Int("max-per-side", 20, "max resting limit orders per side across the soak; 0 = unlimited")
	flag.Parse()

	if *instructionSenderF == "" {
		fmt.Fprintln(os.Stderr, "--instructionSender is required")
		os.Exit(1)
	}

	if *logFileF != "" {
		logger.Set(logger.Config{Level: "INFO", Console: true, File: *logFileF})
		logger.Infof("logging to %s", *logFileF)
	}

	_ = godotenv.Load(filepath.Join(configs.NetworkConfigDir(*af), "test-tokens.env"))
	quoteTokenStr := os.Getenv("QUOTE_TOKEN")
	if quoteTokenStr == "" {
		fmt.Fprintln(os.Stderr, "QUOTE_TOKEN env var must be set — run test-setup first")
		os.Exit(1)
	}
	// Resolve base token by -pair flag. The pair's base-symbol (text before the
	// "/") selects BASE_TOKEN_<SYMBOL> written by test-setup. Falls back to
	// BASE_TOKEN (legacy single-pair setup) for backwards compat.
	baseSymbol := strings.ToUpper(strings.SplitN(*pairF, "/", 2)[0])
	baseTokenStr := os.Getenv("BASE_TOKEN_" + baseSymbol)
	if baseTokenStr == "" {
		baseTokenStr = os.Getenv("BASE_TOKEN")
	}
	if baseTokenStr == "" {
		fmt.Fprintf(os.Stderr, "BASE_TOKEN_%s (or BASE_TOKEN) env var must be set — run test-setup first\n", baseSymbol)
		os.Exit(1)
	}
	quoteToken := common.HexToAddress(quoteTokenStr)
	baseToken := common.HexToAddress(baseTokenStr)
	instructionSender := common.HexToAddress(*instructionSenderF)

	// --- Resolve tier + persona mix ---
	tier, err := tierByName(*tierF)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	if *personaMixF != "" {
		m, err := ParseMixOverride(*personaMixF)
		if err != nil {
			fccutils.FatalWithCause(fmt.Errorf("parsing persona-mix: %w", err))
		}
		tier.Mix = m
	}
	if *durationF != 0 {
		tier.Duration = *durationF
	} else {
		tier.Duration = 0
	}
	totalTraders := tier.Mix.Total()
	if *tradersF > 0 {
		totalTraders = *tradersF
	}

	logger.Infof("tier=%s total_traders=%d duration=%s pair=%s", tier.Name, totalTraders, tier.Duration, *pairF)

	// --- Load funder support + keys ---
	funder, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	keys, err := stress.GenerateOrLoadTraderKeys(totalTraders, *keysFileF)
	if err != nil {
		fccutils.FatalWithCause(err)
	}

	// --- Query token decimals so every human-unit amount below can be scaled
	// to the raw integer units the TEE and ERC20 contracts expect. Matches
	// frontend/src/hooks/usePlaceOrder.ts; without this, orders placed by the
	// stress test round to 0 in the UI. ---
	baseDecimals, err := instrutils.DecimalsERC20(funder, baseToken)
	if err != nil {
		fccutils.FatalWithCause(fmt.Errorf("reading base token decimals: %w", err))
	}
	quoteDecimals, err := instrutils.DecimalsERC20(funder, quoteToken)
	if err != nil {
		fccutils.FatalWithCause(fmt.Errorf("reading quote token decimals: %w", err))
	}
	scaling := stress.Scaling{BaseDecimals: baseDecimals, QuoteDecimals: quoteDecimals}
	logger.Infof("scaling: base_decimals=%d quote_decimals=%d price_precision=%d",
		baseDecimals, quoteDecimals, stress.PricePrecision)

	// --- Build Trader objects ---
	traders := make([]*stress.Trader, totalTraders)
	for i := 0; i < totalTraders; i++ {
		tr, err := stress.NewTrader(i, keys[i], funder.ChainClient, funder.Addresses)
		if err != nil {
			fccutils.FatalWithCause(fmt.Errorf("trader %d: %w", i, err))
		}
		traders[i] = tr
	}

	// --- Fund traders with native gas ---
	logger.Infof("funding %d traders with native gas...", totalTraders)
	perTrader, _ := new(big.Int).SetString(*fundPerTraderF, 10)
	minNative, _ := new(big.Int).SetString(*fundMinF, 10)
	if err := stress.FundTraders(funder, traders, perTrader, minNative); err != nil {
		fccutils.FatalWithCause(err)
	}

	// --- Cleanup stale open orders from any interrupted previous run ---
	logger.Infof("cleanup: checking for stale open orders on %d traders...", totalTraders)
	stress.CleanupOpenOrders(traders, *pf)

	// --- Bootstrap (mint + approve + deposit) ---
	logger.Infof("bootstrapping %d traders (mint+approve+deposit)...", totalTraders)
	bcfg := stress.BootstrapConfig{
		InstructionSender: instructionSender,
		QuoteToken:        quoteToken,
		BaseToken:         baseToken,
		Scaling:           scaling,
		MintHuman:         *mintAmountF,
		ApproveHuman:      *mintAmountF,
		DepositHuman:      *depositAmountF,
		Concurrency:       8,
	}
	if err := stress.BootstrapTraders(traders, bcfg); err != nil {
		fccutils.FatalWithCause(err)
	}

	// --- Optional price oracle (CoinGecko) ---
	// CLI flag overrides the tier's PriceSymbol; pass -price-symbol="" to
	// force a tier like btc-day into static pricing for tests.
	priceSymbol := tier.PriceSymbol
	if *priceSymbolF != "" {
		priceSymbol = *priceSymbolF
	}
	oracleCtx, oracleCancel := context.WithCancel(context.Background())
	defer oracleCancel()
	var oracle stress.PriceOracle
	if priceSymbol != "" {
		o, err := stress.NewCoinGeckoOracle(oracleCtx, stress.CoinGeckoConfig{
			Symbol: priceSymbol, VsCurrency: *priceVsCurrencyF,
			Interval: *priceIntervalF, Scaling: scaling,
		})
		if err != nil {
			fccutils.FatalWithCause(fmt.Errorf("price oracle: %w", err))
		}
		oracle = o
	}

	// --- Build assignments from tier config ---
	assignments := BuildAssignments(tier, traders, *pairF, tier.Duration, scaling, oracle)
	if len(assignments) == 0 {
		fccutils.FatalWithCause(fmt.Errorf("no assignments — check persona mix"))
	}

	// --- Run ---
	metrics := stress.NewMetrics()
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		logger.Infof("%s received — cancelling all traders", s)
		cancel()
	}()

	// Periodic metrics reporters:
	//   - Compact one-liner every 60s: tight, scannable in a long log file.
	//   - Full per-action snapshot every 5 min: for deeper inspection.
	compactTicker := time.NewTicker(60 * time.Second)
	verboseTicker := time.NewTicker(5 * time.Minute)
	defer compactTicker.Stop()
	defer verboseTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-compactTicker.C:
				printCompact(metrics.Snapshot())
			case <-verboseTicker.C:
				printSnapshot(metrics.Snapshot())
			}
		}
	}()

	start := time.Now()
	logger.Infof("starting run (persistent traders run until SIGINT even if duration elapses)")
	stress.Run(ctx, assignments, stress.RunConfig{
		ProxyURL: *pf,
		Duration: tier.Duration,
		Metrics:  metrics,
		BookCap:  stress.NewBookCap(*maxPerSideF),
	})
	logger.Infof("run loop exited after %s", time.Since(start))

	// --- Final sweep (always runs, even on SIGINT) ---
	logger.Infof("sweeping open orders and withdrawing balances...")
	stress.Sweep(traders, stress.SweepConfig{
		ProxyURL:          *pf,
		InstructionSender: instructionSender,
		Tokens:            []common.Address{quoteToken, baseToken},
	})

	// --- Final metrics ---
	logger.Infof("")
	logger.Infof("=== FINAL METRICS ===")
	printSnapshot(metrics.Snapshot())
}

func printSnapshot(s stress.MetricsSnapshot) {
	for action, st := range s.Actions {
		logger.Infof("  %-14s count=%d p50=%s p95=%s p99=%s errors=%v (rate=%.3f)",
			action, st.Count, st.P50, st.P95, st.P99, st.Errors, st.ErrorRate)
	}
}

// printCompact emits a single line summarizing each action — meant for hours-
// long soak runs where verbose snapshots clutter the log.
func printCompact(s stress.MetricsSnapshot) {
	if len(s.Actions) == 0 {
		return
	}
	parts := make([]string, 0, len(s.Actions))
	for action, st := range s.Actions {
		parts = append(parts, fmt.Sprintf("%s[ok=%d p50=%s p95=%s err=%.0f%%]",
			action, st.Count, st.P50.Round(time.Millisecond), st.P95.Round(time.Millisecond), st.ErrorRate*100))
	}
	logger.Infof("status %s", joinParts(parts))
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " | "
		}
		out += p
	}
	return out
}
