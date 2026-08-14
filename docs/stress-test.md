# Stress Testing the Orderbook Extension

The `stress-test` tool spins up a fleet of simulated traders that mint, deposit,
place, cancel, and (eventually) withdraw against a live deployment of the
orderbook extension. Use it to load-test the TEE, verify the UI under realistic
activity, or run a multi-hour soak to shake out correctness regressions.

Source: [`tools/cmd/stress-test/`](../tools/cmd/stress-test/).

---

## What it does

Each run, in order:

1. **Generate / load** a set of trader EOAs (cached to `./traders.json` so
   repeated runs reuse the same wallets).
2. **Fund** each trader with native C2FLR gas from your deployer key.
3. **Cleanup** any stale open orders left behind by a previous crashed run.
4. **Bootstrap** every trader: mint test tokens, approve the InstructionSender,
   deposit into the extension.
5. **Run** personas (market-maker, taker, walker, whale, flicker) concurrently
   for the configured duration (or until `Ctrl+C`).
6. **Sweep** — cancel any remaining open orders and withdraw all on-chain
   balances back to each trader's wallet. Runs even on `SIGINT`.

Metrics (latency p50 / p95 / p99 and error rates per action) are printed
periodically during the run and as a final snapshot on exit.

---

## Prerequisites

Before the first run:

1. Extension deployed and registered — `veil deploy all -network coston2`.
2. Test tokens deployed — `cd tools && go run ./cmd/test-setup`. This deploys one
   shared `TUSDT` quote token plus one base token per pair (`TFLR`, `TBTC`,
   `TETH`), writes all three pairs to `config/pairs.json`, and writes
   `config/test-tokens.env` with `QUOTE_TOKEN`, legacy `BASE_TOKEN` (= FLR
   base), and `BASE_TOKEN_FLR` / `BASE_TOKEN_BTC` / `BASE_TOKEN_ETH`.
3. TEE proxy reachable at `$EXT_PROXY_URL`.
4. `DEPLOYMENT_PRIVATE_KEY` exported in your shell, funded with enough native
   C2FLR to fund every trader (default: 0.05 FLR per trader).
5. `INSTRUCTION_SENDER` known (from `deployment/deployment.json`
   `outputs.instructionSender`).

---

## Setup your shell

Run this once per terminal session. It loads all the variables you need for the commands below.

```bash
export INSTRUCTION_SENDER=$(jq -r '.outputs.instructionSender' deployment/deployment.json)
source config/<network>/test-tokens.env
```

**Where variables come from:**

| Variable | Source | Set by |
|----------|--------|--------|
| `DEPLOYMENT_PRIVATE_KEY` | your shell | You |
| `CHAIN_URL` | `configs/<network>.yaml` | You |
| `EXT_PROXY_URL` | `deployment/deployment.json` `outputs.proxyURL` | `veil deploy extension` |
| `ADDRESSES_FILE` | `configs/<network>.yaml` (`addressesFile`) | You |
| `INSTRUCTION_SENDER` | `deployment/deployment.json` `outputs.instructionSender` | `veil deploy contracts` |
| `QUOTE_TOKEN`, `BASE_TOKEN_*` | `config/<network>/test-tokens.env` | `test-setup` (auto-generated, part of `veil deploy contracts`) |

All `stress-test` invocations below follow the same pattern as the other test tools in [`docs/testing.md`](./testing.md): `cd` into `tools/`, then pass the env vars as flags.

```bash
cd tools && go run ./cmd/stress-test \
  -a "$ADDRESSES_FILE" \
  -c "$CHAIN_URL" \
  -p "$EXT_PROXY_URL" \
  -instructionSender "$INSTRUCTION_SENDER" \
  -tier=L2
```

> **Note on paths:** These commands run from the `tools/` directory. If `ADDRESSES_FILE` is a relative path like `./config/coston2/deployed-addresses.json` (relative to the project root), the tooling will automatically resolve it from the parent directory. Alternatively you could run the command with `-a "../$ADDRESSES_FILE"`.

---

## Quick start — "just give me some activity"

### Fastest smoke test (~1 minute, 3 traders)

