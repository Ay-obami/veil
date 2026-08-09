package extension

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"veil/internal/config"
	"veil/pkg/balance"
	"veil/pkg/oracle"
	"veil/pkg/orderbook"
	"veil/pkg/solvency"
	"veil/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"

	"github.com/flare-foundation/tee-node/pkg/processorutils"
)

// History tracks per-user deposit/withdrawal/order/match records.
// All slices are kept bounded (see caps.go); the oldest entries fall off silently.
//
// Note: orders are stored BY VALUE, not by pointer. They are a snapshot at
// place time. This avoids races between the matching engine (which mutates
// the live *Order on each fill) and the eviction path (which used to mutate
// Remaining=0). EXPORT_HISTORY thus reports the order as placed; for the
// up-to-date book state of an open order, use GET_MY_STATE.
type History struct {
	deposits    map[string][]types.DepositRecord    // user -> deposits
	withdrawals map[string][]types.WithdrawalRecord // user -> withdrawals
	orders      map[string][]orderbook.Order        // user -> all orders (snapshot at place time)
	matches     map[string][]orderbook.Match        // user -> matches
}

func newHistory() *History {
	return &History{
		deposits:    make(map[string][]types.DepositRecord),
		withdrawals: make(map[string][]types.WithdrawalRecord),
		orders:      make(map[string][]orderbook.Order),
		matches:     make(map[string][]orderbook.Match),
	}
}

// Extension is the orderbook extension handler.
type Extension struct {
	mu     sync.RWMutex
	Server *http.Server

	orderbooks    map[string]*orderbook.OrderBook                                      // pair name -> orderbook
	balances      *balance.Manager                                                     // per-(user, token) balances
	pairs         map[string]config.TradingPairConfig                                  // pair name -> token addresses
	matchesByPair map[string]*orderbook.Ring[orderbook.Match]                          // pair -> ring of recent matches
	candles       map[string]map[orderbook.Timeframe]*orderbook.Ring[orderbook.Candle] // pair -> tf -> ring
	orders        map[string]string                                                    // orderID -> pair (for cancel routing)
	userOrders    map[string][]string                                                  // user address -> list of orderIDs
	history       *History                                                             // deposit/withdrawal/order history per user
	admins        map[string]bool                                                      // admin addresses
	signPort      int                                                                  // TEE sign server port

	// Solvency proof verification — new for Veil, not in the base
	// fce-orderbook. verificationKey is nil if verification is disabled
	// (see config.SolvencyVerificationKeyPath); PLACE_ORDER requests that
	// include a proof are then rejected with a clear error rather than
	// silently skipping the check, so "the feature looked enabled but
	// wasn't" can't happen silently.
	verificationKey    []byte
	usedSolvencyNonces map[string]time.Time // publicSignals[PublicSignalOrderIDIdx] -> when consumed; guarded by e.mu (see processPlaceOrder's lock-policy comment), NOT solvencyMu below

	// solvencyMu guards issuedCommitments only. Separate from e.mu
	// deliberately: commitment issuance (processGetSolvencyCommitment) is
	// independent of order placement/matching and shouldn't serialize
	// with it, unlike usedSolvencyNonces above which genuinely needs to be
	// atomic with order registration.
	solvencyMu        sync.Mutex
	issuedCommitments map[string]issuedCommitmentRecord // commitment (decimal string) -> record; see issuedCommitmentRecord
}

