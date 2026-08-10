package extension

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// processWithdraw handles WITHDRAW instructions (on-chain).
// The OriginalMessage is ABI-encoded: (address sender, address token, uint256 amount, address to).
// Returns TEE-signed withdrawal parameters that the user submits to the contract.
func (e *Extension) processWithdraw(action teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	sender, token, amount, to, err := decodeWithdrawMessage(df.OriginalMessage)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding withdraw message: %w", err))
	}

	user := strings.ToLower(sender.Hex())

	if amount == 0 {
		return buildResult(action, df, nil, 0, fmt.Errorf("withdraw amount must be greater than zero"))
	}

	// Debit balance.
	if err := e.balances.Withdraw(user, token, amount); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("debiting balance: %w", err))
	}

	// Build the withdrawal message: abi.encodePacked(token, amount, to, withdrawalId).
	// We send the RAW packed bytes (not keccak256'd) because the TEE sign server
	// applies keccak256 + EIP-191 prefix internally; see signWithTEE below.
	withdrawalID := df.InstructionID
	message := packWithdrawalMessage(token, amount, to, withdrawalID)

	// Sign via TEE sign server.
	sig, err := e.signWithTEE(message)
	if err != nil {
		// Rollback: re-credit balance on signing failure.
		_ = e.balances.Deposit(user, token, amount)
		return buildResult(action, df, nil, 0, fmt.Errorf("signing withdrawal: %w", err))
	}

	e.mu.Lock()
	e.history.withdrawals[user] = appendBounded(e.history.withdrawals[user], types.WithdrawalRecord{
		Token:     token,
		Amount:    amount,
		Address:   to,
		Timestamp: time.Now().UnixNano(),
	}, MaxUserWithdrawsHistory)
	e.mu.Unlock()

	bal := e.balances.Get(user, token)
	resp := types.WithdrawResponse{
		Token:        token,
		Amount:       amount,
		To:           to,
		WithdrawalID: withdrawalID,
		Signature:    sig,
		Available:    bal.Available,
	}
	data, _ := json.Marshal(resp)

	return buildResult(action, df, data, 1, nil)
}

// packWithdrawalMessage returns abi.encodePacked(token, amount, to, withdrawalId)
// as raw bytes (104 bytes total). The TEE sign server keccak256's this input
// and signs EIP-191-prefixed digest of the result, which matches the contract's
// _recoverSigner expectation.
func packWithdrawalMessage(token common.Address, amount uint64, to common.Address, withdrawalID common.Hash) []byte {
	// abi.encodePacked: address(20) + uint256(32) + address(20) + bytes32(32)
	buf := make([]byte, 0, 104)
	buf = append(buf, token.Bytes()...)

	amountBytes := make([]byte, 32)
	new(big.Int).SetUint64(amount).FillBytes(amountBytes)
	buf = append(buf, amountBytes...)

	buf = append(buf, to.Bytes()...)
	buf = append(buf, withdrawalID.Bytes()...)

	return buf
}

// signWithTEE sends a message to the TEE sign server and returns the signature.
func (e *Extension) signWithTEE(message []byte) ([]byte, error) {
	reqBody, _ := json.Marshal(teetypes.SignRequest{Message: message})

	url := fmt.Sprintf("http://localhost:%d/sign", e.signPort)
	resp, err := http.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("POST /sign: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sign server returned %d", resp.StatusCode)
	}

	var signResp teetypes.SignResponse
	if err := json.NewDecoder(resp.Body).Decode(&signResp); err != nil {
		return nil, fmt.Errorf("decoding sign response: %w", err)
	}

	logger.Infof("withdrawal signed: %x", signResp.Signature[:8])
	return signResp.Signature, nil
}

// decodeWithdrawMessage ABI-decodes (address sender, address token, uint256 amount, address to).
func decodeWithdrawMessage(msg []byte) (common.Address, common.Address, uint64, common.Address, error) {
	addrTy, _ := abi.NewType("address", "", nil)
	uint256Ty, _ := abi.NewType("uint256", "", nil)

	args := abi.Arguments{
		{Type: addrTy},
		{Type: addrTy},
		{Type: uint256Ty},
		{Type: addrTy},
	}

	values, err := args.Unpack(msg)
	if err != nil {
		return common.Address{}, common.Address{}, 0, common.Address{}, fmt.Errorf("abi unpack: %w", err)
	}

	if len(values) != 4 {
		return common.Address{}, common.Address{}, 0, common.Address{}, fmt.Errorf("expected 4 values, got %d", len(values))
	}

	sender := values[0].(common.Address)
	token := values[1].(common.Address)
	amountBig := values[2].(*big.Int)
	to := values[3].(common.Address)

	return sender, token, amountBig.Uint64(), to, nil
}