```bash
cd tools && go run ./cmd/stress-test \
  -tier=L1 \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

### Moderate load (5 minutes, 10 traders, mixed personas)

```bash
cd tools && go run ./cmd/stress-test \
  -tier=L2 \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

### Only market-maker orders (no takers, no walkers)

```bash
cd tools && go run ./cmd/stress-test \
  -tier=L2 \
  -persona-mix=mm:4 \
  -traders=4 \
  -duration=10m \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

Market-makers post resting bids and asks around a mid-price — use this when
you want to populate the book for the UI or for another test to trade against.

### Only aggressive market-order flow

```bash
cd tools && go run ./cmd/stress-test \
  -tier=L2 \
  -persona-mix=taker:6 \
  -traders=6 \
  -duration=5m \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

(You need resting orders on the book for takers to hit — usually combine with
at least one `mm` trader, e.g. `-persona-mix=mm:2,taker:6`.)

### Long soak — simulate a quiet trading day

```bash
cd tools && go run ./cmd/stress-test \
  -tier=day \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER" \
  -log-file=/tmp/soak-$(date +%Y%m%d-%H%M%S).log
```

Runs until you `Ctrl+C`. ~10 orders/min, balance-neutral by design (MMs and
takers work both sides of the book), so traders don't drift toward zero
balance over hours. The `-log-file` duplicates all output to a file so you can
`tail -f` it from another terminal.

### Perpetual mode with any tier

Pass `-duration=0` to force every trader Persistent; the run continues until
`Ctrl+C`:

