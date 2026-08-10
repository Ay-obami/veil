// Package config contains configuration values and defaults used by the extension.
package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

const (
	Version = "0.1.0"

	OPTypeOrderbook                = "ORDERBOOK"
	OPCommandDeposit               = "DEPOSIT"
	OPCommandWithdraw              = "WITHDRAW"
	OPCommandPlaceOrder            = "PLACE_ORDER"
	OPCommandCancelOrder           = "CANCEL_ORDER"
	OPCommandGetMyState            = "GET_MY_STATE"
	OPCommandGetSolvencyCommitment = "GET_SOLVENCY_COMMITMENT" // new for Veil, not in the base fce-orderbook
	OPCommandGetBookState          = "GET_BOOK_STATE"
	OPCommandGetCandles            = "GET_CANDLES"
	OPCommandExportHistory         = "EXPORT_HISTORY"

	TimeoutShutdown = 5 * time.Second
)

// Defaults.
var (
	ExtensionPort   = 8080
	SignPort        = 9090
	TypesServerPort = 8100
	AdminAddresses  []string
	BalancesPath    string // optional: path to persist balance manager state

	// FTSO price-band oracle settings (see pkg/oracle.FTSOPriceOracle).
	// Empty FtsoRPCURL disables the oracle entirely — the extension falls
	// back to unrestricted matching (equivalent to the base fce-orderbook
	// behavior) rather than failing to start. Set FTSO_RPC_URL to enable.
	FtsoRPCURL          string       // e.g. "https://coston2-api.flare.network/ext/C/rpc"
	FtsoV2Address       string       // FtsoV2 proxy address; defaults to Coston2's if unset and RPC is set
	FtsoToleranceBps    uint64 = 100 // default ±1% band
	FtsoCacheTTLSeconds int    = 5   // default 5s cache on feed reads

	// SolvencyVerificationKeyPath points at the verification_key.json
	// produced by circuits/package.json's export:vkey script (see
	// circuits/artifacts/README.md). Defaults to the real, committed
	// path — solvency proof verification is opt-OUT (set
	// SOLVENCY_VK_PATH="" to disable), unlike the FTSO oracle above,
	// since the verification key is now a genuine part of this repo, not
	// optional infrastructure someone has to stand up. Still fails open at
	// startup if the file is missing/invalid, rather than crashing —
	// same philosophy as the FTSO oracle, just a different default.
	SolvencyVerificationKeyPath = "circuits/artifacts/verification_key.json"
)

// TradingPairConfig maps a pair name to its base and quote token addresses.
type TradingPairConfig struct {
	Name       string         `json:"name"`
	BaseToken  common.Address `json:"baseToken"`
	QuoteToken common.Address `json:"quoteToken"`
	// FtsoFeed is the FTSO feed name to price this pair's base asset against
	// (e.g. "XRP/USD"), used for the price-band check (see pkg/oracle).
	// Optional — empty means no band enforcement for this pair, matching
	// the base fce-orderbook behavior. Quote-side is assumed ~1 USD; see
	// pkg/oracle.FTSOPriceOracle's doc comment for that caveat.
	FtsoFeed string `json:"ftsoFeed,omitempty"`
}

// LoadTradingPairs reads a JSON file of trading pair configs.
func LoadTradingPairs(path string) ([]TradingPairConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pairs []TradingPairConfig
	if err := json.Unmarshal(data, &pairs); err != nil {
		return nil, err
	}
	return pairs, nil
}

// Environment variables override defaults.
func init() {
	if v := os.Getenv("EXTENSION_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ExtensionPort = n
		}
	}
	if v := os.Getenv("SIGN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			SignPort = n
		}
	}
	if v := os.Getenv("TYPES_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			TypesServerPort = n
		}
	}
	if v := os.Getenv("BALANCES_PATH"); v != "" {
		BalancesPath = v
	}
	if v := os.Getenv("ADMIN_ADDRESSES"); v != "" {
		for _, addr := range strings.Split(v, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				AdminAddresses = append(AdminAddresses, strings.ToLower(addr))
			}
		}
	}

	if v := os.Getenv("FTSO_RPC_URL"); v != "" {
		FtsoRPCURL = v
	}
	if v := os.Getenv("FTSO_V2_ADDRESS"); v != "" {
		FtsoV2Address = v
	}
	if v := os.Getenv("FTSO_TOLERANCE_BPS"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			FtsoToleranceBps = n
		}
	}
	if v := os.Getenv("FTSO_CACHE_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			FtsoCacheTTLSeconds = n
		}
	}
	// LookupEnv, not Getenv: SOLVENCY_VK_PATH="" (explicitly set, empty)
	// must be able to disable the feature, which a plain `v != ""` check
	// (as used for the other overrides above) couldn't distinguish from
	// "not set at all."
	if v, ok := os.LookupEnv("SOLVENCY_VK_PATH"); ok {
		SolvencyVerificationKeyPath = v
	}

	// Load trading pairs from config file if it exists.
	pairsPath := os.Getenv("PAIRS_CONFIG")
	if pairsPath == "" {
		pairsPath = "config/pairs.json"
	}
	pairs, err := LoadTradingPairs(pairsPath)
	if err != nil {
		logger.Infof("no trading pairs config loaded from %s: %v (will use defaults)", pairsPath, err)
		return
	}
	TradingPairs = pairs
}

// TradingPairs is the list of configured trading pairs, loaded at init.
var TradingPairs []TradingPairConfig
