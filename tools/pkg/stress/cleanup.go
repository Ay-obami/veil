package stress

import (
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// CleanupOpenOrders cancels every open order for each trader at startup.
// Recovers cleanly from a previous run that was SIGKILLed before its sweep
// could finish — otherwise stale orders would accumulate in the TEE across
// runs and crowd out or cross new MM quotes.
//
// Errors are logged but not returned: an order might have filled between the
// GetMyState read and the CancelOrder write, which is benign. The operation
// is best-effort, not transactional.
func CleanupOpenOrders(traders []*Trader, proxyURL string) {
	total := 0
	for _, t := range traders {
		state, err := t.GetMyState(proxyURL)
		if err != nil {
			logger.Errorf("  cleanup trader %d: get state: %s", t.Index, err)
			continue
		}
		if len(state.OpenOrders) == 0 {
			continue
		}
		cancelled := 0
		for _, o := range state.OpenOrders {
			if _, err := t.CancelOrder(proxyURL, o.ID); err == nil {
				cancelled++
			}
		}
		if cancelled > 0 {
			logger.Infof("  cleanup trader %d (%s): cancelled %d stale orders", t.Index, t.Addr.Hex(), cancelled)
			total += cancelled
		}
	}
	if total > 0 {
		logger.Infof("cleanup: %d stale orders cancelled across all traders", total)
	}
}
