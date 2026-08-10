// Command get-fxrp-address resolves the real, current FXRP ERC-20 token
// address by calling the Flare Contract Registry live, rather than trusting
// any hardcoded value — including values pasted into a chat, a README, or
// even official-looking documentation, since addresses can and do go stale
// (see BUILD_NOTES.md for a concrete example: an address once considered
// current was later documented elsewhere as deprecated).
//
// This mirrors dev.flare.network's own guidance exactly:
//
//	"You should not hardcode the FXRP Asset Manager address... Instead you
//	should dynamically fetch the FXRP Asset Manager address using the
//	Flare Contract Registry."
//
// (https://dev.flare.network/fassets/developer-guides/fassets-asset-manager-address-contracts-registry)
//
// Two-step resolution, both live on-chain calls:
//  1. FlareContractRegistry.getContractAddressByName("AssetManagerFXRP")
//     -> AssetManager address
//  2. AssetManager.fAsset() -> the actual FXRP ERC-20 token address
//
// The registry address itself (0xaD67FE...) is the one address genuinely
// safe to hardcode: Flare's docs state it is deployed at the identical
// address on Flare Mainnet, Coston2, Songbird, and Coston — it's the fixed
// entry point specifically so nothing downstream of it needs to be
// hardcoded. See:
// https://dev.flare.network/network/guides/flare-contracts-registry
//
// Usage:
//
//	go run ./cmd/get-fxrp-address -rpc https://coston2-api.flare.network/ext/C/rpc
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// flareContractRegistryAddress is documented as identical across every
// Flare network (mainnet, Coston2, Songbird, Coston) — see the package doc
// comment above for the source. This is the one address this tool
// hardcodes, deliberately, because Flare's own docs designate it as the
// stable entry point precisely so nothing else has to be.
const flareContractRegistryAddress = "0xaD67FE66660Fb8dFE9d6b1b4240d8650e30F6019"

const registryABI = `[{
	"inputs":[{"internalType":"string","name":"_name","type":"string"}],
	"name":"getContractAddressByName",
	"outputs":[{"internalType":"address","name":"","type":"address"}],
	"stateMutability":"view",
	"type":"function"
}]`

const assetManagerABI = `[{
	"inputs":[],
	"name":"fAsset",
	"outputs":[{"internalType":"contract IERC20","name":"","type":"address"}],
	"stateMutability":"view",
	"type":"function"
}]`

func main() {
	rpcURL := flag.String("rpc", "https://coston2-api.flare.network/ext/C/rpc", "Flare RPC endpoint")
	flag.Parse()

	client, err := ethclient.Dial(*rpcURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED to dial %s: %v\n", *rpcURL, err)
		os.Exit(1)
	}

	regABI, err := abi.JSON(strings.NewReader(registryABI))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED to parse registry ABI: %v\n", err)
		os.Exit(1)
	}
	amABI, err := abi.JSON(strings.NewReader(assetManagerABI))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED to parse AssetManager ABI: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("RPC:                %s\n", *rpcURL)
	fmt.Printf("Contract Registry:  %s\n", flareContractRegistryAddress)
	fmt.Println(strings.Repeat("-", 60))

	// Step 1: registry -> AssetManagerFXRP
	assetManagerAddr, err := callAddress(client, regABI, "getContractAddressByName",
		common.HexToAddress(flareContractRegistryAddress), "AssetManagerFXRP")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED at step 1 (resolve AssetManagerFXRP): %v\n", err)
		os.Exit(1)
	}
	if assetManagerAddr == (common.Address{}) {
		fmt.Fprintln(os.Stderr, "FAILED: registry returned the zero address for \"AssetManagerFXRP\" — wrong network, or the registry key name has changed. Don't proceed with a zero address.")
		os.Exit(1)
	}
	fmt.Printf("AssetManagerFXRP:   %s\n", assetManagerAddr.Hex())

	// Step 2: AssetManager -> fAsset() (the real FXRP token address)
	fxrpAddr, err := callAddress(client, amABI, "fAsset", assetManagerAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED at step 2 (call fAsset()): %v\n", err)
		os.Exit(1)
	}
	if fxrpAddr == (common.Address{}) {
		fmt.Fprintln(os.Stderr, "FAILED: fAsset() returned the zero address — do not use this as the FXRP address.")
		os.Exit(1)
	}

	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("FXRP token address: %s\n", fxrpAddr.Hex())
	fmt.Println()
	fmt.Println("This is a live, on-chain-resolved result, not a hardcoded or pasted value.")
	fmt.Println("Cross-check against https://coston2-explorer.flare.network before using it")
	fmt.Println("in config/coston2/pairs.json — confirm it's a verified ERC-20 contract with")
	fmt.Println("a plausible symbol/name (FXRP) before trusting it for real settlement logic.")
}

// callAddress makes an eth_call to `method` on `contract` with the given
// args and decodes a single address return value.
func callAddress(client *ethclient.Client, contractABI abi.ABI, method string, contract common.Address, args ...interface{}) (common.Address, error) {
	callData, err := contractABI.Pack(method, args...)
	if err != nil {
		return common.Address{}, fmt.Errorf("pack %s: %w", method, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &contract,
		Data: callData,
	}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("eth_call %s: %w", method, err)
	}

	out, err := contractABI.Unpack(method, result)
	if err != nil {
		return common.Address{}, fmt.Errorf("unpack %s: %w", method, err)
	}
	if len(out) != 1 {
		return common.Address{}, fmt.Errorf("unexpected %s output length %d", method, len(out))
	}
	addr, ok := out[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("unexpected type for %s output: %T", method, out[0])
	}
	return addr, nil
}
