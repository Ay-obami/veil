# Encrypted State Backup — Staging Validation Runbook (Confidential VM)

The state-preservation code (Phases 1–5) is fully implemented and covered by CI
with fakes (`MemorySealer`, `FakeIssuer`, in-memory `BlobStore`). This runbook
exercises the parts CI **cannot**: the real vTPM sealer and real GCS
export/restore, on a live GCP Confidential VM.

Design: [`state-backup.md`](state-backup.md) ·
Spec: [`superpowers/specs/2026-05-13-encrypted-state-backup-design.md`](superpowers/specs/2026-05-13-encrypted-state-backup-design.md)

## Scope

| Path | Covered here | Notes |
|---|---|---|
| Real vTPM seal/unseal of `DEK_local` (Phase 5) | ✅ | `/dev/tpmrm0` exists on a Confidential VM |
| Real GCS off-site export + restore (Phases 3–4) | ✅ | Real bucket, real `latest.bin`, real handshake |
| Real Confidential Space attestation (Phase 4) | ❌ deferred | `teeserver.sock` exists only on Confidential **Space**, not a plain Confidential **VM**. Validated in a follow-up CS pass; here the TEE issues `FakeIssuer` tokens and the admin CLI runs with `-fake-attestation`. |

A plain GCP **Confidential VM** (SEV-SNP / TDX) gives us the hardware vTPM but
not the Confidential Space container-launcher socket. That is why attestation is
faked here and real attestation is a separate milestone.

## Inputs — resolved for the `flare-network-sandbox` project

Discovered via `gcloud` against the active `flare-network-sandbox` project; the
commands below are pre-filled with these:

| Value | Resolved | Notes |
|---|---|---|
| GCP project | `flare-network-sandbox` | active gcloud config |
| Zone | `us-central1-a` | where the existing orderbook/TEE VMs run |
| Machine type | `n2d-standard-4`, `--confidential-compute-type=SEV_SNP` | verified available in-zone; SEV precedent `jt-amd-sev-test` |
| VM service account | `confidential-sa@flare-network-sandbox.iam.gserviceaccount.com` | purpose-built; already has `artifactregistry.reader` + confidential-compute roles; **needs** a bucket-scoped `storage.objectAdmin` grant |
| Image registry | `us-docker.pkg.dev/flare-network-sandbox/flare-tee/orderbook-extension-tee` | `flare-tee` repo (location `us`); no orderbook image there yet |
| GCS bucket | `gs://flare-network-sandbox-orderbook-state` | free to create (location `us-central1`) |
| Admin address / pubkey | Step 1 (`admin-keygen`) | a throwaway staging keypair is fine here |
| TEE code hash | proxy `/info` after first boot (`MachineData.CodeHash`) | `STATE_CODE_HASH`; identical on both VMs |

## Step 1 — Generate admin restore keys

