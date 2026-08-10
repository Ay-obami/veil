# VM 2 — orderbook extension-tee on GCP Confidential VM

This is the orderbook business logic. It signs match-engine instructions as a
TEE and is the component that **requires genuine attestation** — deploy it
on a GCP Confidential VM, not a plain GCE instance.

## Confidential VM requirements

- Machine type: `n2d-standard-*` (AMD SEV-SNP) or `c3-standard-*` (Intel TDX).
- `--confidential-compute-type=SEV_SNP` (or `TDX`) on `gcloud compute instances create`.
- Boot disk image: a Confidential-Space-compatible Ubuntu or Container-Optimized OS.
- `SIMULATED_TEE=false` in the env file — without a real TEE, the attestation
  call will fail and the on-chain registration will be rejected.

## Files to copy onto the VM

- `docker-compose.yaml` (this directory)
- `pairs.json` — copy from this repo's `config/coston2/pairs.json`
- `.env` — populated from `../.env.example`

## Networking

- No public ingress. Only outbound to:
  - ext-proxy VM on port 6663 (via `PROXY_URL`)
  - Coston2 RPC for chain reads
- Put the extension-tee VM in the same GCP VPC as the ext-proxy VM and
  reference it by internal IP for lower latency and no public exposure.

## First-run checklist

1. `PROXY_URL=http://<ext-proxy-internal-ip>:6663` set in `.env`.
2. `EXTENSION_ID` set to the on-chain ID assigned when the extension was
   registered (see `e2e/README.md` → "Extensions on Coston").
3. `INITIAL_OWNER` set to an address in the extension's owner allowlist.
4. `SIMULATED_TEE=false`.
5. `MODE` set correctly for remote build (confirm with extension code what
   `MODE=1` vs `MODE=0` means — the e2e `tee-node` uses `Mode=0` for remote).
6. TEE version has been allowed on-chain by the extension owner (the hash
   must match the image being run).

## After starting

From an operator workstation (not the VM), register the TEE machine:

```bash
PRIV_KEY=0x<owner_key> go run ./cmd/setup/setup.go --a configs/coston2_addresses.json --c https://coston2-api.flare.network/ext/C/rpc --p https://<ext-proxy-public-url> --ep https://<helper-extension-0-proxy-url>
```

The `--ep` flag needs an already-registered extension-0 proxy that helps
confirm the new registration.
