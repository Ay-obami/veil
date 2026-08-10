// test-withdraw tests the 2-step withdrawal flow.
//
// Steps:
//  1. GET_MY_STATE — check available balance
//  2. Send withdraw instruction on-chain
//  3. Poll proxy for TEE-signed result
//  4. Call executeWithdrawal with signed params
//  5. Verify on-chain token balance increased
//  6. GET_MY_STATE — verify internal balance decreased
//
// Usage:
//
//	go run ./cmd/test-withdraw -a <addresses-file> -c <chain-url> -p <proxy-url> -instructionSender <addr>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"veil/tools/pkg/configs"
	"veil/tools/pkg/contracts/veil"
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/joho/godotenv"
)

const withdrawAmount = 100

type getMyStateReq struct {
	Sender string `json:"sender"`
}
type tokenBalance struct {
	Available uint64 `json:"available"`
	Held      uint64 `json:"held"`
}
type getMyStateResp struct {
	Balances map[common.Address]tokenBalance `json:"balances"`
}

type withdrawResp struct {
	Token        common.Address `json:"token"`
	Amount       uint64         `json:"amount"`
	To           common.Address `json:"to"`
	WithdrawalID common.Hash    `json:"withdrawalId"`
	Signature    hexutil.Bytes  `json:"signature"`
	Available    uint64         `json:"available"`
}

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	instructionSenderF := flag.String("instructionSender", "", "InstructionSender contract address")
	flag.Parse()

	if *instructionSenderF == "" {
		logger.Errorf("--instructionSender is required")
		os.Exit(1)
	}

	_ = godotenv.Load(filepath.Join(configs.NetworkConfigDir(*af), "test-tokens.env"))
	quoteTokenStr := os.Getenv("QUOTE_TOKEN")
	if quoteTokenStr == "" {
		logger.Errorf("QUOTE_TOKEN not set. Run test-setup first.")
		os.Exit(1)
	}

	instructionSenderAddr := common.HexToAddress(*instructionSenderF)
	quoteToken := common.HexToAddress(quoteTokenStr)

	s, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}

	deployer := crypto.PubkeyToAddress(s.Prv.PublicKey)
	proxyURL := *pf
	passed := 0
	failed := 0

	run := func(name string, fn func() error) {
		logger.Infof("TEST: %s", name)
		if err := fn(); err != nil {
			logger.Errorf("  FAIL: %s", err)
			failed++
		} else {
			logger.Infof("  PASS")
			passed++
		}
	}

	// --- Test 1: Check balance before withdrawal ---
	var balanceBefore uint64
	run("GET_MY_STATE — check balance before withdrawal", func() error {
		var resp getMyStateResp
		err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{
			Sender: strings.ToLower(deployer.Hex()),
		}, &resp)
		if err != nil {
			return err
		}
		bal, ok := resp.Balances[quoteToken]
		if !ok || bal.Available == 0 {
			return fmt.Errorf("no available quote token balance — run test-deposit first")
		}
		balanceBefore = bal.Available
		logger.Infof("  Quote token available: %d", balanceBefore)
		return nil
	})

	if failed > 0 {
		logger.Infof("Results: %d passed, %d failed", passed, failed)
		os.Exit(1)
	}

	// --- Test 2: Send withdraw + get TEE signature ---
	var wr withdrawResp
	run("Withdraw — send instruction and get TEE signature", func() error {
		amount := big.NewInt(withdrawAmount)
		instructionID, _, err := instrutils.Withdraw(s, instructionSenderAddr, quoteToken, amount, deployer)
		if err != nil {
			return fmt.Errorf("sending withdraw tx: %w", err)
		}
		logger.Infof("  Instruction ID: %s", instructionID.Hex())

		actionResp, err := fccutils.ActionResult(proxyURL, instructionID)
		if err != nil {
			return fmt.Errorf("polling result: %w", err)
		}
		if actionResp.Result.Status != 1 {
			return fmt.Errorf("expected status 1, got %d: %s", actionResp.Result.Status, actionResp.Result.Log)
		}

		if err := json.Unmarshal(actionResp.Result.Data, &wr); err != nil {
			return fmt.Errorf("unmarshaling response: %w", err)
		}
		if wr.Amount != withdrawAmount {
			return fmt.Errorf("expected amount %d, got %d", withdrawAmount, wr.Amount)
		}
		if len(wr.Signature) == 0 {
			return fmt.Errorf("expected TEE signature, got empty")
		}
		logger.Infof("  Got TEE signature (%d bytes), withdrawalId=%s", len(wr.Signature), wr.WithdrawalID.Hex())
		return nil
	})

	if failed > 0 {
		logger.Infof("Results: %d passed, %d failed", passed, failed)
		os.Exit(1)
	}

	// --- Test 3: Execute withdrawal on-chain ---
	run("Execute withdrawal with TEE signature", func() error {
		// Get on-chain balance before.
		balBefore, err := instrutils.BalanceOfERC20(s, quoteToken, deployer)
		if err != nil {
			return fmt.Errorf("balanceOf before: %w", err)
		}

		sender, err := veil.NewVeilInstructionSender(instructionSenderAddr, s.ChainClient)
		if err != nil {
			return fmt.Errorf("binding contract: %w", err)
		}

		opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
		if err != nil {
			return fmt.Errorf("creating transactor: %w", err)
		}

		tx, err := sender.ExecuteWithdrawal(opts, wr.Token, big.NewInt(int64(wr.Amount)), wr.To, wr.WithdrawalID, wr.Signature)
		if err != nil {
			return fmt.Errorf("executeWithdrawal: %w", err)
		}

		_, err = support.CheckTx(tx, s.ChainClient)
		if err != nil {
			return fmt.Errorf("executeWithdrawal tx failed: %w", err)
		}

		// Get on-chain balance after.
		balAfter, err := instrutils.BalanceOfERC20(s, quoteToken, deployer)
		if err != nil {
			return fmt.Errorf("balanceOf after: %w", err)
		}

		diff := new(big.Int).Sub(balAfter, balBefore)
		if diff.Cmp(big.NewInt(withdrawAmount)) != 0 {
			return fmt.Errorf("expected on-chain balance increase of %d, got %s", withdrawAmount, diff.String())
		}
		logger.Infof("  On-chain balance increased by %s", diff.String())
		return nil
	})

	// --- Test 4: Verify internal balance decreased ---
	run("GET_MY_STATE — balance decreased after withdrawal", func() error {
		var resp getMyStateResp
		err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{
			Sender: strings.ToLower(deployer.Hex()),
		}, &resp)
		if err != nil {
			return err
		}
		bal := resp.Balances[quoteToken]
		expected := balanceBefore - withdrawAmount
		if bal.Available != expected {
			return fmt.Errorf("expected available=%d, got %d", expected, bal.Available)
		}
		logger.Infof("  Internal balance: %d (was %d, withdrew %d)", bal.Available, balanceBefore, withdrawAmount)
		return nil
	})

	// --- Summary ---
	logger.Infof("")
	logger.Infof("Results: %d passed, %d failed", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
