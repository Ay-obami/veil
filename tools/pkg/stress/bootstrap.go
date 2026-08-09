package stress

import (
	"fmt"
	"sync"

	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// BootstrapConfig defines per-trader initial funding amounts in HUMAN token
// units. Each amount is multiplied by 10^decimals of the respective token
// before being sent on-chain / to the TEE.
type BootstrapConfig struct {
	InstructionSender common.Address
	QuoteToken        common.Address
	BaseToken         common.Address
	Scaling           Scaling
	MintHuman         uint64 // human tokens minted per token per trader
	ApproveHuman      uint64 // approval (>= MintHuman)
	DepositHuman      uint64 // human tokens deposited per token per trader
	Concurrency       int    // max concurrent trader bootstraps; 1-8 sensible for Coston2
}

// BootstrapTraders runs mint → approve → deposit for each trader in parallel
// (bounded by cfg.Concurrency). Each trader's actions are sequential from that
// trader's own nonce, so no nonce races across traders.
func BootstrapTraders(traders []*Trader, cfg BootstrapConfig) error {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	errCh := make(chan error, len(traders))

	for _, t := range traders {
		wg.Add(1)
		sem <- struct{}{}
		go func(t *Trader) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := bootstrapOne(t, cfg); err != nil {
				errCh <- fmt.Errorf("trader %d: %w", t.Index, err)
			}
		}(t)
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("bootstrap errors: %v", errs)
	}
	return nil
}

func bootstrapOne(t *Trader, cfg BootstrapConfig) error {
	mintBase := cfg.Scaling.ScaleBaseAmount(cfg.MintHuman)
	mintQuote := cfg.Scaling.ScaleQuoteAmount(cfg.MintHuman)
	approveBase := cfg.Scaling.ScaleBaseAmount(cfg.ApproveHuman)
	approveQuote := cfg.Scaling.ScaleQuoteAmount(cfg.ApproveHuman)
	depositBase := cfg.Scaling.ScaleBaseAmount(cfg.DepositHuman)
	depositQuote := cfg.Scaling.ScaleQuoteAmount(cfg.DepositHuman)

	// Mint quote.
	if err := instrutils.MintERC20(t.Support, cfg.QuoteToken, t.Addr, mintQuote); err != nil {
		return fmt.Errorf("mint quote: %w", err)
	}
	// Mint base.
	if err := instrutils.MintERC20(t.Support, cfg.BaseToken, t.Addr, mintBase); err != nil {
		return fmt.Errorf("mint base: %w", err)
	}
	// Approve quote.
	if err := instrutils.ApproveERC20(t.Support, cfg.QuoteToken, cfg.InstructionSender, approveQuote); err != nil {
		return fmt.Errorf("approve quote: %w", err)
	}
	// Approve base.
	if err := instrutils.ApproveERC20(t.Support, cfg.BaseToken, cfg.InstructionSender, approveBase); err != nil {
		return fmt.Errorf("approve base: %w", err)
	}

	// Deposit both tokens.
	if _, err := t.Deposit(cfg.InstructionSender, cfg.QuoteToken, depositQuote); err != nil {
		return fmt.Errorf("deposit quote: %w", err)
	}
	if _, err := t.Deposit(cfg.InstructionSender, cfg.BaseToken, depositBase); err != nil {
		return fmt.Errorf("deposit base: %w", err)
	}

	logger.Infof("  trader %d (%s) bootstrapped: minted=%d deposited=%d (human tokens, each)",
		t.Index, t.Addr.Hex(), cfg.MintHuman, cfg.DepositHuman)
	return nil
}
