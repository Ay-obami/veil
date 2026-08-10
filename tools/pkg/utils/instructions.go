package utils

import (
	"context"
	"math/big"
	"time"

	"veil/tools/pkg/contracts/veil"
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/support"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pkg/errors"
)

func DeployInstructionSender(s *support.Support) (common.Address, *veil.VeilInstructionSender, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return common.Address{}, nil, errors.Errorf("failed to create transactor: %s", err)
	}

	deployer := crypto.PubkeyToAddress(s.Prv.PublicKey)
	admins := []common.Address{deployer}

	// Both registry args are the FlareTeeManager diamond proxy: the diamond
	// routes ExtensionManager and MachineManager calls to the right facets.
	address, tx, contract, err := veil.DeployVeilInstructionSender(
		opts, s.ChainClient, s.Addresses.FlareTeeManager, s.Addresses.FlareTeeManager, admins,
	)
	if err != nil {
		return common.Address{}, nil, errors.Errorf("failed to deploy contract: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := bind.WaitMined(ctx, s.ChainClient, tx)
	if err != nil {
		return common.Address{}, nil, errors.Errorf("deployment tx not mined within 2 minutes (tx: %s): %s", tx.Hash().Hex(), err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return common.Address{}, nil, errors.New("contract deployment failed")
	}

	return address, contract, nil
}

func SetExtensionId(s *support.Support, instructionSenderAddress common.Address) error {
	sender, err := veil.NewVeilInstructionSender(instructionSenderAddress, s.ChainClient)
	if err != nil {
		return errors.Errorf("failed to bind contract: %s", err)
	}

	opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
	if err != nil {
		return errors.Errorf("failed to create transactor: %s", err)
	}

	tx, err := sender.SetExtensionId(opts)
	if err != nil {
		reason := fccutils.DecodeRevertReason(err)
		if reason == "" {
			parsed, _ := veil.VeilInstructionSenderMetaData.GetAbi()
			if parsed != nil {
				callData, packErr := parsed.Pack("setExtensionId")
				if packErr == nil {
					from := crypto.PubkeyToAddress(s.Prv.PublicKey)
					reason = fccutils.SimulateAndDecodeRevert(
						s.ChainClient, from, instructionSenderAddress, nil, callData,
					)
				}
			}
		}
		if reason != "" {
			return errors.Errorf("failed to call setExtensionId: %s (revert reason: %s)", err, reason)
		}
		return errors.Errorf("failed to call setExtensionId: %s", err)
	}

	receipt, err := bind.WaitMined(context.Background(), s.ChainClient, tx)
	if err != nil {
		return errors.Errorf("failed waiting for transaction: %s", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		parsed, _ := veil.VeilInstructionSenderMetaData.GetAbi()
		if parsed != nil {
			callData, packErr := parsed.Pack("setExtensionId")
			if packErr == nil {
				from := crypto.PubkeyToAddress(s.Prv.PublicKey)
				reason := fccutils.SimulateAndDecodeRevert(
					s.ChainClient, from, instructionSenderAddress, nil, callData,
				)
				if reason != "" {
					return errors.Errorf("setExtensionId transaction failed (revert reason: %s)", reason)
				}
			}
		}
		return errors.New("setExtensionId transaction failed")
	}

	return nil
}

func Deposit(s *support.Support, instructionSenderAddress common.Address, token common.Address, amount *big.Int) (common.Hash, common.Hash, error) {
	sender, err := veil.NewVeilInstructionSender(instructionSenderAddress, s.ChainClient)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to bind contract: %s", err)
	}

	// Retry on transient/rate-limit errors. IsRetryableTxError also matches
	// HTTP 429 now, so this survives bursts against a shared public RPC.
	var tx *types.Transaction
	var sendErr error
	for attempt := 0; attempt < 3; attempt++ {
		opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
		if err != nil {
			return common.Hash{}, common.Hash{}, errors.Errorf("failed to create transactor: %s", err)
		}
		opts.Value = big.NewInt(1000000) // Instruction fee in wei

		tx, sendErr = sender.Deposit(opts, token, amount)
		if sendErr == nil {
			break
		}
		if !IsRetryableTxError(sendErr) {
			reason := fccutils.DecodeRevertReason(sendErr)
			if reason == "" {
				parsed, _ := veil.VeilInstructionSenderMetaData.GetAbi()
				if parsed != nil {
					callData, packErr := parsed.Pack("deposit", token, amount)
					if packErr == nil {
						from := crypto.PubkeyToAddress(s.Prv.PublicKey)
						reason = fccutils.SimulateAndDecodeRevert(
							s.ChainClient, from, instructionSenderAddress,
							big.NewInt(1000000), callData,
						)
					}
				}
			}
			if reason != "" {
				return common.Hash{}, common.Hash{}, errors.Errorf("failed to send deposit: %s (revert reason: %s)", sendErr, reason)
			}
			return common.Hash{}, common.Hash{}, errors.Errorf("failed to send deposit: %s", sendErr)
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		}
	}
	if sendErr != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to send deposit after retries: %s", sendErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := bind.WaitMined(ctx, s.ChainClient, tx)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed waiting for deposit tx (tx: %s): %s", tx.Hash().Hex(), err)
	}

	if receipt.Status != 1 {
		parsed, _ := veil.VeilInstructionSenderMetaData.GetAbi()
		if parsed != nil {
			callData, packErr := parsed.Pack("deposit", token, amount)
			if packErr == nil {
				from := crypto.PubkeyToAddress(s.Prv.PublicKey)
				reason := fccutils.SimulateAndDecodeRevert(
					s.ChainClient, from, instructionSenderAddress,
					big.NewInt(1000000), callData,
				)
				if reason != "" {
					return common.Hash{}, common.Hash{}, errors.Errorf("deposit tx failed (revert reason: %s)", reason)
				}
			}
		}
		return common.Hash{}, common.Hash{}, errors.Errorf("deposit tx failed with status: %d", receipt.Status)
	}

	instructionID, err := findInstructionID(s, receipt)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}

	return instructionID, receipt.TxHash, nil
}

