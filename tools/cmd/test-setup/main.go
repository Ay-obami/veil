// test-setup sets up the test environment for the orderbook extension.
//
// It performs these steps:
//  1. Allow deployer to deposit (idempotent)
//  2. Deploy four TestToken contracts (TUSDT, TFLR, TBTC, TETH)
//  3. Update config/pairs.json with deployed token addresses (3 pairs)
//  4. Mint tokens to the deployer
//  5. Approve InstructionSender to spend all tokens
//  6. Write config/test-tokens.env for use by other test commands
//
// If config/pairs.json is already fully populated (every baseToken/quoteToken
// is a non-zero address) steps 2, 3, and 4 are skipped — token addresses are
// read from pairs.json instead. Steps 1, 5, and 6 always run, because allow
// and approve are tied to the (possibly fresh) InstructionSender.
//
// Note: setExtensionId is handled by post-build.sh, not here.
//
// Usage:
//
//	go run ./cmd/test-setup -a <addresses-file> -c <chain-url> -instructionSender <addr>
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"veil/tools/pkg/configs"
	"veil/tools/pkg/contracts/veil"
	"veil/tools/pkg/fccutils"
	"veil/tools/pkg/support"
	instrutils "veil/tools/pkg/utils"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

// txSpacing is a small pause between consecutive transactions, giving
// load-balanced public RPC nodes time to propagate nonce state between peers.
const txSpacing = 500 * time.Millisecond

const (
	testTokenArtifact = "../out/TestToken.sol/TestToken.json"
	mintAmount        = 1_000_000
	approveAmount     = 1_000_000
)

type pairConfig struct {
	Name       string `json:"name"`
	BaseToken  string `json:"baseToken"`
	QuoteToken string `json:"quoteToken"`
}

