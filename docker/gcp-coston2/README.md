# Orderbook extension — Coston2 GCP deployment

Production deployment templates for the orderbook extension on Flare Coston2,
running on Google Cloud. Modeled after `e2e/docker/gcp-coston/` in the
`flare-foundation/tee/e2e` repo.

## Layout

The orderbook extension splits across two VMs:

| VM | Role | Subdir | Type |
|----|------|--------|------|
| 1 | Redis + `ext-proxy` — public-facing proxy that data providers call | `gcp-ext-proxy/` | Standard GCE VM |
| 2 | `extension-tee` — the orderbook business logic that signs on behalf of the extension | `gcp-extension-tee/` | **GCP Confidential VM** (required for real attestation) |

The Coston2 c-chain indexer and its MySQL DB are already deployed separately
(host `34.62.247.94` in the current test setup). This deployment reuses them —
unlike the e2e repo, we don't run our own indexer here.

The top-level `docker-compose.yaml` is a combined reference for running both
services on a single VM (development / smoke testing). **For real production,
use the split layout in the subdirectories.**

## Files

```
docker/gcp-coston2/
├── README.md                    # this file
├── docker-compose.yaml          # combined reference (single-VM)
├── proxy.config.toml            # ext-proxy config template (no secrets)
├── .env.example                 # env var template
├── gcp-ext-proxy/
│   ├── docker-compose.yaml      # VM 1: redis + ext-proxy
│   └── proxy.config.toml        # VM 1 proxy config
└── gcp-extension-tee/
    ├── README.md                # Confidential VM setup notes
    └── docker-compose.yaml      # VM 2: extension-tee
```

## Secrets — never commit

All of these are provided per-deployment via environment variables and must
come from **GCP Secret Manager** (not from a committed `.env`):

| Variable | Purpose | Notes |
|----------|---------|-------|
| `PROXY_PRIVATE_KEY` | ext-proxy signing identity | Public signing identity; unique per deploy. The Hardhat default is used in the e2e repo's prod for testing networks — fine for Coston2 test deployment, but generate a fresh key per extension. |
| `DEPLOYMENT_PRIVATE_KEY` | On-chain registration tx signer | **Must be funded on Coston2.** |
| `INDEXER_DB_HOST` | Coston2 indexer DB IP/host | External host, provided by infra team. |
| `INDEXER_DB_PASSWORD` | Coston2 indexer DB password | Provided by infra team. |
| `DIRECT_API_KEY` | API key for the ext-proxy `/direct` endpoint | Frontend uses this. |
| `INITIAL_OWNER` | Address registered as machine owner on-chain | Corresponds to `DEPLOYMENT_PRIVATE_KEY` derived address (or governance-approved owner). |

See `.env.example` for the full list.

## Image registry

Images should be pushed to GCP Artifact Registry. The e2e repo uses:

```
europe-west1-docker.pkg.dev/flare-network-staging/containers/tee-proxy:latest
```

Parameterized as `${REGISTRY}` in the compose files — set in deployment env.
For the orderbook `extension-tee`, a matching entry must be built and pushed
(it's not a public image).

## Deployment steps (high level)

1. **Build and push images**
   - `tee-proxy` — use the image from `flare-foundation/tee/tee-proxy` (or the Flare staging registry).
   - `extension-tee` — build from this repo's `Dockerfile` (build context is the repo root — see `REPRODUCIBILITY.md`) and push to your registry.

2. **Provision VMs**
   - `gcp-ext-proxy` → standard `e2-standard-2` (or similar).
   - `gcp-extension-tee` → **Confidential VM** (AMD SEV-SNP or Intel TDX), so attestation works with `SIMULATED_TEE=false`.

3. **Networking / firewall**
   - ext-proxy VM: allow ingress on 6664 (external, public — data providers hit this) and 6663 (internal, restricted to the extension-tee VM's IP).
   - extension-tee VM: no public ingress needed; egress to ext-proxy VM on 6663.

4. **Deploy on-chain pieces (from operator workstation)**
   - Deploy the `InstructionSender` contract.
   - Register the extension, get the extension ID.
   - Allow the extension-tee TEE version via the extension owner key.

5. **Configure `.env` on each VM** (from `.env.example`).

6. **Start services**
   - ext-proxy VM: `docker compose -f gcp-ext-proxy/docker-compose.yaml up -d`
   - extension-tee VM: `docker compose -f gcp-extension-tee/docker-compose.yaml up -d`

7. **Register the TEE machine on-chain** using the ext-proxy public URL as
   `--p` and an already-registered helper extension-0 proxy URL as `--ep`.
   See `e2e/README.md` section "Extensions on Coston" for the full command.

8. **Deploy the frontend separately** (static hosting / CDN). Build with:
   ```bash
   VITE_TEE_PROXY_URL=https://<ext-proxy-public-url> \
   VITE_DIRECT_API_KEY=<DIRECT_API_KEY> \
   VITE_SHOW_FAUCET=false \
   npm run build
   ```

## What's different from the current local/testing setup

| Aspect | Local/testing | GCP prod |
|--------|---------------|----------|
| Proxy URL | ngrok tunnel | Stable HTTPS via GCP LB / managed DNS |
| `SIMULATED_TEE` | `true` | `false` (real attestation on Confidential VM) |
| `VITE_SHOW_FAUCET` | `true` | `false` |
| `CHAIN_URL` | Public `coston2-api.flare.network` | Private RPC endpoint (lower rate limits) |
| Secrets | `.env` file | GCP Secret Manager |
| Network | Single Docker host | Two VMs, GCP VPC firewall |
| Port bindings | `host.docker.internal` | Real VM IPs / DNS |
| Images | `local/...` locally-built | `europe-west1-docker.pkg.dev/...` pulled |
| Logging | stdout | Promtail → Grafana Loki |
| Restart | manual | `restart: unless-stopped` |
| Memory | unlimited | `mem_limit` per service |
