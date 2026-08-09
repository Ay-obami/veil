package main

import (
	"context"
	"encoding/json"
	"flag"
	"math/big"
	"strings"
	"time"

	"veil/tools/pkg/configs"
	"veil/tools/pkg/contracts/veil"
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/pkg/errors"
)

type DepositResponse struct {
	Token     string `json:"token"`
	Amount    uint64 `json:"amount"`
	Available uint64 `json:"available"`
}

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	instructionSenderF := flag.String("instructionSender", "", "instructionSender address")
	tokenF := flag.String("token", "", "ERC20 token address for deposit test")
	amountF := flag.Int64("amount", 1000, "deposit amount")
	flag.Parse()

	instructionSenderAddress := common.HexToAddress(*instructionSenderF)
	tokenAddress := common.HexToAddress(*tokenF)

	testSupport, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}

	// --- Generic: configure contract -----------------------------------------
	logger.Infof("Setting extension ID on instruction sender...")
	err = instrutils.SetExtensionId(testSupport, instructionSenderAddress)
	if err != nil {
		if strings.Contains(err.Error(), "already set") || strings.Contains(err.Error(), "Extension ID already set") {
			logger.Infof("Extension ID already set on contract, continuing")
		} else {
			logger.Errorf("setExtensionId failed: %s", err)
			fccutils.FatalWithCause(errors.Errorf(
				"setExtensionId failed — is the extension registered? Check that pre-build.sh completed successfully. Error: %s", err))
		}
	}

	// --- Allow the deployer to deposit ---
	logger.Infof("Allowing deployer to deposit...")
	err = allowUser(testSupport, instructionSenderAddress)
	if err != nil {
		// May already be allowed (admins are allowed at deploy time).
		logger.Infof("allowUser note: %s (may already be allowed)", err)
	}

	// --- Test case: Send a DEPOSIT instruction ---
	if *tokenF == "" {
		logger.Infof("No --token specified, skipping deposit test")
		logger.Infof("Usage: --token <ERC20_ADDRESS> --amount <AMOUNT>")
		logger.Infof("Note: Deployer must have approved the instruction sender contract to spend tokens.")
		return
	}

	logger.Infof("Sending DEPOSIT instruction (token=%s, amount=%d)...", tokenAddress.Hex(), *amountF)

	amount := big.NewInt(*amountF)
	instructionId, _, err := instrutils.Deposit(testSupport, instructionSenderAddress, tokenAddress, amount)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("Instruction sent. ID: %s", instructionId.Hex())

	time.Sleep(5 * time.Second)

	err = verifyDepositResult(*pf, instructionId)
	if err != nil {
		fccutils.FatalWithCause(err)
	}
	logger.Infof("Test passed: DEPOSIT instruction processed successfully")

	logger.Infof("All tests passed.")
}

func allowUser(s *support.Support, instructionSenderAddress common.Address) error {
	sender, err := veil.NewVeilInstructionSender(instructionSenderAddress, s.ChainClient)
	if err != nil {
		return errors.Errorf("failed to bind contract: %s", err)
	}

	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("failed to create transactor: %s", err)
	}

	deployer := crypto.PubkeyToAddress(s.Prv.PublicKey)
	tx, err := sender.AllowUser(opts, deployer)
	if err != nil {
		return errors.Errorf("failed to call allowUser: %s", err)
	}

	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("allowUser tx not mined: %s", err)
	}
	if receipt.Status != 1 {
		return errors.New("allowUser transaction failed")
	}

	return nil
}

func verifyDepositResult(proxyURL string, instructionId common.Hash) error {
	// --- Generic: poll proxy for result (do not modify) ---
	actionResponse, err := fccutils.ActionResult(proxyURL, instructionId)
	if err != nil {
		return err
	}
	actionResult := actionResponse.Result

	if actionResult.Status == 0 {
		return errors.Errorf("instruction processing failed: %s", actionResult.Log)
	}
	if actionResult.Status == 2 {
		return errors.New("instruction still pending after polling, expected completed")
	}

	if len(actionResult.Data) == 0 {
		return errors.New("expected response data but got none")
	}

	var resp DepositResponse
	err = json.Unmarshal(actionResult.Data, &resp)
	if err != nil {
		return errors.Errorf("failed to unmarshal response: %s", err)
	}

	if resp.Amount == 0 {
		return errors.New("expected non-zero deposit amount in response")
	}

	logger.Infof("Response data: %+v", resp)

	return nil
}
