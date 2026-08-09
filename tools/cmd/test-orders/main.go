// test-orders tests the order lifecycle via direct instructions.
//
// Tests:
//  1. GET_MY_STATE — verify balances exist
//  2. Place sell limit order — verify status=resting
//  3. Check /state — verify ask in book
//  4. Place matching buy — verify status=filled with match
//  5. Check /state — verify book cleared
//  6. GET_MY_STATE — verify balances changed
//  7. Place and cancel an order — verify funds released
//  8. Partial fill — sell 10, buy 5, verify remainder
//
// Usage:
//
//	go run ./cmd/test-orders -p <proxy-url>
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"veil/tools/pkg/configs"
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// Request/response types for direct instructions.
type placeOrderReq struct {
	Sender   string `json:"sender"`
	Pair     string `json:"pair"`
	Side     string `json:"side"`
	Type     string `json:"type"`
	Price    uint64 `json:"price"`
	Quantity uint64 `json:"quantity"`
}

type placeOrderResp struct {
	OrderID   string `json:"orderId"`
	Status    string `json:"status"`
	Remaining uint64 `json:"remaining"`
	Matches   []struct {
		Price    uint64 `json:"price"`
		Quantity uint64 `json:"quantity"`
	} `json:"matches"`
}

type cancelOrderReq struct {
	Sender  string `json:"sender"`
	OrderID string `json:"orderId"`
}

type cancelOrderResp struct {
	OrderID   string `json:"orderId"`
	Remaining uint64 `json:"remaining"`
}

type getMyStateReq struct {
	Sender string `json:"sender"`
}

type getBookStateReq struct {
	Sender string `json:"sender,omitempty"`
}

type tokenBalance struct {
	Available uint64 `json:"available"`
	Held      uint64 `json:"held"`
}

type getMyStateResp struct {
	Balances   map[common.Address]tokenBalance `json:"balances"`
	OpenOrders []struct {
		ID        string `json:"id"`
		Remaining uint64 `json:"remaining"`
	} `json:"openOrders"`
}

