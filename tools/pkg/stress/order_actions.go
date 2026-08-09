package stress

import (
	instrutils "veil/tools/pkg/utils"
)

// --- Direct-instruction payload / response shapes (mirror tools/cmd/test-orders) ---

type PlaceOrderReq struct {
	Sender   string `json:"sender"`
	Pair     string `json:"pair"`
	Side     string `json:"side"`
	Type     string `json:"type"`
	Price    uint64 `json:"price"`
	Quantity uint64 `json:"quantity"`
}

type PlaceOrderResp struct {
	OrderID   string `json:"orderId"`
	Status    string `json:"status"`
	Remaining uint64 `json:"remaining"`
	Matches   []struct {
		Price    uint64 `json:"price"`
		Quantity uint64 `json:"quantity"`
	} `json:"matches"`
}

type CancelOrderReq struct {
	Sender  string `json:"sender"`
	OrderID string `json:"orderId"`
}

type CancelOrderResp struct {
	OrderID   string `json:"orderId"`
	Remaining uint64 `json:"remaining"`
}

type GetMyStateReq struct {
	Sender string `json:"sender"`
}

type TokenBalance struct {
	Available uint64 `json:"available"`
	Held      uint64 `json:"held"`
}

type GetMyStateResp struct {
	Balances   map[string]TokenBalance `json:"balances"`
	OpenOrders []struct {
		ID        string `json:"id"`
		Remaining uint64 `json:"remaining"`
	} `json:"openOrders"`
}

// PlaceOrder submits a PLACE_ORDER direct instruction as this trader.
func (t *Trader) PlaceOrder(proxyURL, pair, side, orderType string, price, qty uint64) (*PlaceOrderResp, error) {
	var resp PlaceOrderResp
	err := instrutils.SendDirectAndPoll(proxyURL, "PLACE_ORDER", PlaceOrderReq{
		Sender: t.AddrLC, Pair: pair, Side: side, Type: orderType, Price: price, Quantity: qty,
	}, &resp)
	return &resp, err
}

// CancelOrder submits a CANCEL_ORDER direct instruction.
func (t *Trader) CancelOrder(proxyURL, orderID string) (*CancelOrderResp, error) {
	var resp CancelOrderResp
	err := instrutils.SendDirectAndPoll(proxyURL, "CANCEL_ORDER", CancelOrderReq{
		Sender: t.AddrLC, OrderID: orderID,
	}, &resp)
	return &resp, err
}

// GetMyState fetches balances and open orders for this trader.
func (t *Trader) GetMyState(proxyURL string) (*GetMyStateResp, error) {
	var resp GetMyStateResp
	err := instrutils.SendDirectAndPoll(proxyURL, "GET_MY_STATE", GetMyStateReq{
		Sender: t.AddrLC,
	}, &resp)
	return &resp, err
}
