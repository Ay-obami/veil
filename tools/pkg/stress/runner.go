package stress

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// TraderRole distinguishes traders that stop after Duration from those that
// run until SIGINT.
type TraderRole int

const (
	Ephemeral TraderRole = iota
	Persistent
)

// Assignment pairs a trader with a persona and a role.
type Assignment struct {
	Trader  *Trader
	Persona Persona
	Role    TraderRole
}

// RunConfig controls the runner.
type RunConfig struct {
	ProxyURL string
	Duration time.Duration // 0 = perpetual (all traders persistent even if Role==Ephemeral)
	Metrics  *Metrics
	BookCap  *BookCap // optional process-wide per-side resting-order cap; nil = unlimited
}

// Run launches one goroutine per assignment and blocks until:
//   - ctx is cancelled (SIGINT), OR
//   - all ephemeral traders have exited AND no persistent traders exist
//
// If Duration > 0, a timer cancels ephemeral traders but not persistent ones.
func Run(ctx context.Context, assignments []Assignment, cfg RunConfig) {
	epheCtx, cancelEphe := context.WithCancel(ctx)
	defer cancelEphe()

	if cfg.Duration > 0 {
		t := time.AfterFunc(cfg.Duration, func() {
			logger.Infof("duration %s elapsed — stopping ephemeral traders (persistent continue)", cfg.Duration)
			cancelEphe()
		})
		defer t.Stop()
	}

	var wg sync.WaitGroup
	for _, a := range assignments {
		wg.Add(1)
		go func(a Assignment) {
			defer wg.Done()
			activeCtx := ctx
			if a.Role == Ephemeral {
				activeCtx = epheCtx
			}
			runOne(activeCtx, a, cfg)
		}(a)
	}
	wg.Wait()
}

func runOne(ctx context.Context, a Assignment, cfg RunConfig) {
	seed := time.Now().UnixNano() + int64(a.Trader.Index)*1000003
	r := rand.New(rand.NewSource(seed))

	// Small ring of recent order IDs, used for cancel actions.
	recent := make([]string, 0, 16)

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		act := a.Persona.NextAction(r)
		executeAction(a.Trader, cfg, act, &recent)

		pause := a.Persona.PauseAfter(r)
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
	}
}

func executeAction(t *Trader, cfg RunConfig, act Action, recent *[]string) {
	start := time.Now()
	switch act.Kind {
	case "place":
		resp, err := t.PlaceOrder(cfg.ProxyURL, act.Pair, act.Side, act.Type, act.Price, act.Quantity)
		if err != nil {
			cfg.Metrics.RecordError("place_order", classifyErr(err))
			return
		}
		cfg.Metrics.RecordLatency("place_order", time.Since(start))
		if resp.Status == "resting" || resp.Status == "partial" {
			*recent = appendCapped(*recent, resp.OrderID, 16)
			// If the per-side cap is configured, push and evict the oldest
			// over-cap order. Cancel it via its own trader (the EOA that
			// placed it). Latency is measured for the cancel alone.
			if evicted := cfg.BookCap.Push(act.Side, resp.OrderID, t); evicted != nil {
				cancelStart := time.Now()
				if _, err := evicted.trader.CancelOrder(cfg.ProxyURL, evicted.orderID); err != nil {
					cfg.Metrics.RecordError("cancel_order", classifyErr(err))
				} else {
					cfg.Metrics.RecordLatency("cancel_order", time.Since(cancelStart))
				}
			}
		}
	case "cancel":
		if len(*recent) == 0 {
			return
		}
		id := (*recent)[0]
		*recent = (*recent)[1:]
		if _, err := t.CancelOrder(cfg.ProxyURL, id); err != nil {
			cfg.Metrics.RecordError("cancel_order", classifyErr(err))
			return
		}
		cfg.Metrics.RecordLatency("cancel_order", time.Since(start))
		// Persona's own cancel succeeded — drop from BookCap so we don't
		// later try to evict a no-longer-resting order.
		cfg.BookCap.Remove(id)
	}
}

func appendCapped(s []string, v string, cap int) []string {
	s = append(s, v)
	if len(s) > cap {
		s = s[len(s)-cap:]
	}
	return s
}

func classifyErr(err error) string {
	msg := err.Error()
	switch {
	case containsAny(msg, "timeout", "deadline"):
		return "timeout"
	case containsAny(msg, "insufficient balance", "hold"):
		return "insufficient_balance"
	case containsAny(msg, "status not ok", "500", "502", "503"):
		return "server_error"
	default:
		return "other"
	}
}

func containsAny(hay string, needles ...string) bool {
	for _, n := range needles {
		for i := 0; i+len(n) <= len(hay); i++ {
			if hay[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}
