package extension

import (
	"encoding/json"
	"fmt"

	"veil/internal/config"
	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"
)

// processGetBookState handles GET_BOOK_STATE direct instructions.
//
// Always returns depth for every configured pair (depth is bounded by MaxLevelsPerSide).
// If req.Pair is set and known, also returns the most recent matches for that pair,
// newest-first, capped at min(req.MatchLimit | DefaultBookMatchLimit, ring size).
// If req.Pair is empty, no matches are included.
func (e *Extension) processGetBookState(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.GetBookStateRequest
	if len(msg) > 0 {
		if err := json.Unmarshal(msg, &req); err != nil {
			return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
		}
	}

	limit := req.MatchLimit
	if limit <= 0 {
		limit = DefaultBookMatchLimit
	}
	if limit > MaxMatchesPerPair {
		limit = MaxMatchesPerPair
	}

	e.mu.RLock()
	pairStates := make(map[string]types.PairState, len(e.orderbooks))
	for name, ob := range e.orderbooks {
		bids, asks := ob.Depth()
		pairStates[name] = types.PairState{Bids: bids, Asks: asks}
	}

	var matches []orderbook.Match
	var matchCount int
	if req.Pair != "" {
		ring, ok := e.matchesByPair[req.Pair]
		if !ok {
			e.mu.RUnlock()
			return buildResult(action, df, nil, 0, fmt.Errorf("unknown pair: %s", req.Pair))
		}
		matchCount = ring.Len()
		matches = ring.SnapshotNewestFirst(limit)
	}
	e.mu.RUnlock()

	resp := types.StateResponse{
		StateVersion: teeutils.ToHash(config.Version),
		State: types.State{
			Pairs:      pairStates,
			MatchCount: matchCount,
			Matches:    matches,
		},
	}

	data, _ := json.Marshal(resp)
	return buildResult(action, df, data, 1, nil)
}
