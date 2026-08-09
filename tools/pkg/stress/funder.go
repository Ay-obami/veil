package stress

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// FundTraders tops up each trader's native balance from the funder wallet so
// they can pay gas for deposits/withdrawals. Idempotent: skips any trader already
// above minNative.
//
// Sequential on purpose: sharing one funder key across concurrent senders races
// on nonces on load-balanced public RPCs (see tools/pkg/utils/erc20.go).
func FundTraders(funder *support.Support, traders []*Trader, perTrader, minNative *big.Int) error {
	ctx := context.Background()

	for _, t := range traders {
		bal, err := funder.ChainClient.BalanceAt(ctx, t.Addr, nil)
		if err != nil {
			return fmt.Errorf("balance for trader %d: %w", t.Index, err)
		}
		if bal.Cmp(minNative) >= 0 {
			logger.Infof("  trader %d (%s): %s wei — skipping", t.Index, t.Addr.Hex(), bal.String())
			continue
		}

		need := new(big.Int).Sub(perTrader, bal)
		if err := sendNative(funder, t.Addr, need); err != nil {
			return fmt.Errorf("funding trader %d: %w", t.Index, err)
		}
		logger.Infof("  trader %d (%s): sent %s wei", t.Index, t.Addr.Hex(), need.String())

		// Pace for load-balanced RPC (same reason as tools/cmd/test-setup/main.go:43).
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func sendNative(s *support.Support, to common.Address, amount *big.Int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	fromAddr := crypto.PubkeyToAddress(s.Prv.PublicKey)
	signer := types.LatestSignerForChainID(s.ChainID)

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		gasPrice, err := s.ChainClient.SuggestGasPrice(ctx)
		if err != nil {
			return fmt.Errorf("suggest gas price: %w", err)
		}
		if attempt > 0 {
			mul := new(big.Int).Mul(gasPrice, big.NewInt(int64(100+20*attempt)))
			gasPrice = new(big.Int).Div(mul, big.NewInt(100))
		}
		nonce, err := s.ChainClient.PendingNonceAt(ctx, fromAddr)
		if err != nil {
			return fmt.Errorf("nonce: %w", err)
		}
		tx := types.NewTransaction(nonce, to, amount, 21_000, gasPrice, nil)
		signed, err := types.SignTx(tx, signer, s.Prv)
		if err != nil {
			return fmt.Errorf("sign: %w", err)
		}
		if err := s.ChainClient.SendTransaction(ctx, signed); err != nil {
			lastErr = err
			if instrutils.IsRetryableTxError(err) && attempt < 2 {
				time.Sleep(2 * time.Second)
				continue
			}
			return fmt.Errorf("send: %w", err)
		}
		if _, err := support.CheckTx(signed, s.ChainClient); err != nil {
			return fmt.Errorf("mined: %w", err)
		}
		return nil
	}
	return fmt.Errorf("exhausted retries: %w", lastErr)
}