func main() {
	af := flag.String("a", configs.AddressesFile, "file with deployed addresses")
	cf := flag.String("c", configs.ChainNodeURL, "chain node url")
	instructionSenderF := flag.String("instructionSender", "", "InstructionSender contract address")
	flag.Parse()

	if *instructionSenderF == "" {
		logger.Errorf("--instructionSender is required")
		os.Exit(1)
	}

	instructionSenderAddr := common.HexToAddress(*instructionSenderF)

	// Network-scoped config: derive from the addresses file path so each chain
	// (coston, coston2, local) keeps its own pairs.json and test-tokens.env.
	cfgDir := configs.NetworkConfigDir(*af)
	pairsConfigPath := filepath.Join(cfgDir, "pairs.json")
	testTokensEnvPath := filepath.Join(cfgDir, "test-tokens.env")

	s, err := support.DefaultSupport(*af, *cf)
	if err != nil {
		fccutils.FatalWithCause(err)
	}

	deployer := crypto.PubkeyToAddress(s.Prv.PublicKey)
	logger.Infof("Deployer: %s", deployer.Hex())
	logger.Infof("InstructionSender: %s", instructionSenderAddr.Hex())

	// --- Step 1: Allow deployer to deposit (idempotent) ---
	logger.Infof("Step 1: Allowing deployer to deposit...")
	err = allowUser(s, instructionSenderAddr, deployer)
	if err != nil {
		logger.Infof("  allowUser: %s (may already be allowed)", err)
	} else {
		logger.Infof("  Deployer allowed")
	}

	type tokenSpec struct {
		name    string
		symbol  string
		display string
	}
	specs := []tokenSpec{
		{"TestUSDT", "TUSDT", "TUSDT"},
		{"TestFLR", "TFLR", "TFLR"},
		{"TestBTC", "TBTC", "TBTC"},
		{"TestETH", "TETH", "TETH"},
	}

	addrs := make(map[string]common.Address, len(specs))

	// --- Steps 2/3/4: deploy, write pairs.json, mint — skipped if pairs.json is
	// already populated (tokens already exist on-chain from a prior run). ---
	if existing, ok := readPopulatedPairs(pairsConfigPath); ok {
		logger.Infof("Step 2/3/4: config/pairs.json already populated — skipping deploy, pairs.json write, and mint")
		if err := addressesFromExistingPairs(existing, addrs); err != nil {
			fccutils.FatalWithCause(err)
		}
		for _, spec := range specs {
			logger.Infof("  %s=%s (from pairs.json)", spec.display, addrs[spec.symbol].Hex())
		}
	} else {
		logger.Infof("Step 2: Deploying test tokens...")
		for _, spec := range specs {
			time.Sleep(txSpacing)
			a, err := instrutils.DeployTestToken(s, testTokenArtifact, spec.name, spec.symbol)
			if err != nil {
				fccutils.FatalWithCause(fmt.Errorf("deploying %s: %w", spec.symbol, err))
			}
			addrs[spec.symbol] = a
			logger.Infof("  %s deployed at: %s", spec.display, a.Hex())
		}

		logger.Infof("Step 3: Updating config/pairs.json...")
		pairs := []pairConfig{
			{Name: "FLR/USDT", BaseToken: addrs["TFLR"].Hex(), QuoteToken: addrs["TUSDT"].Hex()},
			{Name: "BTC/USDT", BaseToken: addrs["TBTC"].Hex(), QuoteToken: addrs["TUSDT"].Hex()},
			{Name: "ETH/USDT", BaseToken: addrs["TETH"].Hex(), QuoteToken: addrs["TUSDT"].Hex()},
		}
		pairsJSON, _ := json.MarshalIndent(pairs, "", "    ")
		if err := os.MkdirAll(filepath.Dir(pairsConfigPath), 0755); err != nil {
			fccutils.FatalWithCause(fmt.Errorf("creating pairs.json dir: %w", err))
		}
		if err := os.WriteFile(pairsConfigPath, pairsJSON, 0644); err != nil {
			fccutils.FatalWithCause(fmt.Errorf("writing pairs.json: %w", err))
		}
		logger.Infof("  Wrote FLR/USDT, BTC/USDT, ETH/USDT")

		logger.Infof("Step 4: Minting tokens to deployer...")
		amount := big.NewInt(mintAmount)
		for _, spec := range specs {
			time.Sleep(txSpacing)
			if err := instrutils.MintERC20(s, addrs[spec.symbol], deployer, amount); err != nil {
				fccutils.FatalWithCause(fmt.Errorf("minting %s: %w", spec.symbol, err))
			}
			logger.Infof("  Minted %d %s to %s", mintAmount, spec.display, deployer.Hex())
		}
	}

	tusdt := addrs["TUSDT"]
	tflr := addrs["TFLR"]
	tbtc := addrs["TBTC"]
	teth := addrs["TETH"]

	// --- Step 5: Approve InstructionSender ---
	// Always runs — the InstructionSender may have been freshly redeployed
	// (ERC20 approvals are stored per-spender, so a new InstructionSender
	// starts with zero allowance even if the tokens themselves are unchanged).
	logger.Infof("Step 5: Approving InstructionSender to spend tokens...")
	approveAmt := big.NewInt(approveAmount)
	for _, spec := range specs {
		time.Sleep(txSpacing)
		if err := instrutils.ApproveERC20(s, addrs[spec.symbol], instructionSenderAddr, approveAmt); err != nil {
			fccutils.FatalWithCause(fmt.Errorf("approving %s: %w", spec.symbol, err))
		}
		logger.Infof("  Approved %d %s", approveAmount, spec.display)
	}

	// --- Step 6: Write test-tokens.env ---
	// QUOTE_TOKEN / BASE_TOKEN retain their original FLR/USDT meaning so existing
	// test-deposit / test-withdraw commands keep working unchanged.
	// BASE_TOKEN_<SYM> lets stress-test select a specific pair's base via -pair.
	logger.Infof("Step 6: Writing config/test-tokens.env...")
	os.MkdirAll(filepath.Dir(testTokensEnvPath), 0755)
	envContent := fmt.Sprintf(
		"# Auto-generated by test-setup — do not edit manually\nQUOTE_TOKEN=%s\nBASE_TOKEN=%s\nTUSDT_TOKEN=%s\nTFLR_TOKEN=%s\nTBTC_TOKEN=%s\nTETH_TOKEN=%s\nBASE_TOKEN_FLR=%s\nBASE_TOKEN_BTC=%s\nBASE_TOKEN_ETH=%s\n",
		tusdt.Hex(), tflr.Hex(), tusdt.Hex(), tflr.Hex(), tbtc.Hex(), teth.Hex(), tflr.Hex(), tbtc.Hex(), teth.Hex(),
	)
	if err := os.WriteFile(testTokensEnvPath, []byte(envContent), 0644); err != nil {
		fccutils.FatalWithCause(fmt.Errorf("writing test-tokens.env: %w", err))
	}

	logger.Infof("")
	logger.Infof("Setup complete!")
	logger.Infof("  TUSDT=%s", tusdt.Hex())
	logger.Infof("  TFLR=%s", tflr.Hex())
	logger.Infof("  TBTC=%s", tbtc.Hex())
	logger.Infof("  TETH=%s", teth.Hex())
}