```bash
cd tools && go run ./cmd/stress-test -tier=L3 -duration=0 \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

---

## Stress tiers

Tiers are preset persona mixes + durations. Pick one with `-tier=`.

| Tier     | MM | Taker | Walker | Whale | Flicker | Total | Default Duration | Notes |
|----------|----|-------|--------|-------|---------|-------|------------------|-------|
| L1       | 1  | 1     | 1      | 0     | 0       | 3     | 60s              | Smoke test |
| L2       | 2  | 3     | 4      | 1     | 0       | 10    | 5m               | Moderate load |
| L3       | 5  | 15    | 25     | 3     | 2       | 50    | 10m              | Heavy load |
| L4       | 10 | 60    | 100    | 20    | 10      | 200   | 15m              | Stress |
| L5       | 20 | 150   | 250    | 50    | 30      | 500   | 30s              | Max throughput burst |
| day      | 2  | 2     | 1      | 0     | 0       | 5     | until SIGTERM    | Long-running soak, static mid=100, slow cadence |
| btc-day  | 2  | 2     | 1      | 0     | 0       | 5     | until SIGTERM    | Soak tracking live BTC/USD (CoinGecko); qty 0.005–0.1 BTC |
| eth-day  | 2  | 2     | 1      | 0     | 0       | 5     | until SIGTERM    | Soak tracking live ETH/USD (CoinGecko); qty 0.1–1 ETH |

**L1–L5** — throughput / contention tests (static pricing around mid = 100).
**day** — multi-hour FLR/USDT correctness soak; slow cadence, tight price bands,
balance-neutral, Persistent traders.
**btc-day / eth-day** — same cadence as `day`, but the mid floats with the real
CoinGecko price and qty bounds are scaled for high-priced assets. Use these
when you target `-pair=BTC/USDT` or `-pair=ETH/USDT`; a plain `day` tier would
send orders at $100/BTC and is useless for those books.

### Persistent vs ephemeral traders

- **Market-makers are always Persistent** — they run until `Ctrl+C` regardless
  of `-duration`. This is deliberate: if an MM stopped mid-run, the book would
  go one-sided immediately.
- With `-duration=0` or `-tier=day`, **every trader is Persistent**.
- `Ctrl+C` always triggers the final sweep (cancel open orders + withdraw).

---

## Personas

| Persona  | What it does                                              | Default cadence |
|----------|-----------------------------------------------------------|------------------|
| mm       | Posts limit orders on both sides of mid, cancels stale     | 3s refresh |
| taker    | Aggressive market orders crossing the book                 | 500ms |
| walker   | Random side / type / price within tier bounds              | 500ms–2s random |
| whale    | Occasional very large market orders                        | 30s |
| flicker  | Alternates place / cancel rapidly (tests cancel path)      | 200ms |

All five are exercised by the L-tiers; `day`, `btc-day`, and `eth-day` use
only mm + taker + walker.

---

## Flags reference

All flags for `cd tools && go run ./cmd/stress-test`:

| Flag | Default | Meaning |
|------|---------|---------|
| `-instructionSender` | *(required)* | InstructionSender contract address. Pass `$INSTRUCTION_SENDER`. |
| `-tier` | `L1` | Tier preset: `L1`, `L2`, `L3`, `L4`, `L5`, `day`, `btc-day`, `eth-day`. Case-insensitive. |
| `-traders` | `0` | Override total trader count. `0` = use the tier's total. |
| `-persona-mix` | *(unset)* | Override mix. Format: `mm:2,taker:3,walker:5,whale:1,flicker:1`. Any persona omitted counts as 0. |
| `-duration` | `0` | How long to run ephemeral traders. `0` = perpetual (all traders Persistent). Overrides the tier default. Format: `30s`, `5m`, `2h`. |
| `-pair` | `FLR/USDT` | Trading pair name as defined in `config/pairs.json`. The symbol before the `/` selects `BASE_TOKEN_<SYMBOL>` from `config/test-tokens.env`. |
| `-keys` | `./traders.json` | Cache file for trader private keys. Reused across runs so traders keep their balances. |
| `-fund-per-trader` | `50000000000000000` | Native wei per trader at funding time (0.05 FLR). |
| `-fund-min` | `10000000000000000` | Top-up threshold — traders below this (0.01 FLR) get refilled. |
| `-mint` | `1000000` | Human token amount to mint per trader per side (scaled by `decimals()`). |
| `-deposit` | `100000` | Human token amount to deposit per trader per side. |
| `-log-file` | *(unset)* | Also write all log output to this file. Console output preserved. |
| `-price-symbol` | *(unset)* | CoinGecko asset id (`bitcoin`, `ethereum`, …). Overrides the tier's built-in oracle symbol. Pass `""` to force an oracle tier (`btc-day`/`eth-day`) into static pricing. |
| `-price-interval` | `60s` | Oracle poll interval. Clamped to `>=30s` to respect CoinGecko's free-tier rate limit. |
| `-price-vs-currency` | `usd` | CoinGecko `vs_currencies` param. |
| `-a` | `config.AddressesFile` | Deployed addresses file. |
| `-c` | `config.ChainNodeURL` | Chain node URL (overrides `.env`). |
| `-p` | `config.ExtensionProxyURL` | Extension proxy URL (overrides `.env`). |

### Environment variables

- `QUOTE_TOKEN` — required. Shared TUSDT quote address for every pair.
- `BASE_TOKEN_<SYMBOL>` — one per pair (`BASE_TOKEN_FLR`, `BASE_TOKEN_BTC`,
  `BASE_TOKEN_ETH`). `stress-test` selects the right one from the `-pair`
  flag (the symbol before the `/`). Falls back to `BASE_TOKEN` if unset.
- `BASE_TOKEN` — legacy single-base variable, points at the FLR base. Kept for
  `test-deposit` and `test-withdraw`.
- All of the above are populated by `test-setup` into `config/test-tokens.env`.
- `DEPLOYMENT_PRIVATE_KEY` — used to fund traders.

### Targeting a specific pair

`-tier` and `-pair` do two different things:

- **`-tier`** picks the persona mix, cadence, qty bounds, and pricing mode
  (static vs CoinGecko oracle).
- **`-pair`** picks which orderbook the resulting orders are sent to, and
  selects the matching `BASE_TOKEN_<SYMBOL>` env var for bootstrap.

For the oracle soak tiers, the tier already knows which asset to price from
CoinGecko, but you still need `-pair` so orders route to the right book:

```bash
# Default — FLR/USDT (static mid=100, no oracle)
cd tools && go run ./cmd/stress-test -tier=L2 -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"

# BTC/USDT with live BTC/USD mid from CoinGecko and BTC-sized qty
cd tools && go run ./cmd/stress-test -tier=btc-day -pair=BTC/USDT \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"

# ETH/USDT with live ETH/USD mid and ETH-sized qty
cd tools && go run ./cmd/stress-test -tier=eth-day -pair=ETH/USDT \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"

