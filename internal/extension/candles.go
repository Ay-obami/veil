package extension

import (
	"encoding/json"
	"fmt"

	"veil/pkg/orderbook"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// processGetCandles handles GET_CANDLES direct instructions.
// Returns OHLCV candles for (pair, timeframe), oldest-first, capped at limit.
func (e *Extension) processGetCandles(action teetypes.Action, df *instruction.DataFixed, msg hexutil.Bytes) teetypes.ActionResult {
	var req types.GetCandlesRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return buildResult(action, df, nil, 0, fmt.Errorf("decoding request: %w", err))
	}

	if req.Pair == "" {
		return buildResult(action, df, nil, 0, fmt.Errorf("pair is required"))
	}

	tf, err := orderbook.ParseTimeframe(req.Timeframe)
	if err != nil {
		return buildResult(action, df, nil, 0, err)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = DefaultCandleLimit
	}
	if limit > MaxCandlesPerTF {
		limit = MaxCandlesPerTF
	}

	e.mu.RLock()
	tfRings, ok := e.candles[req.Pair]
	if !ok {
		e.mu.RUnlock()
		return buildResult(action, df, nil, 0, fmt.Errorf("unknown pair: %s", req.Pair))
	}
	ring := tfRings[tf]
	if ring == nil {
		e.mu.RUnlock()
		return buildResult(action, df, nil, 0, fmt.Errorf("timeframe not initialized: %s", req.Timeframe))
	}
	all := ring.Snapshot() // oldest-first
	e.mu.RUnlock()

	if len(all) > limit {
		all = all[len(all)-limit:]
	}

	resp := types.GetCandlesResponse{
		Pair:      req.Pair,
		Timeframe: tf.String(),
		Candles:   all,
	}
	data, _ := json.Marshal(resp)
	return buildResult(action, df, data, 1, nil)
}