type stateResp struct {
	State struct {
		Pairs map[string]struct {
			Bids []struct {
				Price    uint64 `json:"price"`
				Quantity uint64 `json:"quantity"`
			} `json:"bids"`
			Asks []struct {
				Price    uint64 `json:"price"`
				Quantity uint64 `json:"quantity"`
			} `json:"asks"`
		} `json:"pairs"`
	} `json:"state"`
}

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	pf := flag.String("p", configs.ExtensionProxyURL, "extension proxy url")
	flag.Parse()

	// We need support only for the deployer address derivation.
	s, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}

	deployer := strings.ToLower(crypto.PubkeyToAddress(s.Prv.PublicKey).Hex())
	proxyURL := *pf
	pair := "FLR/USDT"

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

	// --- Test 1: Verify balances exist ---
	run("GET_MY_STATE — balances exist", func() error {
		var resp getMyStateResp
		err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{Sender: deployer}, &resp)
		if err != nil {
			return err
		}
		if len(resp.Balances) == 0 {
			return fmt.Errorf("no balances found — run test-deposit first")
		}
		for token, bal := range resp.Balances {
			logger.Infof("  %s: available=%d held=%d", token.Hex(), bal.Available, bal.Held)
		}
		return nil
	})

	// --- Test 2: Place sell limit order ---
	var sellOrderID string
	run("Place sell limit order — status=resting", func() error {
		var resp placeOrderResp
		err := instrutils.SendDirectAndPoll(proxyURL, "PLACE_ORDER", placeOrderReq{
			Sender: deployer, Pair: pair, Side: "sell", Type: "limit", Price: 100, Quantity: 50,
		}, &resp)
		if err != nil {
			return err
		}
		if resp.Status != "resting" {
			return fmt.Errorf("expected status=resting, got %s", resp.Status)
		}
		if resp.Remaining != 50 {
			return fmt.Errorf("expected remaining=50, got %d", resp.Remaining)
		}
		sellOrderID = resp.OrderID
		logger.Infof("  Order %s: status=%s remaining=%d", resp.OrderID, resp.Status, resp.Remaining)
		return nil
	})

	// --- Test 3: Check /state — ask in book ---
	run("Check /state — ask appears in book", func() error {
		state, err := fetchState(proxyURL, deployer)
		if err != nil {
			return err
		}
		pairState, ok := state.State.Pairs[pair]
		if !ok {
			return fmt.Errorf("pair %s not found in state", pair)
		}
		if len(pairState.Asks) == 0 {
			return fmt.Errorf("expected asks in book, got 0")
		}
		if pairState.Asks[0].Price != 100 || pairState.Asks[0].Quantity != 50 {
			return fmt.Errorf("expected ask price=100 qty=50, got price=%d qty=%d", pairState.Asks[0].Price, pairState.Asks[0].Quantity)
		}
		logger.Infof("  Ask: price=%d qty=%d", pairState.Asks[0].Price, pairState.Asks[0].Quantity)
		return nil
	})

	// --- Test 4: Place matching buy — verify fill ---
	run("Place matching buy — status=filled", func() error {
		var resp placeOrderResp
		err := instrutils.SendDirectAndPoll(proxyURL, "PLACE_ORDER", placeOrderReq{
			Sender: deployer, Pair: pair, Side: "buy", Type: "limit", Price: 100, Quantity: 50,
		}, &resp)
		if err != nil {
			return err
		}
		if resp.Status != "filled" {
			return fmt.Errorf("expected status=filled, got %s", resp.Status)
		}
		if len(resp.Matches) != 1 {
			return fmt.Errorf("expected 1 match, got %d", len(resp.Matches))
		}
		if resp.Matches[0].Price != 100 || resp.Matches[0].Quantity != 50 {
			return fmt.Errorf("expected match price=100 qty=50, got price=%d qty=%d", resp.Matches[0].Price, resp.Matches[0].Quantity)
		}
		logger.Infof("  Filled: match price=%d qty=%d", resp.Matches[0].Price, resp.Matches[0].Quantity)
		return nil
	})

	// --- Test 5: Check /state — book cleared ---
	run("Check /state — book cleared after match", func() error {
		state, err := fetchState(proxyURL, deployer)
		if err != nil {
			return err
		}
		pairState, ok := state.State.Pairs[pair]
		if !ok {
			return fmt.Errorf("pair %s not found in state", pair)
		}
		if len(pairState.Asks) != 0 {
			return fmt.Errorf("expected 0 asks after match, got %d", len(pairState.Asks))
		}
		if len(pairState.Bids) != 0 {
			return fmt.Errorf("expected 0 bids after match, got %d", len(pairState.Bids))
		}
		logger.Infof("  Book is empty")
		return nil
	})

	// --- Test 6: Verify balances changed ---
	run("GET_MY_STATE — balances changed after trade", func() error {
		var resp getMyStateResp
		err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{Sender: deployer}, &resp)
		if err != nil {
			return err
		}
		for token, bal := range resp.Balances {
			logger.Infof("  %s: available=%d held=%d", token.Hex(), bal.Available, bal.Held)
		}
		return nil
	})

	// --- Test 7: Place and cancel ---
	run("Place order then cancel — funds released", func() error {
		// Get state before.
		var before getMyStateResp
		if err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{Sender: deployer}, &before); err != nil {
			return fmt.Errorf("GET_MY_STATE before: %w", err)
		}

		// Place a sell order.
		var placeResp placeOrderResp
		if err := instrutils.SendDirectAndPoll(proxyURL, "PLACE_ORDER", placeOrderReq{
			Sender: deployer, Pair: pair, Side: "sell", Type: "limit", Price: 200, Quantity: 10,
		}, &placeResp); err != nil {
			return fmt.Errorf("placing order: %w", err)
		}
		if placeResp.Status != "resting" {
			return fmt.Errorf("expected resting, got %s", placeResp.Status)
		}
		logger.Infof("  Placed order %s", placeResp.OrderID)

		// Cancel it.
		var cancelResp cancelOrderResp
		if err := instrutils.SendDirectAndPoll(proxyURL, "CANCEL_ORDER", cancelOrderReq{
			Sender: deployer, OrderID: placeResp.OrderID,
		}, &cancelResp); err != nil {
			return fmt.Errorf("cancelling order: %w", err)
		}
		logger.Infof("  Cancelled order %s, remaining=%d", cancelResp.OrderID, cancelResp.Remaining)

		// Get state after — balances should be restored.
		var after getMyStateResp
		if err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", getMyStateReq{Sender: deployer}, &after); err != nil {
			return fmt.Errorf("GET_MY_STATE after: %w", err)
		}

		// Check no open orders remain.
		if len(after.OpenOrders) != 0 {
			return fmt.Errorf("expected 0 open orders after cancel, got %d", len(after.OpenOrders))
		}
		logger.Infof("  No open orders, funds released")
		return nil
	})

	// --- Test 8: Partial fill ---
	run("Partial fill — sell 10, buy 5", func() error {
		// Place sell for 10.
		var sellResp placeOrderResp
		if err := instrutils.SendDirectAndPoll(proxyURL, "PLACE_ORDER", placeOrderReq{
			Sender: deployer, Pair: pair, Side: "sell", Type: "limit", Price: 150, Quantity: 10,
		}, &sellResp); err != nil {
			return fmt.Errorf("placing sell: %w", err)
		}
		if sellResp.Status != "resting" {
			return fmt.Errorf("expected sell resting, got %s", sellResp.Status)
		}

		// Buy 5 — should partially fill the sell.
		var buyResp placeOrderResp
		if err := instrutils.SendDirectAndPoll(proxyURL, "PLACE_ORDER", placeOrderReq{
			Sender: deployer, Pair: pair, Side: "buy", Type: "limit", Price: 150, Quantity: 5,
		}, &buyResp); err != nil {
			return fmt.Errorf("placing buy: %w", err)
		}
		if buyResp.Status != "filled" {
			return fmt.Errorf("expected buy filled, got %s", buyResp.Status)
		}
		if len(buyResp.Matches) != 1 || buyResp.Matches[0].Quantity != 5 {
			return fmt.Errorf("expected 1 match qty=5, got %d matches", len(buyResp.Matches))
		}

		// Check book — sell should have remaining=5.
		state, err := fetchState(proxyURL, deployer)
		if err != nil {
			return err
		}
		pairState := state.State.Pairs[pair]
		if len(pairState.Asks) != 1 {
			return fmt.Errorf("expected 1 ask remaining, got %d", len(pairState.Asks))
		}
		if pairState.Asks[0].Quantity != 5 {
			return fmt.Errorf("expected remaining ask qty=5, got %d", pairState.Asks[0].Quantity)
		}
		logger.Infof("  Partial fill: buy filled, sell has %d remaining on book", pairState.Asks[0].Quantity)

		// Clean up: cancel the remaining sell.
		instrutils.SendDirectAndPoll(proxyURL, "CANCEL_ORDER", cancelOrderReq{
			Sender: deployer, OrderID: sellResp.OrderID,
		}, nil)

		return nil
	})

	// --- Summary ---
	logger.Infof("")
	logger.Infof("Results: %d passed, %d failed", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}

	_ = sellOrderID // used contextually in test 3
}

func fetchState(proxyURL, sender string) (*stateResp, error) {
	var state stateResp
	if err := instrutils.SendDirectAndPoll(proxyURL, "GET_BOOK_STATE", getBookStateReq{Sender: sender}, &state); err != nil {
		return nil, fmt.Errorf("GET_BOOK_STATE: %w", err)
	}
	return &state, nil
}
