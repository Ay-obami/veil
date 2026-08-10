package stress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// PriceOracle exposes a current mid-price in raw TEE units (quote-per-base
// scaled by the relevant decimals). Reads are lock-free so personas can call
// Mid() on every order without contention.
type PriceOracle interface {
	Mid() uint64
	Symbol() string
}

// --- Static oracle (no polling). Useful for tests and back-compat. ---

type StaticOracle struct {
	symbol string
	mid    uint64
}

func NewStaticOracle(symbol string, mid uint64) *StaticOracle {
	return &StaticOracle{symbol: symbol, mid: mid}
}

func (o *StaticOracle) Mid() uint64    { return o.mid }
func (o *StaticOracle) Symbol() string { return o.symbol }

// --- CoinGecko oracle ---

// CoinGeckoConfig controls the polling oracle.
type CoinGeckoConfig struct {
	// Symbol is the CoinGecko asset id, e.g. "bitcoin", "ethereum".
	Symbol string
	// VsCurrency is the quote currency, e.g. "usd". CoinGecko lowercases it
	// internally so any case works.
	VsCurrency string
	// Interval between polls. Clamped to ≥ 30s to stay under the free tier's
	// 30-req/min quota with headroom.
	Interval time.Duration
	// Scaling used to convert the fetched float price to raw TEE units.
	Scaling Scaling
	// HTTPClient is injectable for tests. nil → http.DefaultClient with a 10s
	// per-request timeout.
	HTTPClient *http.Client
}

type coinGeckoOracle struct {
	cfg    CoinGeckoConfig
	mid    atomic.Uint64 // raw-TEE-units; 0 until first successful poll
	client *http.Client
}

// NewCoinGeckoOracle constructs the oracle, performs one synchronous initial
// poll (so callers never see mid=0), then launches a background goroutine that
// refreshes at cfg.Interval until ctx is cancelled.
func NewCoinGeckoOracle(ctx context.Context, cfg CoinGeckoConfig) (PriceOracle, error) {
	if cfg.Symbol == "" {
		return nil, fmt.Errorf("coingecko: Symbol is required")
	}
	if cfg.VsCurrency == "" {
		cfg.VsCurrency = "usd"
	}
	if cfg.Interval < 30*time.Second {
		cfg.Interval = 30 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	o := &coinGeckoOracle{cfg: cfg, client: client}

	// Synchronous first poll so we never trade against mid=0.
	price, err := o.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("coingecko initial poll: %w", err)
	}
	raw := cfg.Scaling.ScalePriceFloat(price)
	o.mid.Store(raw)
	logger.Infof("oracle %s/%s: initial price=%.4f raw_mid=%d", cfg.Symbol, cfg.VsCurrency, price, raw)

	go o.loop(ctx)
	return o, nil
}

func (o *coinGeckoOracle) Mid() uint64    { return o.mid.Load() }
func (o *coinGeckoOracle) Symbol() string { return o.cfg.Symbol }

func (o *coinGeckoOracle) loop(ctx context.Context) {
	t := time.NewTicker(o.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			price, err := o.fetch(ctx)
			if err != nil {
				logger.Warnf("oracle %s: poll failed (keeping last mid): %s", o.cfg.Symbol, err)
				continue
			}
			raw := o.cfg.Scaling.ScalePriceFloat(price)
			o.mid.Store(raw)
			logger.Infof("oracle %s: price=%.4f %s raw_mid=%d", o.cfg.Symbol, price, o.cfg.VsCurrency, raw)
		}
	}
}

func (o *coinGeckoOracle) fetch(ctx context.Context) (float64, error) {
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=%s",
		o.cfg.Symbol, o.cfg.VsCurrency)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	// Response shape: {"bitcoin":{"usd":68432.5}}
	var body map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	entry, ok := body[o.cfg.Symbol]
	if !ok {
		return 0, fmt.Errorf("symbol %q missing in response", o.cfg.Symbol)
	}
	price, ok := entry[o.cfg.VsCurrency]
	if !ok {
		return 0, fmt.Errorf("currency %q missing in response for %s", o.cfg.VsCurrency, o.cfg.Symbol)
	}
	if price <= 0 {
		return 0, fmt.Errorf("non-positive price %f", price)
	}
	return price, nil
}
