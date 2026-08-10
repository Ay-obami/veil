package stress

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"veil/tools/pkg/contracts/veil"
	"veil/tools/pkg/fccutils"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// SweepConfig is what Sweep needs.
type SweepConfig struct {
	ProxyURL          string
	InstructionSender common.Address
	Tokens            []common.Address
}

// withdrawalAuth mirrors the TEE-signed result returned by the WITHDRAW instruction.
type withdrawalAuth struct {
	Token        common.Address `json:"token"`
	Amount       uint64         `json:"amount"`
	To           common.Address `json:"to"`
	WithdrawalID common.Hash    `json:"withdrawalId"`
	Signature    hexutil.Bytes  `json:"signature"`
}

// Sweep cancels every open order and withdraws every non-zero balance for each
// trader. Runs sequentially per trader; parallelism is safe across traders since
// they have distinct EOAs, but sequential keeps logs readable.
func Sweep(traders []*Trader, cfg SweepConfig) {
	for _, t := range traders {
		if err := sweepOne(t, cfg); err != nil {
			logger.Errorf("sweep trader %d (%s): %s", t.Index, t.Addr.Hex(), err)
		}
	}
}

func sweepOne(t *Trader, cfg SweepConfig) error {
	state, err := t.GetMyState(cfg.ProxyURL)
	if err != nil {
		return fmt.Errorf("get state: %w", err)
	}

	// Cancel all open orders.
	for _, o := range state.OpenOrders {
		if _, err := t.CancelOrder(cfg.ProxyURL, o.ID); err != nil {
			logger.Errorf("  cancel %s: %s", o.ID, err)
			continue
		}
		logger.Infof("  cancelled %s", o.ID)
	}

	// Re-fetch to see released balances.
	state, err = t.GetMyState(cfg.ProxyURL)
	if err != nil {
		return fmt.Errorf("get state after cancel: %w", err)
	}

	// Withdraw any non-zero available balance for each known token.
	for _, token := range cfg.Tokens {
		bal, ok := state.Balances[lowerHex(token)]
		if !ok || bal.Available == 0 {
			continue
		}
		if err := withdrawAndExecute(t, cfg.ProxyURL, cfg.InstructionSender, token, bal.Available); err != nil {
			logger.Errorf("  withdraw %s amt=%d: %s", token.Hex(), bal.Available, err)
			continue
		}
		logger.Infof("  withdrew %d of %s", bal.Available, token.Hex())
	}
	return nil
}

// lowerHex returns the lowercased 0x-prefixed hex address — matches the format
// the extension's GET_MY_STATE emits as JSON map keys.
func lowerHex(a common.Address) string {
	return strings.ToLower(a.Hex())
}

func withdrawAndExecute(t *Trader, proxyURL string, instructionSender, token common.Address, amount uint64) error {
	instrID, _, err := instrutils.Withdraw(t.Support, instructionSender, token, new(big.Int).SetUint64(amount), t.Addr)
	if err != nil {
		return fmt.Errorf("withdraw tx: %w", err)
	}
	actionResp, err := fccutils.ActionResult(proxyURL, instrID)
	if err != nil {
		return fmt.Errorf("poll result: %w", err)
	}
	if actionResp.Result.Status != 1 {
		return fmt.Errorf("withdraw rejected: %s", actionResp.Result.Log)
	}

	var wr withdrawalAuth
	if err := json.Unmarshal(actionResp.Result.Data, &wr); err != nil {
		return fmt.Errorf("unmarshal withdraw auth: %w", err)
	}

	sender, err := veil.NewVeilInstructionSender(instructionSender, t.Support.ChainClient)
	if err != nil {
		return fmt.Errorf("bind sender: %w", err)
	}
	opts, err := bind.NewKeyedTransactorWithChainID(t.Support.Prv, t.Support.ChainID)
	if err != nil {
		return fmt.Errorf("transactor: %w", err)
	}
	tx, err := sender.ExecuteWithdrawal(opts, wr.Token, new(big.Int).SetUint64(wr.Amount), wr.To, wr.WithdrawalID, wr.Signature)
	if err != nil {
		return fmt.Errorf("executeWithdrawal: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := bind.WaitMined(ctx, t.Support.ChainClient, tx)
	if err != nil {
		return fmt.Errorf("execute mined: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("executeWithdrawal tx reverted")
	}
	return nil
}