# Stress-test BTC/USDT with static pricing (e.g. oracle is down)
cd tools && go run ./cmd/stress-test -tier=L2 -pair=BTC/USDT -price-symbol="" \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

Any tier combines with any pair, but the oracle tiers (`btc-day`, `eth-day`)
only make sense with the matching pair — a plain `-tier=day -pair=BTC/USDT`
would post BTC orders at $100 and drain balances immediately.

`stress-test` targets one pair per invocation. To generate activity on
multiple pairs in parallel, launch one process per pair with distinct `-keys`
cache files so traders don't collide.

---

## What you can configure, at a glance

| Dimension | How |
|-----------|-----|
| **Number of traders** | `-traders=N`, or choose a tier |
| **Which personas / ratio** | `-persona-mix=mm:X,taker:Y,...`, or choose a tier |
| **Run length** | `-duration=5m` (or `0` for perpetual, or `-tier=day` for SIGTERM-only) |
| **Trading pair** | `-pair=FLR/USDT` (must be in `config/pairs.json`) |
| **Per-trader gas funding** | `-fund-per-trader=<wei>` + `-fund-min=<wei>` |
| **Per-trader token supply** | `-mint=<human>` |
| **Per-trader deposit** | `-deposit=<human>` |
| **Log to file** | `-log-file=/tmp/run.log` |
| **Mid / spread / walker bounds / cadences** | Baked into each tier in [`tiers.go`](../tools/cmd/stress-test/tiers.go). Edit the tier definition if you need different prices; the CLI doesn't expose these directly. |

---

## Metrics output

During a run:

- **Every 60s** — one-line compact status per action:
  `status place_order[ok=423 p50=120ms p95=340ms err=2%] | cancel_order[...]`
- **Every 5 min** — full per-action breakdown with p50 / p95 / p99 / error buckets.

On exit:

- `=== FINAL METRICS ===` with the cumulative per-action snapshot.

Error buckets: `timeout`, `insufficient_balance`, `server_error`, `other`.

---

## Common workflows

### "I want to see orders in the UI right now"

```bash
cd tools && go run ./cmd/stress-test -tier=L1 -duration=5m \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

Open the frontend — you'll see the book fill in within a few seconds.

### "I want a populated book to manually trade against"

Persistent market-makers only, no takers eating the book:

```bash
cd tools && go run ./cmd/stress-test \
  -persona-mix=mm:3 -traders=3 -duration=0 \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER"
```

Place orders from the frontend against the resting quotes; `Ctrl+C` to sweep.

### "I want to run a multi-hour correctness soak overnight"

```bash
cd tools && nohup go run ./cmd/stress-test -tier=day \
  -a "$ADDRESSES_FILE" -c "$CHAIN_URL" -p "$EXT_PROXY_URL" -instructionSender "$INSTRUCTION_SENDER" \
  -log-file=/tmp/soak-$(date +%Y%m%d).log &
```

Inspect with `tail -f /tmp/soak-YYYYMMDD.log`. `kill -TERM <pid>` triggers the
sweep cleanly.

### "I want to reuse yesterday's traders (keep their balances, skip re-funding)"

Keep the same `-keys` file (default `./traders.json`). The funder tops up only
traders below `-fund-min`, and bootstrap skips mint/approve/deposit for
already-funded traders.

---

## Known limits

- Traders use their own EOAs, so **gas cost scales linearly** with N. L5 (500
  traders × 0.05 FLR) is ~25 FLR at startup.
- Coston2 public RPCs have rate limits; bootstrap concurrency is capped at 8.
- Each live order is one proxy pending-request slot; the proxy's default cap is
  10 000. L5 with lots of resting orders can approach this.
- If a run crashes before sweep, the next run's cleanup phase will cancel the
  stale orders automatically.

---

## See also

- [`tools/cmd/stress-test/README.md`](../tools/cmd/stress-test/README.md) —
  short CLI reference.
- [`docs/testing.md`](./testing.md) — manual test flows (single deposit /
  order / withdraw).
- `docs/superpowers/plans/2026-04-20-orderbook-stress-test.md` — original
  design doc.
