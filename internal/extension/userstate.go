package extension

import (
	"encoding/json"
	"fmt"
	"strings"

	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// processGetMyState handles GET_MY_STATE direct instructions.
// Returns the calling user's balances, open orders, and matches.
func (e *Extension) processGetMyState(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.GetMyStateRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
	}

	user := strings.ToLower(req.Sender)
	if user == "" {
		return buildResult(action, df, nil, 0, fmt.Errorf("sender address is required"))
	}

	e.mu.RLock()
	srcMatches := e.getUserMatches(user)
	resp := types.GetMyStateResponse{
		Balances:   e.balances.GetAll(user),
		OpenOrders: e.getUserOpenOrders(user),
		Matches:    append([]orderbook.Match(nil), srcMatches...), // defensive copy
	}
	e.mu.RUnlock()

	data, _ := json.Marshal(resp)
	return buildResult(action, df, data, 1, nil)
}

// processExportHistory handles EXPORT_HISTORY direct instructions.
// Returns full history for the user (or target user if admin).
func (e *Extension) processExportHistory(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.ExportHistoryRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
	}

	user := strings.ToLower(req.Sender)
	if user == "" {
		return buildResult(action, df, nil, 0, fmt.Errorf("sender address is required"))
	}

	targetUser := user
	if req.TargetUser != "" {
		targetUser = strings.ToLower(req.TargetUser)
		if !e.admins[user] {
			return buildResult(action, df, nil, 0, fmt.Errorf("not authorized to export other user's history"))
		}
	}

	e.mu.RLock()
	// Defensive copies — marshal happens after RUnlock.
	srcOrders := e.history.orders[targetUser]
	allOrders := append([]orderbook.Order(nil), srcOrders...)
	srcMatches := e.history.matches[targetUser]
	allMatches := append([]orderbook.Match(nil), srcMatches...)
	srcDeposits := e.history.deposits[targetUser]
	allDeposits := append([]types.DepositRecord(nil), srcDeposits...)
	srcWithdrawals := e.history.withdrawals[targetUser]
	allWithdrawals := append([]types.WithdrawalRecord(nil), srcWithdrawals...)

	resp := types.ExportHistoryResponse{
		User:        targetUser,
		Balances:    e.balances.GetAll(targetUser),
		Orders:      allOrders,
		Matches:     allMatches,
		Deposits:    allDeposits,
		Withdrawals: allWithdrawals,
	}
	e.mu.RUnlock()

	data, _ := json.Marshal(resp)
	return buildResult(action, df, data, 1, nil)
}