var zeroAddr = common.Address{}

// readPopulatedPairs returns the parsed pairs slice iff every pair has
// non-zero baseToken and quoteToken addresses. Returns (nil, false) when the
// file is missing, malformed, empty, or contains any zero address.
func readPopulatedPairs(path string) ([]pairConfig, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var pairs []pairConfig
	if err := json.Unmarshal(raw, &pairs); err != nil || len(pairs) == 0 {
		return nil, false
	}
	for _, p := range pairs {
		if !common.IsHexAddress(p.BaseToken) || !common.IsHexAddress(p.QuoteToken) {
			return nil, false
		}
		if common.HexToAddress(p.BaseToken) == zeroAddr || common.HexToAddress(p.QuoteToken) == zeroAddr {
			return nil, false
		}
	}
	return pairs, true
}

// addressesFromExistingPairs fills addrs (symbol → address) by matching pairs
// in pairs.json against the known pair names. Returns an error if any expected
// pair is missing.
func addressesFromExistingPairs(pairs []pairConfig, addrs map[string]common.Address) error {
	// name → (baseSymbol, quoteSymbol)
	byName := map[string][2]string{
		"FLR/USDT": {"TFLR", "TUSDT"},
		"BTC/USDT": {"TBTC", "TUSDT"},
		"ETH/USDT": {"TETH", "TUSDT"},
	}
	for _, p := range pairs {
		syms, ok := byName[p.Name]
		if !ok {
			continue // unknown pair, ignored
		}
		addrs[syms[0]] = common.HexToAddress(p.BaseToken)
		addrs[syms[1]] = common.HexToAddress(p.QuoteToken)
	}
	for _, sym := range []string{"TUSDT", "TFLR", "TBTC", "TETH"} {
		if _, ok := addrs[sym]; !ok {
			return fmt.Errorf("pairs.json missing pair that defines %s", sym)
		}
	}
	return nil
}

func allowUser(s *support.Support, instructionSenderAddr, user common.Address) error {
	sender, err := veil.NewVeilInstructionSender(instructionSenderAddr, s.ChainClient)
	if err != nil {
		return fmt.Errorf("binding contract: %w", err)
	}

	var tx *types.Transaction
	var sendErr error
	for attempt := 0; attempt < 3; attempt++ {
		opts, err := bind.NewKeyedTransactorWithChainID(s.Prv, s.ChainID)
		if err != nil {
			return fmt.Errorf("creating transactor: %w", err)
		}
		if attempt > 0 {
			gp, gerr := s.ChainClient.SuggestGasPrice(context.Background())
			if gerr != nil {
				return fmt.Errorf("suggesting gas price: %w", gerr)
			}
			mul := new(big.Int).Mul(gp, big.NewInt(int64(100+20*attempt)))
			opts.GasPrice = new(big.Int).Div(mul, big.NewInt(100))
		}

		tx, sendErr = sender.AllowUser(opts, user)
		if sendErr == nil {
			break
		}
		if !instrutils.IsRetryableTxError(sendErr) {
			return fmt.Errorf("calling allowUser: %w", sendErr)
		}
		if attempt < 2 {
			time.Sleep(2 * time.Second)
		}
	}
	if sendErr != nil {
		return fmt.Errorf("calling allowUser after retries: %w", sendErr)
	}

	_, err = support.CheckTx(tx, s.ChainClient)
	return err
}
