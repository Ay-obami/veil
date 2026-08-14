# TEE Machine Registration

The single supported way to register a TEE machine for a Veil extension is the
`veil deploy machine` stage. It whitelists the running code, pins governance on
chain, and registers the TEE under the configured public host so it can serve
direct actions and author withdrawals. This document is the canonical reference
for that stage.

## Prerequisites

A running, measured TEE behind a reachable proxy:

- The extension image deployed on a Flare Confidential Compute VM (or locally
  via Docker), exposing `/info` over the proxy.
- The container launched with: `INITIAL_OWNER`, `CHAIN_URL`, `EXTENSION_ID`
  (from `veil status`), `PROXY_URL` (public HTTPS on the proxy port), plus any
  data-provider reachability your launch policy requires.
- Proxy `/info` verified — `platform`, `codeHash`, `extensionId`, and
  `initialOwner` must match your deployment.
- `configs/<network>.yaml` populated with:
  - `normalProxyUrl` — the public tee-proxy URL the Flare data providers call.
  - `extProxyUrl` — the proxy address `veil` queries for TEE info
    (default `http://localhost:6674` for local Docker).
  - `extProxyHostUrl` — the host registered on chain. It must be publicly
    reachable from the internet.

> If the extension image changed, re-run this stage so the new `codeHash` is
> whitelisted before trading resumes.

## Register

```bash
veil deploy machine -network coston2   # or -network coston
```

Re-running after an interruption is safe — the stage is idempotent:

```bash
veil deploy resume -network coston2
```

## What it does (in order)

1. `allow-tee-version` — whitelists the running `codeHash` on
   `FlareTeeManager` for `teeVersion` from config.
2. `set-governance` — pins `initialOwner` (and `governanceSigners`/threshold
   if set) as the extension's on-chain governance.
3. `register-tee` — registers the TEE node under `extProxyHostUrl`, attaching
   `normalProxyUrl` as the external proxy.
4. `setExtensionId` — stores the extension id on `InstructionSender`.
5. `setTeeAddress` — stores the TEE signing address on `InstructionSender`,
   enabling `executeWithdrawal()`.

After the stage, `veil status` shows `machineRegistered: true`,
`teeAddressSet: true`, `extensionIdSet: true`.

## Real-hardware note

Against real TEE hardware (`simulatedTee: false` in config), `machine` runs
`register-tee` with `-command rRap`, which decouples the one-time availability
challenge from the per-run `rap` flow so re-runs don't revert with an expired
challenge. The shipped configs set `simulatedTee: true` (local), where the
default `rap` command is used.

## Verification

```bash
veil status -network coston2
# machine: completed, machineRegistered=true, teeAddressSet=true
```

Confirm on chain that `InstructionSender.teeAddress()` returns the TEE signing
address and `executeWithdrawal()` is enabled (the deploy log prints both once
the stage finishes).

## Config → tool flag mapping

| config field        | purpose                                       | used as / by          |
| ------------------- | --------------------------------------------- | --------------------- |
| `normalProxyUrl`    | public tee-proxy URL (data-provider side)     | `register-tee -ep`    |
| `extProxyUrl`       | proxy to query for TEE info                   | `register-tee -p`     |
| `extProxyHostUrl`   | on-chain TEE host (public, internet reachable)| `register-tee -h`     |
| `teeVersion`        | code version to whitelist                     | `allow-tee-version`   |
| `initialOwner`      | governance signer / deployer                  | `set-governance`      |

## Run everything in one shot

From scratch (compile, deploy, build, register, test):

```bash
veil deploy all -network coston2
```
