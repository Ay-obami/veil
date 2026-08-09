# stress-test

Parallel mock traders against a deployed orderbook extension. Stress-tests the
TEE under concurrent deposits, limit/market orders, cancellations, and final
withdrawals.

## Prerequisites

1. Extension deployed and registered (`scripts/full-setup.sh`).
2. Test tokens deployed (`tools/cmd/test-setup`). `config/test-tokens.env` must
   be populated.
3. TEE proxy reachable.
4. `.env` has `DEPLOYMENT_PRIVATE_KEY` funded with enough native C2FLR to fund
   every trader (default 0.05 FLR each).

## Usage

    go run ./cmd/stress-test \
      -tier=L2 \
      -instructionSender=0x... \
      -duration=5m

### Perpetual mode

    go run ./cmd/stress-test -tier=L2 -instructionSender=0x... -duration=0
    # all traders persistent; runs until Ctrl+C

### Override tier

    go run ./cmd/stress-test \
      -tier=L1 \
      -traders=25 \
      -persona-mix=mm:2,taker:10,walker:10,whale:2,flicker:1 \
      -duration=15m \
      -instructionSender=0x...

## Tiers

| Tier    | MM | Taker | Walker | Whale | Flicker | Total | Duration | Mid pricing |
|---------|----|-------|--------|-------|---------|-------|----------|-------------|
| L1      | 1  | 1     | 1      | 0     | 0       | 3     | 60s      | static 100  |
| L2      | 2  | 3     | 4      | 1     | 0       | 10    | 5m       | static 100  |
| L3      | 5  | 15    | 25     | 3     | 2       | 50    | 10m      | static 100  |
| L4      | 10 | 60    | 100    | 20    | 10      | 200   | 15m      | static 100  |
| L5      | 20 | 150   | 250    | 50    | 30      | 500   | 30s      | static 100  |
| day     | 2  | 2     | 1      | 0     | 0       | 5     | SIGTERM  | static 100  |
| btc-day | 2  | 2     | 1      | 0     | 0       | 5     | SIGTERM  | **live BTC/USD via CoinGecko** |
| eth-day | 2  | 2     | 1      | 0     | 0       | 5     | SIGTERM  | **live ETH/USD via CoinGecko** |

**Stress tiers (L1–L5)** — throughput / contention testing.
**Soak tiers (day, btc-day, eth-day)** — long-running consistent activity. ~10 orders/min, runs until SIGTERM. `btc-day` / `eth-day` pull live prices from CoinGecko (polled every 60s by default; `-price-interval=…`). MM spread is ±1% of the live mid; walker bounds are the same. Qty sizing: 0.005–0.1 BTC, 0.1–1 ETH, so a default $100k deposit covers many hours.

**Persistent trader rule:** every market-maker is Persistent. With
`-duration=0` (or `-tier=day`), every trader is Persistent. Ctrl+C always triggers the sweep.

## Log file (long runs)

Use `-log-file=path` to duplicate all output to a file; console output is preserved. Lets you `tail -f path` from another terminal without worrying about pipe buffering:

    go run ./cmd/stress-test -tier=day \
      -instructionSender=0x... \
      -log-file=/tmp/soak-$(date +%Y%m%d-%H%M%S).log

One-line compact status is emitted every 60s, full per-action snapshot every 5 min.

## Personas

- **mm** — market-maker, posts limit orders around mid on both sides
- **taker** — aggressive market orders crossing the book
- **walker** — random side/type/price within bounds
- **whale** — occasional large market orders (30s between)
- **flicker** — alternates place/cancel rapidly

## What gets exercised

- Deposit flow (on-chain, once per trader at startup)
- PLACE_ORDER direct instruction (limit + market)
- CANCEL_ORDER direct instruction (flicker + sweep)
- Withdraw flow (on-chain, once per trader at sweep)
- Extension mutex, per-pair orderbook mutex, balance-manager mutex

## Known limits

- Traders use their own EOAs, so gas costs scale linearly with N.
- Coston2 public RPCs have rate limits; bootstrap concurrency capped at 8.
- Each order is 1 proxy pending-request slot; total pending capped at 10000
  by the proxy config. L5 (500 traders) can approach this.