func New(extensionPort, signPort int) *Extension {
	e := &Extension{
		orderbooks:         make(map[string]*orderbook.OrderBook),
		balances:           balance.NewManager(),
		pairs:              make(map[string]config.TradingPairConfig),
		matchesByPair:      make(map[string]*orderbook.Ring[orderbook.Match]),
		candles:            make(map[string]map[orderbook.Timeframe]*orderbook.Ring[orderbook.Candle]),
		orders:             make(map[string]string),
		userOrders:         make(map[string][]string),
		history:            newHistory(),
		usedSolvencyNonces: make(map[string]time.Time),
		issuedCommitments:  make(map[string]issuedCommitmentRecord),
		admins:             make(map[string]bool),
		signPort:           signPort,
	}

	for _, addr := range config.AdminAddresses {
		e.admins[strings.ToLower(addr)] = true
	}

	if config.BalancesPath != "" {
		if err := e.balances.SetPersistPath(config.BalancesPath); err != nil {
			logger.Errorf("balance persistence load failed at %s: %v (starting empty)", config.BalancesPath, err)
		} else {
			logger.Infof("balance persistence enabled at %s", config.BalancesPath)
		}
	}

	for _, pair := range config.TradingPairs {
		e.pairs[pair.Name] = pair
		e.orderbooks[pair.Name] = orderbook.NewOrderBook(pair.Name)
		e.matchesByPair[pair.Name] = orderbook.NewRing[orderbook.Match](MaxMatchesPerPair)
		tfRings := make(map[orderbook.Timeframe]*orderbook.Ring[orderbook.Candle], len(orderbook.Timeframes))
		for _, tf := range orderbook.Timeframes {
			tfRings[tf] = orderbook.NewRing[orderbook.Candle](MaxCandlesPerTF)
		}
		e.candles[pair.Name] = tfRings
		logger.Infof("registered trading pair: %s (base=%s, quote=%s)", pair.Name, pair.BaseToken.Hex(), pair.QuoteToken.Hex())
	}

	// FTSO price-band oracle: opt-in via FTSO_RPC_URL. A construction failure
	// here (bad RPC, bad address) is logged and treated as "no oracle" rather
	// than a startup failure — matches the fail-open philosophy documented in
	// pkg/orderbook.PriceOracle and BUILD_NOTES.md. Only pairs with a
	// non-empty FtsoFeed get the oracle attached; others keep unrestricted
	// matching exactly as in the base fce-orderbook.
	if config.FtsoRPCURL != "" {
		pairFeedNames := make(map[string]string)
		for _, pair := range config.TradingPairs {
			if pair.FtsoFeed != "" {
				pairFeedNames[pair.Name] = pair.FtsoFeed
			}
		}
		if len(pairFeedNames) == 0 {
			logger.Infof("FTSO_RPC_URL set but no configured pair has an ftsoFeed — price-band check stays disabled for all pairs")
		} else {
			ftsoV2Addr := config.FtsoV2Address
			if ftsoV2Addr == "" {
				ftsoV2Addr = oracle.FtsoV2ProxyCoston2
			}
			priceOracle, err := oracle.NewFTSOPriceOracle(
				config.FtsoRPCURL,
				common.HexToAddress(ftsoV2Addr),
				pairFeedNames,
				config.FtsoToleranceBps,
				time.Duration(config.FtsoCacheTTLSeconds)*time.Second,
			)
			if err != nil {
				logger.Errorf("FTSO oracle setup failed: %v (matching will run without a price band — see BUILD_NOTES.md)", err)
			} else {
				for pairName := range pairFeedNames {
					if ob, ok := e.orderbooks[pairName]; ok {
						ob.SetPriceOracle(priceOracle)
					}
				}
				logger.Infof("FTSO price-band oracle enabled for pairs: %v (tolerance=%dbps)", mapKeys(pairFeedNames), config.FtsoToleranceBps)
			}
		}
	}

	// Solvency proof verification — opt-out via SOLVENCY_VK_PATH="".
	// Fails open at startup (log + disable), same as the FTSO oracle
	// above, rather than crashing extension startup over an optional
	// feature. See pkg/solvency and internal/extension/solvency.go.
	if config.SolvencyVerificationKeyPath != "" {
		vk, err := solvency.LoadVerificationKey(config.SolvencyVerificationKeyPath)
		if err != nil {
			logger.Errorf("solvency verification key load failed at %s: %v (PLACE_ORDER requests with a proof attached will be rejected until this is fixed)", config.SolvencyVerificationKeyPath, err)
		} else {
			e.verificationKey = vk
			logger.Infof("solvency proof verification enabled (key: %s)", config.SolvencyVerificationKeyPath)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /action", e.actionHandler)

	e.Server = &http.Server{Addr: fmt.Sprintf(":%d", extensionPort), Handler: mux}

	// Periodically sweep zero-balance users so the manager's user-keyed map
	// doesn't grow unboundedly under churn (mock MMs spin up new addresses).
	go e.sweepEmptyBalances(5 * time.Minute)

	return e
}

// mapKeys returns the keys of m as a slice, in unspecified order. Used only
// for logging (see New()'s FTSO oracle setup) — not relied on for anything
// order-sensitive.
func mapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// sweepEmptyBalances runs forever, calling balances.EvictEmpty at the given interval.
// This goroutine is intentionally fire-and-forget; the process exits when the
// container stops.
func (e *Extension) sweepEmptyBalances(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		if n := e.balances.EvictEmpty(); n > 0 {
			logger.Infof("balance manager: evicted %d empty user records", n)
		}
	}
}

// processAction routes by action type (instruction vs direct) and then by OPType/OPCommand.
func (e *Extension) processAction(action teetypes.Action) (int, []byte) {
	switch action.Data.Type {
	case teetypes.Instruction:
		return e.processInstruction(action)
	case teetypes.Direct:
		return e.processDirect(action)
	default:
		return http.StatusBadRequest, []byte(fmt.Sprintf("unsupported action type: %s", action.Data.Type))
	}
}

// processInstruction handles on-chain instruction actions (deposits, withdrawals).
func (e *Extension) processInstruction(action teetypes.Action) (int, []byte) {
	df, err := processorutils.Parse[instruction.DataFixed](action.Data.Message)
	if err != nil {
		return http.StatusBadRequest, []byte(fmt.Sprintf("decoding fixed data: %v", err))
	}

	if df.OPType != teeutils.ToHash(config.OPTypeOrderbook) {
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported op type: received %s, expected %s (%s)",
			df.OPType.Hex(), teeutils.ToHash(config.OPTypeOrderbook).Hex(), config.OPTypeOrderbook,
		))
	}

	var ar teetypes.ActionResult

	switch {
	case df.OPCommand == teeutils.ToHash(config.OPCommandDeposit):
		ar = e.processDeposit(action, df)
	case df.OPCommand == teeutils.ToHash(config.OPCommandWithdraw):
		ar = e.processWithdraw(action, df)
	default:
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported instruction op command: %s", df.OPCommand.Hex(),
		))
	}

	b, _ := json.Marshal(ar)
	return http.StatusOK, b
}

