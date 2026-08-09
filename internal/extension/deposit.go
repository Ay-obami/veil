package extension

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// processDeposit handles DEPOSIT instructions (on-chain).
// The OriginalMessage is ABI-encoded: (address sender, address token, uint256 amount).
func (e *Extension) processDeposit(action teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	// ABI-decode the message: (address, address, uint256)
	sender, token, amount, err := decodeDepositMessage(df.OriginalMessage)
	if err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding deposit message: %w", err))
	}

	user := strings.ToLower(sender.Hex())

	if amount == 0 {
		return buildResult(action, df, nil, 0, fmt.Errorf("deposit amount must be greater than zero"))
	}

	// Credit the user's balance.
	if err := e.balances.Deposit(user, token, amount); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("crediting balance: %w", err))
	}

	e.mu.Lock()
	e.history.deposits[user] = appendBounded(e.history.deposits[user], types.DepositRecord{
		Token:     token,
		Amount:    amount,
		Timestamp: time.Now().UnixNano(),
	}, MaxUserDepositsHistory)
	e.mu.Unlock()

	bal := e.balances.Get(user, token)
	resp := types.DepositResponse{
		Token:     token,
		Amount:    amount,
		Available: bal.Available,
	}
	data, _ := json.Marshal(resp)

	return buildResult(action, df, data, 1, nil)
}

// decodeDepositMessage ABI-decodes (address sender, address token, uint256 amount).
func decodeDepositMessage(msg []byte) (common.Address, common.Address, uint64, error) {
	addrTy, _ := abi.NewType("address", "", nil)
	uint256Ty, _ := abi.NewType("uint256", "", nil)

	args := abi.Arguments{
		{Type: addrTy},
		{Type: addrTy},
		{Type: uint256Ty},
	}

	values, err := args.Unpack(msg)
	if err != nil {
		return common.Address{}, common.Address{}, 0, fmt.Errorf("abi unpack: %w", err)
	}

	if len(values) != 3 {
		return common.Address{}, common.Address{}, 0, fmt.Errorf("expected 3 values, got %d", len(values))
	}

	sender, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, common.Address{}, 0, fmt.Errorf("expected address for sender")
	}
	token, ok := values[1].(common.Address)
	if !ok {
		return common.Address{}, common.Address{}, 0, fmt.Errorf("expected address for token")
	}

	// uint256 comes as *big.Int
	amountBig, ok := values[2].(*big.Int)
	if !ok {
		return common.Address{}, common.Address{}, 0, fmt.Errorf("expected uint256 for amount")
	}

	return sender, token, amountBig.Uint64(), nil
}
