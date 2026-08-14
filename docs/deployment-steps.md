# 🚀 TEE Extension Deployment — Step by Step

Linear recipe to deploy a TEE extension to Flare Coston or Coston2. Run the steps top to bottom.

> This is the manual, first-time walkthrough — generating a deployer key,
> handing an image off to a GCP Confidential Space operator, verifying
> `/info` by hand. Steps 4, 8, and 9 below are now single `veil` commands
> instead of shell scripts; see [deployment.md](deployment.md) for how the
> engine behind them works, how to resume a failed run, and how rollback
> works. Everyday re-deploys (image changed, diamond redeployed) are
> covered at the bottom of this doc and are entirely `veil` commands.

## Prerequisites

- 🐳 Docker Desktop (Linux containers)
- 🐹 Go 1.25.1+
- 🔨 Foundry (`forge`, `cast`)
- `jq`
- Bash (Git Bash on Windows works)
- VPN access to Flare's indexer DB (`35.241.249.150:3306`)

## 1. Clone sibling repos

`tee-proxy` runs as a separate service alongside the extension and still
needs its own sibling checkout for this deployment flow (unexamined and
unchanged by anything in this repo). `tee-node`, however, no longer needs
to be a sibling for *this extension's* own Dockerfile build — `go.mod`
now pins it as a normal published dependency (see `BUILD_NOTES.md`'s
"Side quest: actually trying option 1"), so `tee-node/` below is only
needed if some other part of your deployment flow still expects it, not
for building this extension's own image.

```text
<workspace>/tee/
├── tee-node/         # gitlab.com/flarenetwork/tee/tee-node, tag v0.0.20 — see note above
├── tee-proxy/        # gitlab.com/flarenetwork/tee/tee-proxy, tag v0.0.17
└── extensions/
    └── <your-extension>/
```

## 2. Generate a funded deployer key

```bash
cast wallet new
cast wallet address --private-key 0x<private-key>
```

The derived address becomes your `INITIAL_OWNER`. Fund it from the target chain's faucet.

| Chain   | Faucet                                 |
| ------- | -------------------------------------- |
| Coston  | `https://faucet.flare.network/coston`  |
| Coston2 | `https://faucet.flare.network/coston2` |

## 3. Create `configs/<chain>.yaml` and export secrets

`veil` reads static config from `configs/coston.yaml` / `configs/coston2.yaml`
(already checked in — edit them if your `initialOwner`, `adminAddresses`, or
governance settings differ from the defaults) and reads secrets from your
shell:

```bash
export DEPLOYMENT_PRIVATE_KEY=<private key, no 0x prefix>
export PROXY_PRIVATE_KEY=<proxy signing key>
```

`INITIAL_OWNER` in the old `.env.<chain>` maps to `initialOwner:` in the
config file — set it to the address from Step 2.

## 4. Register the extension on-chain

```bash
veil deploy contracts -network coston2   # or -network coston
```

Compiles Solidity, deploys `InstructionSender`, registers the extension
on-chain, provisions test pairs/tokens. Everything it produces is recorded
in `deployment/deployment.json`:

```bash
veil status -network coston2
```

`extensionId` and `instructionSender` there are what Step 6 hands off.

## 5. Build the Docker image

Confirm `MODE=0` is the default in your extension's `Dockerfile` (`MODE=0` is the production attestation backend; `MODE=1` produces simulated attestation that FTDC rejects):

```dockerfile
ENV MODE=0 CONFIG_PORT=5501 SIGN_PORT=7701 EXTENSION_PORT=7702
```

Then build:

```powershell
$env:SOURCE_DATE_EPOCH = (git log -1 --format=%ct)
docker compose -f docker-compose.yaml build --no-cache extension-tee
docker tag <your-extension>-extension-tee:latest <your-extension>:v0.1.0
docker save <your-extension>:v0.1.0 -o <your-extension>-v0.1.0.tar
```

Setting `SOURCE_DATE_EPOCH` makes the build reproducible (same source → same `codeHash`).

Verify `MODE=0` is baked into the image:

```powershell
docker inspect <your-extension>:v0.1.0 --format '{{range .Config.Env}}{{println .}}{{end}}' | Select-String MODE
# expected: MODE=0
```

## 6. Deploy the image on a Confidential Space VM

Hand off (or deploy yourself) to a GCP Confidential Space VM with:

- The image (tar or registry URL+tag)
- Workload-launch env: `INITIAL_OWNER`, `CHAIN_URL`, `EXTENSION_ID` (from Step 4), `PROXY_URL` (proxy URL reachable from the TEE)
- Public HTTPS routed to port `6664` of the proxy container

You receive back the **public proxy URL**. Set it in `configs/<chain>.yaml`:

```yaml
# in configs/coston2.yaml (or coston.yaml)
extProxyUrl: <public proxy URL>
```

## 7. Verify the proxy `/info`

```powershell
curl -s $env:EXT_PROXY_URL/info | jq '.machineData'
```

Required values:

| Field          | Expected                                                          |
| -------------- | ------------------------------------------------------------------ |
| `platform`     | starts with `0x4743505f414d445f534556…` (GCP_AMD_SEV)             |
| `codeHash`     | real measured hash (**not** `0x194844cf…` — that's simulated)     |
| `extensionId`  | matches `extensionId` in `deployment/deployment.json`             |
| `initialOwner` | matches `initialOwner` in your `configs/<chain>.yaml`              |

If `extensionId` is wrong, ask the VM operator to restart the container with the correct `EXTENSION_ID` env override (no image rebuild needed — it's a launch-policy override).

## 8. Register the TEE machine

```bash
veil deploy machine -network coston2
```

Runs `allow-tee-version` (whitelists the codeHash), `set-governance`,
`register-tee`, `setExtensionId`, and `setTeeAddress`.

For the full config → tool-flag mapping and post-registration verification,
see [docs/tee-registration.md](tee-registration.md).

> [!NOTE]
> Against real hardware (`simulatedTee: false`, which both shipped configs
> set), `machine` automatically passes `register-tee -command rRap`
> instead of the tool's own default `rap`. Step `a` (availability check)
> needs a one-time **challenge** — a random number from the contract that
> the TEE signs to prove it's alive. By default only `r` issues it, but
> `r` skips itself once the TEE is registered on-chain, so re-runs (image
> changes, diamond cuts, retries) would otherwise revert with
> `Verification.ChallengeExpired`. Capital `R` issues the challenge
> directly, decoupled from `r`, so re-runs keep working — see
> [deployment.md](deployment.md#known-deliberate-gaps).

## 9. End-to-end test

```bash
veil deploy smoke -network coston2
```

Sends test instructions through the deployed TEE and verifies the round-trip.

Or run everything from Step 4 through here in one command:

```bash
veil deploy all -network coston2
```

It's resumable — if any stage fails, fix the issue and run
`veil deploy resume -network coston2` to continue from wherever it stopped.

---

## When the extension image changes

1. Rebuild and hand off the new image.
2. The VM is re-deployed → `codeHash` changes.
3. `veil deploy machine -network coston2` whitelists the new codeHash.
4. `veil deploy smoke -network coston2`.

## When the `FlareTeeManager` diamond is re-deployed

All extension registrations on that chain are wiped:

1. `veil deploy contracts -network coston2` — mints a fresh `extensionId`
   (check `veil status` for the new value).
2. Send the new `extensionId` to the VM operator. They restart the
   container with `EXTENSION_ID=<new value>` as a launch-policy env
   override — no image rebuild needed.
3. Re-curl `/info` and confirm `extensionId` matches.
4. `veil deploy machine -network coston2`.
5. `veil deploy smoke -network coston2`.
