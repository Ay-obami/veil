# Orderbook Extension — Testing Guide

## Prerequisites

- Full deployment completed (`veil deploy all -network local`, or per-network config)
- Extension TEE + proxy running (Docker)
- `deployment/deployment.json` has `instructionSender` and `extensionId` under `outputs`
- `config/<network>/pairs.json` populated with deployed token addresses (written by the `contracts` stage)
- `DEPLOYMENT_PRIVATE_KEY` exported in your shell

## Setup Lifecycle

The full lifecycle runs in this order (see `docs/deployment.md` for the
engine that runs it). Token deployment and config must happen **before**
Docker starts so the extension loads the correct `pairs.json` at startup.

```
1. contracts    → deploy InstructionSender, register extension, deploy
                   tokens, write pairs.json, mint, approve
2. extension    → docker compose up, extension starts with correct config
3. machine      → register TEE version + machine, setExtensionId,
                   set InstructionSender TEE signer address (enables executeWithdrawal)
4. frontend     → generate frontend/src/config/generated.ts
5. smoke        → sends a real instruction end-to-end
6. test-deposit         → deposit tokens (on-chain instructions)   [manual, below]
7. test-orders          → place/cancel orders (direct instructions) [manual, below]
8. test-withdraw        → withdraw tokens (on-chain + executeWithdrawal) [manual, below]
```

Run stages 1-5 at once: `veil deploy all -network local`

## Setup your shell

Run this once per terminal session. It loads all the variables you need for the commands below.

```bash
export DEPLOYMENT_PRIVATE_KEY=<your key>   # if not already exported

# Pull values out of the deployment engine's state
export INSTRUCTION_SENDER=$(jq -r '.outputs.instructionSender' deployment/deployment.json)
export EXTENSION_ID=$(jq -r '.outputs.extensionId' deployment/deployment.json)
export EXT_PROXY_URL=$(jq -r '.outputs.proxyURL' deployment/deployment.json)
export CHAIN_URL=$(grep '^rpc:' configs/local.yaml | sed 's/rpc:[[:space:]]*//')

# Derive your deployer address from your private key
export DEPLOYER_ADDRESS=$(cast wallet address --private-key $DEPLOYMENT_PRIVATE_KEY)

echo "Deployer:           $DEPLOYER_ADDRESS"
echo "InstructionSender:  $INSTRUCTION_SENDER"
echo "Proxy:              $EXT_PROXY_URL"
```

**Where variables come from:**

| Variable | Source | Set by |
|----------|--------|--------|
| `DEPLOYMENT_PRIVATE_KEY` | your shell | You |
| `CHAIN_URL` | `configs/<network>.yaml` | You |
| `EXT_PROXY_URL` | `deployment/deployment.json` `outputs.proxyURL` | `veil deploy extension` |
| `INSTRUCTION_SENDER` | `deployment/deployment.json` `outputs.instructionSender` | `veil deploy contracts` |
| `EXTENSION_ID` | `deployment/deployment.json` `outputs.extensionId` | `veil deploy contracts` |
| `DEPLOYER_ADDRESS` | Derived | `cast wallet address` (see above) |
| Token addresses | `config/<network>/pairs.json` | `veil deploy contracts` (after deploying tokens) |

---

## 1. Unit Tests (no infra needed)

```bash
go test ./pkg/...
```

Tests the matching engine (price-time priority, partial fills, multi-level sweeps) and balance manager (hold/release/transfer) in isolation.

---

## 2. Integration Tests

On-chain tests for contract deployment and revert decoding. Require a running chain node.

```bash
cd tools && go test -tags integration ./integration/ -v -count=1
```

Against Coston2:
```bash
cd tools && CHAIN_URL=https://coston2-api.flare.network/ext/C/rpc \
  DEPLOYMENT_PRIVATE_KEY=<key> \
  go test -tags integration ./integration/ -v -count=1
```

---

## 3. End-to-End Test Commands

Four Go test commands under `tools/cmd/` cover the full orderbook lifecycle. Run them from the `tools/` directory.

### Run everything at once

```bash
./scripts/test-all.sh
```

This runs all four commands in sequence. Use `--skip-setup` on subsequent runs to skip token deployment:

```bash
./scripts/test-all.sh --skip-setup
```

### Run individually

Each command can be run on its own. They share a common set of flags:

```
-a <addresses-file>       # path to deployed-addresses.json
-c <chain-url>            # RPC endpoint
-p <proxy-url>            # extension proxy URL
-instructionSender <addr> # InstructionSender contract address
```

If you've sourced your env files (see "Setup your shell" above), the values come from there.