ECIES envelope-wrapping needs the *public* key, not just the address (and `cast`
won't print the uncompressed form cleanly), so use the helper added for this:

```
cd tools && go run ./cmd/admin-keygen -gen 1
```

It prints the private key (store it safely — this is a restore recipient), the
lowercase address, and the ready-to-paste `ADMIN_ADDRESSES` / `ADMIN_PUBLIC_KEYS`
lines. To derive from a key you already control instead:

```
cd tools && go run ./cmd/admin-keygen -keys 0x<admin_priv_hex>
```

Each address in `ADMIN_PUBLIC_KEYS` must also be in `ADMIN_ADDRESSES` or the TEE
logs a warning and drops that key as a restore recipient.

## Step 2 — Create the GCS bucket and grant the VM

Create the bucket (uniform access), then grant the Confidential VM's service
account object admin on it. The Go GCS client uses the VM's attached service
account via ADC automatically — no key file is mounted into the container.

```
gcloud storage buckets create gs://flare-network-sandbox-orderbook-state --project=flare-network-sandbox --location=us-central1 --uniform-bucket-level-access
```

```
gcloud storage buckets add-iam-policy-binding gs://flare-network-sandbox-orderbook-state --member="serviceAccount:confidential-sa@flare-network-sandbox.iam.gserviceaccount.com" --role=roles/storage.objectAdmin
```

Optional but recommended (matches the spec's 24h admin-rotation story): a
lifecycle rule that deletes backups after a day.

## Step 3 — Build and push the extension image (MODE=0)

Reproducible build (see [`REPRODUCIBILITY.md`](../REPRODUCIBILITY.md)); note the
build context is the parent `tee/` dir and `MODE=0` for real attestation:

```
docker buildx create --driver=docker-container --name=moby-buildkit --driver-opt image=moby/buildkit --bootstrap
```

```
gcloud auth configure-docker us-docker.pkg.dev --quiet
```

```
docker buildx build --builder moby-buildkit --platform linux/amd64 --build-arg SOURCE_DATE_EPOCH=$(git log -1 --format=%ct) --build-arg MODE=0 --build-arg NETWORK=coston2 -t us-docker.pkg.dev/flare-network-sandbox/flare-tee/orderbook-extension-tee:staging-state --push -f Dockerfile ../..
```

Record the pushed digest — it is `EXTENSION_TEE_IMAGE` for the compose file.

## Step 4 — Launch the Confidential VM

SEV-SNP on an `n2d-standard-*` machine, attaching the service account from Step 2
(the existing `testing/scripts/launch-gcp.sh` creates a **non**-confidential
testing VM — do not reuse it for this):

```
gcloud compute instances create orderbook-state-cvm --project=flare-network-sandbox --zone=us-central1-a --machine-type=n2d-standard-4 --confidential-compute-type=SEV_SNP --maintenance-policy=TERMINATE --service-account=confidential-sa@flare-network-sandbox.iam.gserviceaccount.com --scopes=cloud-platform --image-family=ubuntu-2204-lts --image-project=ubuntu-os-cloud --boot-disk-size=50GB
```

SSH in, install Docker, and copy `docker-compose.yaml`, `pairs.json`, and your
populated `.env` per
[`docker/gcp-coston2/gcp-extension-tee/README.md`](../docker/gcp-coston2/gcp-extension-tee/README.md).

## Step 5 — State-enabling compose overlay

Do **not** edit the production `docker-compose.yaml`. Drop this
`docker-compose.override.yaml` next to it — compose auto-merges it — to add the
state env, the TPM device, and a persistent state volume:

```yaml
# docker-compose.override.yaml — staging state-preservation validation only.
services:
  extension-tee:
    environment:
      - SIMULATED_TEE=false
      - STATE_DIR=/var/lib/orderbook/state
      - STATE_VTPM_INDEX=1                 # any non-empty value enables the real TPMSealer
      - STATE_VTPM_DEVICE=/dev/tpmrm0
      - STATE_VTPM_PCRS=0,1,2,3,4,5,6,7
      - STATE_SNAPSHOT_INTERVAL=30s
      - STATE_GCS_BUCKET=${STATE_GCS_BUCKET:?set STATE_GCS_BUCKET}
      - STATE_GCS_PREFIX=orderbook-state/
      - STATE_GCS_INTERVAL=2m
      - STATE_CODE_HASH=${STATE_CODE_HASH:?must match the running image code hash; identical on both VMs}
      - ADMIN_ADDRESSES=${ADMIN_ADDRESSES:?}
      - ADMIN_PUBLIC_KEYS=${ADMIN_PUBLIC_KEYS:?}
      # STATE_ATTESTATION_SOCKET deliberately unset → FakeIssuer (no teeserver socket on a plain CVM)
    devices:
      - /dev/tpmrm0:/dev/tpmrm0
    volumes:
      - orderbook-state:/var/lib/orderbook/state
volumes:
  orderbook-state:
```

`STATE_CODE_HASH`: after first boot, read the code hash from the proxy `/info`
response (`MachineData.CodeHash`) and set it here. It is baked into the AES-GCM
AAD, so the exporting VM and the restoring VM (V4) **must** use the identical
value or restore fails the tag check.

Bring it up, then register the TEE version + machine on-chain as usual
(`veil deploy machine`).

## Validation scenarios

### V1 — vTPM seal on first boot
Fresh state dir, no `latest.bin` yet → greenfield boot seals a new `DEK_local`.

- Expect log line: `state: using vTPM sealer device=/dev/tpmrm0 pcrs=[0 1 2 3 4 5 6 7]`.
- Expect `dek.sealed`, `snapshot.enc`, `log.enc` in the `orderbook-state` volume
  (`docker compose exec` won't work — the image is distroless with no shell;
  inspect from the host at the volume's mountpoint under
  `/var/lib/docker/volumes/`).
- Place a few orders / deposits so there is state worth preserving.

### V2 — unseal survives a restart
Proves same-VM recovery: the TPM releases `DEK_local` only to this image on this VM.

- Container restart: `docker compose restart extension-tee` → state loads from
  `snapshot.enc` + `log.enc` replay; balances/orders intact.
- Stronger — full VM reboot: `gcloud compute instances reset orderbook-state-cvm`
  → after boot the named volume persists and the same TPM unseals; state intact.
  (PCRs 0–7 on a CVM reflect firmware/boot, stable across reboot. On Confidential
  Space the selection would also include image-measurement PCRs.)

### V3 — off-site GCS export
Within `STATE_GCS_INTERVAL` (2m) a signed blob should appear:

```
gcloud storage ls gs://flare-network-sandbox-orderbook-state/orderbook-state/
```

Expect `latest.bin` plus timestamped blobs. To force one immediately, send the
`BACKUP_NOW` admin command. Sanity-check locally that it verifies and is a
recipient-addressed envelope (`admin-restore` does this in V4).

### V4 — disaster recovery on a fresh VM (the real test)
Destroy the VM (or its boot disk) so the sealed `DEK_local` is gone forever, then
boot a **fresh** CVM against the **same bucket** and identical `STATE_CODE_HASH` /
`ADMIN_PUBLIC_KEYS`.

- Fresh VM finds no `dek.sealed` but a `latest.bin` in GCS → boots
  `AwaitingRestore`: every action except `RESTORE_BEGIN` / `RESTORE_SUBMIT`
  returns `503 awaiting-restore`; the snapshot/export loop stays stopped.
- From your workstation, mint a signed URL for the blob and run the handshake
  with the admin key from Step 1. On a CVM use `-fake-attestation` (no real CS
  attestation to verify; the nonce→`ephPk` binding is still checked):

```
gcloud storage sign-url gs://flare-network-sandbox-orderbook-state/orderbook-state/latest.bin --duration=10m --impersonate-service-account=confidential-sa@flare-network-sandbox.iam.gserviceaccount.com
```

```
ADMIN_PRIVATE_KEY=0x<admin_priv> go run ./cmd/admin-restore -p <ext-proxy-url> -tee <tee-info-url> -blob "<signed-latest.bin-url>" -fake-attestation
```

- Expect `RESTORE_SUBMIT` to return `{CodeHash, CapturedAt}`; the TEE then
  materializes the plaintext snapshot in RAM only, `Apply`s it, seals a fresh
  `DEK_local` to *this* VM's TPM, writes a new `snapshot.enc` + empty `log.enc`,
  and opens for traffic. Confirm the restored balances/orders match pre-disaster.

## Validation run — 2026-07-09 (results)

Executed on a live SEV-SNP Confidential VM `orderbook-state-cvm` (n2d-standard-4,
us-central1-a, project `flare-network-sandbox`), running the real extension image
`us-docker.pkg.dev/flare-network-sandbox/flare-tee/orderbook-extension-tee:staging-state`
(built from `Dockerfile.staging`, `MODE=1`/`SIMULATED_TEE=true`, real vTPM + real
GCS). State was seeded with 4 balances across 2 users via the legacy-migration
path; the direct-`/action` driver `tools/cmd/state-validate` drove queries and the
restore handshake (no proxy).

| Scenario | Result |
|---|---|
| V1 — real vTPM seal on first boot | ✅ `dek.sealed` written by the actual TPM; `state: using vTPM sealer` |
| V3 — real GCS export | ✅ signed `latest.bin` (+timestamped) uploaded via `confidential-sa` ADC |
| V2 — unseal across **container** restart | ✅ vTPM unsealed `DEK_local`; balances intact |
| V2-strong — unseal across **VM reboot** | ❌ **FAILS** — see Finding 1 |
| V4 — disaster recovery (wipe disk → restore from GCS) | ✅ AwaitingRestore 503-gate → handshake → all 4 balances recovered from GCS |

## Findings

**Finding 1 (root cause, narrowed by experiment) — a DEK sealed on the VM's *first*
boot cannot be unsealed after the first reboot.** After a reboot, unseal fails with
`tpm unseal: session 1, error code 0x1d : a policy check failed` (`TPM_RC_POLICY_FAIL`).
Experiment: captured `sha256:0-7` PCRs, rebooted, captured again — **PCRs 0–7 are
byte-identical across reboots**, and a DEK sealed *after* a reboot unseals fine on
every later reboot (verified: state survived a subsequent reboot). So the PCR
divergence is specifically **first-boot (instance creation) vs any rebooted boot** —
a known GCP CVM behavior. CI can't catch this — the `tpmsim` simulator has stable
PCRs. Practical impact: a TEE that seals `DEK_local` on its very first boot will
fail to unseal after its first reboot; every reboot after that is stable. Fully
mitigated by Finding 2's fix (first-reboot failure → off-site restore → reseal on a
rebooted boot → stable thereafter). Optional hardening: reseal once after the first
reboot, or exclude the first-boot-only PCR (requires capturing first-boot values).

**Finding 2 (robustness) — unseal failure crash-looped instead of falling back — FIXED.**
Previously, when the sealed `DEK_local` couldn't be unsealed but a valid GCS backup
existed, boot called `logger.Fatalf` and the container crash-looped instead of
recovering off-site — so a first reboot (Finding 1) bricked the node even though the
state was safely in GCS. Fixed in `pkg/state/manager.go` `LoadAtBoot`: an unseal
*error* now funnels into the same off-site-backup check as a missing DEK — if
`latest.bin` exists it enters AwaitingRestore (graceful recovery); if no backup
exists it still fails loudly rather than silently rotating to a fresh DEK and
discarding the sealed snapshot. Covered by `TestLoadAtBoot_UnsealError_BlobPresent_AwaitsRestore`
and `TestLoadAtBoot_UnsealError_NoBlob_Errors`.

**Finding 3 (build) — branch can't build a deployable image.** `go.mod` requires
`go 1.25.8` (bumped in the Phase 5 commit) but the production `Dockerfile` base
`golang:1.25.1-trixie` ships an older Go with `GOTOOLCHAIN=local`, so `go mod
download` fails. Worked around with `Dockerfile.staging` (`GOTOOLCHAIN=auto`); the
production base must be bumped to Go ≥1.25.8.

**Finding 4 (build) — `tools/` module can't build.** `tools/go.sum` was missing the
`go-tpm` entries that `pkg/state` now imports (Phase 5), so **every** command in
`tools/` failed to build (`admin-restore`, `admin-keygen`, `state-validate`).
Fixed here via `go mod tidy` (updated `tools/go.mod`, `tools/go.sum`).

## Gotchas

- **Distroless image has no shell** — `docker compose exec` / `docker exec … sh`
  fails. Inspect state files from the host volume mountpoint; read behavior from
  container logs (`docker compose logs -f extension-tee`).
- **`STATE_CODE_HASH` must match on both VMs** (it is in the AAD) and should equal
  the image's real code hash from proxy `/info`. A mismatch fails restore at the
  AES-GCM tag, not with a clear error.
- **Admin sets must match** — a blob is only decryptable by admins whose pubkeys
  were in `ADMIN_PUBLIC_KEYS` *when it was written*. Adding an admin later does
  not grant access to older blobs (intended; see spec risk #4).
- **Future Confidential Space pass**: the image's launch-policy label
  (`tee.launch_policy.allow_env_override` in the Dockerfile) does **not** list any
  `STATE_*` / `ADMIN_*` var. On Confidential Space those must be either baked into
  the image or added to that label, or the CVM will reject the overrides at
  attestation time. Irrelevant here (docker-compose sets env freely), but required
  before the CS attestation milestone.
