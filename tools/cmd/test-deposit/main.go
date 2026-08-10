// test-deposit tests the on-chain deposit flow.
//
// Steps:
//  1. Deposit quote token (USDT) via InstructionSender.deposit()
//  2. Poll proxy for result, verify success
//  3. Deposit base token (FLR)
//  4. Verify balances via GET_MY_STATE direct instruction
//
// Usage:
//
//	go run ./cmd/test-deposit -a <addresses-file> -c <chain-url> -p <proxy-url> -instructionSender <addr>
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
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/joho/godotenv"
)

const depositAmount = 10_000

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

	// Load test-tokens.env for QUOTE_TOKEN and BASE_TOKEN (per-chain).
	_ = godotenv.Load(filepath.Join(configs.NetworkConfigDir(*af), "test-tokens.env"))
	quoteTokenStr := os.Getenv("QUOTE_TOKEN")
	baseTokenStr := os.Getenv("BASE_TOKEN")
	if quoteTokenStr == "" || baseTokenStr == "" {
		logger.Errorf("QUOTE_TOKEN and BASE_TOKEN must be set. Run test-setup first.")
		os.Exit(1)
	}

	instructionSenderAddr := common.HexToAddress(*instructionSenderF)
	quoteToken := common.HexToAddress(quoteTokenStr)
	baseToken := common.HexToAddress(baseTokenStr)

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

	// --- Test 1: Deposit quote token ---
	run("Deposit quote token (TUSDT)", func() error {
		amount := big.NewInt(depositAmount)
		instructionID, _, err := instrutils.Deposit(s, instructionSenderAddr, quoteToken, amount)
		if err != nil {
			return fmt.Errorf("sending deposit tx: %w", err)
		}
		logger.Infof("  Instruction ID: %s", instructionID.Hex())

		resp, err := fccutils.ActionResult(proxyURL, instructionID)
		if err != nil {
			return fmt.Errorf("polling result: %w", err)
		}
		if resp.Result.Status != 1 {
			return fmt.Errorf("expected status 1, got %d: %s", resp.Result.Status, resp.Result.Log)
		}

		var deposit struct {
			Amount    uint64 `json:"amount"`
			Available uint64 `json:"available"`
		}
		if err := json.Unmarshal(resp.Result.Data, &deposit); err != nil {
			return fmt.Errorf("unmarshaling response: %w", err)
		}
		if deposit.Amount != depositAmount {
			return fmt.Errorf("expected deposit amount %d, got %d", depositAmount, deposit.Amount)
		}
		logger.Infof("  Deposited %d, available: %d", deposit.Amount, deposit.Available)
		return nil
	})

	// --- Test 2: Deposit base token ---
	run("Deposit base token (TFLR)", func() error {
		amount := big.NewInt(depositAmount)
		instructionID, _, err := instrutils.Deposit(s, instructionSenderAddr, baseToken, amount)
		if err != nil {
			return fmt.Errorf("sending deposit tx: %w", err)
		}
		logger.Infof("  Instruction ID: %s", instructionID.Hex())

		resp, err := fccutils.ActionResult(proxyURL, instructionID)
		if err != nil {
			return fmt.Errorf("polling result: %w", err)
		}
		if resp.Result.Status != 1 {
			return fmt.Errorf("expected status 1, got %d: %s", resp.Result.Status, resp.Result.Log)
		}
		logger.Infof("  Deposit successful")
		return nil
	})

	// --- Test 3: Verify balances via GET_MY_STATE ---
	run("Verify balances via GET_MY_STATE", func() error {
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

		var resp getMyStateResp
		err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{
			Sender: strings.ToLower(deployer.Hex()),
		}, &resp)
		if err != nil {
			return fmt.Errorf("GET_MY_STATE: %w", err)
		}

		quoteBal, ok := resp.Balances[quoteToken]
		if !ok {
			return fmt.Errorf("no balance for quote token %s", quoteToken.Hex())
		}
		if quoteBal.Available == 0 {
			return fmt.Errorf("expected non-zero quote token balance, got 0")
		}
		logger.Infof("  Quote balance: available=%d held=%d", quoteBal.Available, quoteBal.Held)

		baseBal, ok := resp.Balances[baseToken]
		if !ok {
			return fmt.Errorf("no balance for base token %s", baseToken.Hex())
		}
		if baseBal.Available == 0 {
			return fmt.Errorf("expected non-zero base token balance, got 0")
		}
		logger.Infof("  Base balance: available=%d held=%d", baseBal.Available, baseBal.Held)

		return nil
	})

	// --- Summary ---
	logger.Infof("")
	logger.Infof("Results: %d passed, %d failed", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