> **Note on paths:** These commands run from the `tools/` directory. If `ADDRESSES_FILE` is a relative path like `./config/coston2/deployed-addresses.json` (relative to the project root), the tooling will automatically resolve it from the parent directory. Using `scripts/test-all.sh` avoids this entirely since it resolves all paths to absolute before running. Alternatively you could run the command with ` -a "../$ADDRESSES_FILE"`

#### test-setup — Deploy tokens and configure test environment

```bash
go run ./cmd/test-setup -a config/local/deployed-addresses.json -c http://127.0.0.1:8545 -instructionSender $INSTRUCTION_SENDER
```

This runs automatically as the last step of the `contracts` stage (`veil deploy contracts`), but can also be run standalone. It:

1. Allows deployer to deposit on the InstructionSender (idempotent)
2. Deploys four TestToken contracts: one shared quote (TUSDT) and one base per
   pair (TFLR, TBTC, TETH)
3. Updates `config/pairs.json` with all three pairs (FLR/USDT, BTC/USDT,
   ETH/USDT), each sharing the TUSDT quote
4. Mints 1,000,000 of every token to the deployer
5. Approves InstructionSender to spend 1,000,000 of every token
6. Writes `config/<network>/test-tokens.env` with `QUOTE_TOKEN`, legacy `BASE_TOKEN`
   (= FLR base) for `test-deposit` / `test-withdraw`, plus `BASE_TOKEN_FLR` /
   `BASE_TOKEN_BTC` / `BASE_TOKEN_ETH` for `stress-test` pair targeting

**Runs before Docker,** as part of the `contracts` stage — before the `extension` stage brings up `docker compose up`, so the extension loads the correct `pairs.json` at startup. Re-running deploys fresh tokens (requires restarting Docker to pick up new addresses).

#### test-deposit — Test the on-chain deposit flow

```bash
cd tools && go run ./cmd/test-deposit -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

What it does:
1. Deposits quote token (TUSDT) via `InstructionSender.deposit()`, polls for result
2. Deposits base token (TFLR), polls for result
3. Sends GET_MY_STATE direct instruction to verify both balances were credited

**Safe to re-run.** Each run deposits more tokens (cumulative). Will fail when the approved allowance runs out — re-run `test-setup` or manually approve more.

#### test-orders — Test the order lifecycle via direct instructions

```bash
cd tools && go run ./cmd/test-orders -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL"
```

What it does:
1. Verifies balances exist via GET_MY_STATE
2. Places a sell limit order — verifies status=resting
3. Checks `/state` — verifies the ask appears in the book
4. Places a matching buy — verifies status=filled with a match
5. Checks `/state` — verifies the book cleared
6. Verifies balances changed after the trade
7. Places an order and cancels it — verifies funds released
8. Tests partial fill: sell 10, buy 5 — verifies remainder on book

**Safe to re-run** as long as you have enough balance. Cleans up after itself (cancels leftover orders).

#### test-withdraw — Test the 2-step withdrawal flow

```bash
cd tools && go run ./cmd/test-withdraw -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

What it does:
1. Checks available balance via GET_MY_STATE
2. Sends a withdraw instruction on-chain, polls for the TEE-signed result
3. Calls `executeWithdrawal()` with the TEE signature to transfer tokens out of the vault
4. Verifies on-chain token balance increased
5. Verifies internal balance decreased via GET_MY_STATE

**Safe to re-run** as long as there's enough internal balance (each run withdraws 100 tokens).

### Required order

```
contracts (incl. test-setup) → extension (docker compose up) → machine → test-deposit → test-orders / test-withdraw
```

- `contracts` must run before `extension` (writes `pairs.json`)
- `test-deposit` must run before `test-orders` or `test-withdraw` (they need balances)
- `test-orders` and `test-withdraw` are independent — either order works
- `test-deposit` can be re-run to top up balances

---

## 4. Manual Testing (cast + curl)

The sections below show how to test each flow manually with `cast` and `curl` if you prefer that over the Go test commands.

This is where you test the actual orderbook functionality. There are two instruction paths:

| Path | Used for | How it works |
|------|----------|-------------|
| **On-chain** | Deposit, Withdraw | Solidity tx -> TEE processes -> poll proxy for result |
| **Direct** | Place/Cancel orders, Get state, Export history | HTTP POST to proxy `/direct` -> TEE processes -> poll for result |

### Overview of the full test flow