func Withdraw(s *support.Support, instructionSenderAddress common.Address, token common.Address, amount *big.Int, to common.Address) (common.Hash, common.Hash, error) {
	sender, err := veil.NewVeilInstructionSender(instructionSenderAddress, s.ChainClient)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to bind contract: %s", err)
	}

	// Retry on transient/rate-limit errors (see Deposit for rationale).
	var tx *types.Transaction
	var sendErr error
	for attempt := 0; attempt < 3; attempt++ {
		opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
		if err != nil {
			return common.Hash{}, common.Hash{}, errors.Errorf("failed to create transactor: %s", err)
		}
		opts.Value = big.NewInt(1000000) // Instruction fee in wei

		tx, sendErr = sender.Withdraw(opts, token, amount, to)
		if sendErr == nil {
			break
		}
		if !IsRetryableTxError(sendErr) {
			return common.Hash{}, common.Hash{}, errors.Errorf("failed to send withdraw: %s", sendErr)
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		}
	}
	if sendErr != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed to send withdraw after retries: %s", sendErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	receipt, err := bind.WaitMined(ctx, s.ChainClient, tx)
	if err != nil {
		return common.Hash{}, common.Hash{}, errors.Errorf("failed waiting for withdraw tx (tx: %s): %s", tx.Hash().Hex(), err)
	}

	if receipt.Status != 1 {
		return common.Hash{}, common.Hash{}, errors.Errorf("withdraw tx failed with status: %d", receipt.Status)
	}

	instructionID, err := findInstructionID(s, receipt)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}

	return instructionID, receipt.TxHash, nil
}

// findInstructionID iterates through receipt logs to find the TeeInstructionsSent
// event. The event may not be the first log (e.g. ERC20 Transfer events precede it).
func findInstructionID(s *support.Support, receipt *types.Receipt) (common.Hash, error) {
	if len(receipt.Logs) == 0 {
		return common.Hash{}, errors.New("no logs found in receipt")
	}

	for _, log := range receipt.Logs {
		instructionSent, err := s.TeeVerification.ParseTeeInstructionsSent(*log)
		if err == nil {
			return instructionSent.InstructionId, nil
		}
	}

	return common.Hash{}, errors.New("TeeInstructionsSent event not found in receipt logs")
}
