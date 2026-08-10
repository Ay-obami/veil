package stress

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Trader owns one EOA and one Support, plus cached derived state.
type Trader struct {
	Index   int
	Key     *ecdsa.PrivateKey
	Addr    common.Address
	AddrLC  string // lowercased hex, no 0x — what the direct API expects
	Support *support.Support
}

func NewTrader(idx int, key *ecdsa.PrivateKey, client *ethclient.Client, addrs *support.Addresses) (*Trader, error) {
	s, err := support.NewSupport(key, client, addrs)
	if err != nil {
		return nil, fmt.Errorf("new support: %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	return &Trader{
		Index:   idx,
		Key:     key,
		Addr:    addr,
		AddrLC:  strings.ToLower(addr.Hex()),
		Support: s,
	}, nil
}

// Deposit sends an on-chain DEPOSIT for this trader. Wraps instrutils.Deposit so
// the msg.sender is the trader (not the funder). Amount is in raw token units
// (i.e. already multiplied by 10^decimals of the given token).
func (t *Trader) Deposit(instructionSender, token common.Address, amount *big.Int) (common.Hash, error) {
	id, _, err := instrutils.Deposit(t.Support, instructionSender, token, amount)
	return id, err
}

// Withdraw sends an on-chain WITHDRAW for this trader, funds return to t.Addr.
// Amount is in raw token units.
func (t *Trader) Withdraw(instructionSender, token common.Address, amount *big.Int) (common.Hash, error) {
	id, _, err := instrutils.Withdraw(t.Support, instructionSender, token, amount, t.Addr)
	return id, err
}