```
1. Deploy mock ERC20 tokens (or use existing ones)
2. Update config/pairs.json with real token addresses
3. Approve InstructionSender to spend your tokens
4. Deposit tokens (on-chain instruction)
5. Place orders (direct instruction)
6. Verify matching, balances, and book state
7. Cancel an order (direct instruction)
8. Withdraw tokens (on-chain instruction + executeWithdrawal)
```

---

## Step-by-step

### Note: setExtensionId

The InstructionSender contract needs to know its extension ID from the registry. This is now handled automatically by the `machine` stage (`veil deploy machine`). If you skipped it, call it manually:

```bash
cast send $INSTRUCTION_SENDER "setExtensionId()" --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY
```

### 4.1 — Deploy test tokens

The addresses in `config/pairs.json` are placeholders. You need real ERC20 tokens on your target chain.

A minimal mintable ERC20 is included at `contracts/TestToken.sol`. Anyone can call `mint()` — it's for testing only.

```bash
# Deploy a mock USDT (quote token)
forge create --broadcast --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY contracts/TestToken.sol:TestToken --constructor-args "TestUSDT" "TUSDT"

# Deploy a mock FLR (base token)
forge create --broadcast --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY contracts/TestToken.sol:TestToken --constructor-args "TestFLR" "TFLR"
```

Each `forge create` prints `Deployed to: 0x...` — copy those addresses.

> **Alternative:** Use existing testnet tokens if you already have some on Coston2.

### 4.2 — Configure token addresses

Update `config/pairs.json` with the deployed token addresses:
```json
[
  {
    "name": "FLR/USDT",
    "baseToken": "<your-FLR-token-address>",
    "quoteToken": "<your-USDT-token-address>"
  }
]
```

Restart the extension after changing pairs.json.

Then set shell variables for the rest of this guide (use the same addresses you just put in `pairs.json`):

```bash
QUOTE_TOKEN="0x..."  # USDT address from pairs.json quoteToken
BASE_TOKEN="0x..."   # FLR address from pairs.json baseToken
```

### 4.3 — Mint tokens to your deployer

The test tokens start with zero supply. Mint some to your deployer so you can deposit them:

```bash
cast send $QUOTE_TOKEN "mint(address,uint256)" $DEPLOYER_ADDRESS 1000000 --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY

cast send $BASE_TOKEN "mint(address,uint256)" $DEPLOYER_ADDRESS 1000000 --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY
```

Verify your balance:
```bash
cast call $QUOTE_TOKEN "balanceOf(address)(uint256)" $DEPLOYER_ADDRESS --rpc-url $CHAIN_URL
cast call $BASE_TOKEN "balanceOf(address)(uint256)" $DEPLOYER_ADDRESS --rpc-url $CHAIN_URL
```

### 4.4 — Approve the InstructionSender

The contract needs ERC20 approval to pull tokens during deposit:

```bash
cast send $QUOTE_TOKEN "approve(address,uint256)" $INSTRUCTION_SENDER 1000000 --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY

cast send $BASE_TOKEN "approve(address,uint256)" $INSTRUCTION_SENDER 1000000 --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY
```

### 4.5 — Deposit tokens

```bash
# Deposit 10000 USDT (quote token) — payable with instruction fee
cast send $INSTRUCTION_SENDER "deposit(address,uint256)" $QUOTE_TOKEN 10000 --value 1000000 --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY
```

Or via the test tool:
```bash
cd tools && go run ./cmd/run-test -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER" -token "$QUOTE_TOKEN" -amount 10000
```

The TEE processes the deposit and credits your in-memory balance. Verify with GET_MY_STATE (section 4.7).

### 4.6 — Check public state

```bash
curl -s "$EXT_PROXY_URL/state" | jq
```

Returns orderbook depth for all pairs and recent matches. Works anytime, no auth.

### 4.7 — Send direct instructions

Direct instructions go to `POST /direct` on the proxy. The payload format uses bytes32 hashes for opType/opCommand and hex-encoded JSON for the message.

**Helper to build a direct instruction curl:**

```bash
# Converts a string to a 0x-prefixed bytes32 hex
to_bytes32() {
  printf '0x'
  printf "%-32s" "$1" | xxd -p | head -c 64
}

# Sends a direct instruction. Args: <command> <json_payload>
send_direct() {
  local cmd="$1" payload="$2"
  local op_type=$(to_bytes32 "ORDERBOOK")
  local op_cmd=$(to_bytes32 "$cmd")
  local msg_hex="0x$(echo -n "$payload" | xxd -p | tr -d '\n')"

  curl -s -X POST "$EXT_PROXY_URL/direct" \
    -H "Content-Type: application/json" \
    -d "{\"opType\":\"$op_type\",\"opCommand\":\"$op_cmd\",\"message\":\"$msg_hex\"}"
}
```