// processDirect handles off-chain direct instruction actions (orders, cancels, state, history).
func (e *Extension) processDirect(action teetypes.Action) (int, []byte) {
	di, err := processorutils.Parse[teetypes.DirectInstruction](action.Data.Message)
	if err != nil {
		return http.StatusBadRequest, []byte(fmt.Sprintf("decoding direct instruction: %v", err))
	}

	if di.OPType != teeutils.ToHash(config.OPTypeOrderbook) {
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported op type: received %s, expected %s (%s)",
			di.OPType.Hex(), teeutils.ToHash(config.OPTypeOrderbook).Hex(), config.OPTypeOrderbook,
		))
	}

	df := &instruction.DataFixed{
		InstructionID: action.Data.ID,
		OPType:        di.OPType,
		OPCommand:     di.OPCommand,
	}

	var ar teetypes.ActionResult

	switch {
	case di.OPCommand == teeutils.ToHash(config.OPCommandPlaceOrder):
		ar = e.processPlaceOrder(action, df, di.Message)
	case di.OPCommand == teeutils.ToHash(config.OPCommandCancelOrder):
		ar = e.processCancelOrder(action, df, di.Message)
	case di.OPCommand == teeutils.ToHash(config.OPCommandGetMyState):
		ar = e.processGetMyState(action, df, di.Message)
	case di.OPCommand == teeutils.ToHash(config.OPCommandGetSolvencyCommitment):
		ar = e.processGetSolvencyCommitment(action, df, di.Message)
	case di.OPCommand == teeutils.ToHash(config.OPCommandGetBookState):
		ar = e.processGetBookState(action, df, di.Message)
	case di.OPCommand == teeutils.ToHash(config.OPCommandGetCandles):
		ar = e.processGetCandles(action, df, di.Message)
	case di.OPCommand == teeutils.ToHash(config.OPCommandExportHistory):
		ar = e.processExportHistory(action, df, di.Message)
	default:
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported direct op command: %s", di.OPCommand.Hex(),
		))
	}

	b, _ := json.Marshal(ar)
	return http.StatusOK, b
}

// getUserOpenOrders returns all currently-resting orders for a user.
// Caller must hold e.mu (read or write).
func (e *Extension) getUserOpenOrders(user string) []orderbook.Order {
	ids := e.userOrders[user]
	if len(ids) == 0 {
		return nil
	}
	orders := make([]orderbook.Order, 0, len(ids))
	for _, id := range ids {
		pair, ok := e.orders[id]
		if !ok {
			continue
		}
		ob, ok := e.orderbooks[pair]
		if !ok {
			continue
		}
		if o := ob.GetOrder(id); o != nil {
			orders = append(orders, *o)
		}
	}
	return orders
}

// getUserMatches returns the bounded ring of matches involving a user.
// Caller must hold e.mu (read or write).
func (e *Extension) getUserMatches(user string) []orderbook.Match {
	return e.history.matches[user]
}

// nextOrderID generates a unique order ID. Concurrent-safe: the counter is
// incremented atomically and combined with a nanosecond timestamp.
var orderCounter atomic.Uint64

func (e *Extension) nextOrderID() string {
	n := orderCounter.Add(1)
	return fmt.Sprintf("ORD-%d-%d", time.Now().UnixNano(), n)
}