> **Note:** If the proxy has API key auth enabled, add `-H "X-API-Key: <key>"` to the curl.
> If `/direct` returns 404, the proxy may need `enable_direct = true` in its TOML config.

**Get your state:**
```bash
send_direct "GET_MY_STATE" '{"sender":"'$DEPLOYER_ADDRESS'"}'
```

**Place a limit buy order:**
```bash
send_direct "PLACE_ORDER" '{"sender":"'$DEPLOYER_ADDRESS'","pair":"FLR/USDT","side":"buy","type":"limit","price":100,"quantity":50}'
```

**Place a limit sell order (to match):**
```bash
send_direct "PLACE_ORDER" '{"sender":"'$DEPLOYER_ADDRESS'","pair":"FLR/USDT","side":"sell","type":"limit","price":100,"quantity":50}'
```

**Cancel an order:**
```bash
send_direct "CANCEL_ORDER" '{"sender":"'$DEPLOYER_ADDRESS'","orderId":"ORD-..."}'
```

The response from `/direct` is the queued action with an `id` field. To get the processed result:

```bash
curl -s "$EXT_PROXY_URL/action/result/$ACTION_ID" | jq
```

### 4.8 — Test order matching

To properly test matching, you need two different users (or use the same user as both buyer and seller — the orderbook doesn't prevent self-trading).

1. Deposit USDT for the buyer and FLR for the seller
2. Place a **sell** limit order first (rests on the book)
3. Place a **buy** at the same or higher price (triggers match)
4. Check the buy response — should show `"status":"filled"` with match details
5. Check `/state` — the price level should be cleared
6. Check GET_MY_STATE — buyer should now have FLR available, seller should have USDT

**Partial fill test:** Place a sell for qty=10, then buy for qty=5. The buy fills completely, the sell has remaining=5 on the book.

**Multi-level sweep:** Place sells at price 100 (qty=5) and 101 (qty=5), then buy at 101 (qty=8). Should sweep both levels.

### 4.9 — Test withdrawal

After trading, withdraw tokens back on-chain:

```bash
# Request withdrawal — TEE signs it
cast send $INSTRUCTION_SENDER "withdraw(address,uint256,address)" $QUOTE_TOKEN 500 $DEPLOYER_ADDRESS --value 1000000 --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY
```

Poll the result — it returns `token`, `amount`, `to`, `withdrawalId`, and `signature`.

Then execute the withdrawal using the TEE-signed params:
```bash
cast send $INSTRUCTION_SENDER "executeWithdrawal(address,uint256,address,bytes32,bytes)" $TOKEN $AMOUNT $TO $WITHDRAWAL_ID $SIGNATURE --rpc-url $CHAIN_URL --private-key $DEPLOYMENT_PRIVATE_KEY
```

This actually transfers ERC20 tokens from the contract vault to the recipient.

> The TEE address must already be set on the contract (`setTeeAddress`) — the `machine` stage does this automatically as its last step, after registering the TEE. If it was skipped, run `veil deploy machine` (or `veil deploy resume`) before calling `executeWithdrawal`.

---

## Verification Checklist

- [ ] `go test ./pkg/...` passes
- [ ] `/state` returns empty book on fresh start
- [ ] Deposit credits balance (verify via GET_MY_STATE)
- [ ] Limit order with no counterparty: status=resting, appears in `/state`
- [ ] Matching order: status=filled, match in response, book clears
- [ ] Partial fill: status=partial, remainder rests on book
- [ ] Cancel: order removed, held funds released
- [ ] Market order fills against resting liquidity
- [ ] Withdrawal returns valid TEE signature
- [ ] `executeWithdrawal` moves tokens out of vault
- [ ] Price-time priority: earlier order at same price fills first

---

## Quick Reference

| Operation | How | Auth |
|---|---|---|
| Deposit | `InstructionSender.deposit(token, amount)` | On-chain tx (KYC-gated) |
| Withdraw request | `InstructionSender.withdraw(token, amount, to)` | On-chain tx |
| Execute withdrawal | `InstructionSender.executeWithdrawal(...)` | TEE signature required |
| Place order | `POST /direct` — PLACE_ORDER | Direct instruction |
| Cancel order | `POST /direct` — CANCEL_ORDER | Direct instruction |
| Get my state | `POST /direct` — GET_MY_STATE | Direct instruction |
| Export history | `POST /direct` — EXPORT_HISTORY | Direct instruction (admin) |
| Public book depth | `GET /state` | None |
| Poll result | `GET /action/result/<id>` | None |
