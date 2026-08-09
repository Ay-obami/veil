# Deployment engine

`veil` is a small, state-driven CLI that deploys the extension: it replaces
the old `scripts/use-chain.sh` / `pre-build.sh` / `post-build.sh` / `test.sh`
orchestration (plus the `extension-setup.sh` / `extension-post-setup.sh`
hooks) with a single dependency graph of typed stages, one JSON state file,
and per-stage logs.

For the manual, first-time GCP Confidential Space walkthrough (generating a
deployer key, building and handing off the image, verifying `/info`), see
[deployment-steps.md](deployment-steps.md) — that process still has real
manual steps (an operator hands you back a proxy URL) that `veil` can't
automate away. This document covers what `veil` itself does, why it's
structured the way it is, and how to recover when something fails.

## Why this exists

The previous scripts worked, but every run meant: source `.env`, run a
script, copy a value out of its output into another `.env` file by hand,
run the next script, and if anything failed partway you re-read the script
to figure out which of its steps had already happened before re-running it
from the top. Nothing recorded *what had actually been deployed* — that
lived implicitly in whichever `.env` files happened to be lying around.

`veil` is state-driven instead: every stage's inputs and outputs are typed,
the full deployment state lives in exactly one file
(`deployment/deployment.json`), and re-running is a first-class operation
(`veil deploy resume`) rather than something you reconstruct by rereading
shell scripts.

## Architecture

```
cmd/veil/main.go       CLI: flag parsing, dispatch, output formatting
internal/deploy/
  stage.go             Stage interface + RunContext + StageError
  engine.go            Dependency graph resolution (topological sort) + run loop
  state.go             deployment/deployment.json -- the only state file
  config.go            configs/<network>.yaml -- the only static config
  logger.go            deployment/logs/<stage>.log
  exec.go              subprocess helpers (env merging, HTTP polling, git commit)
  doctor.go            `veil doctor` environment/tooling checks
  explain.go           `veil explain` stage documentation (this content, structured)
  resume.go / rollback.go
  validate.go, contracts.go, extension.go, machine.go, frontend.go, smoke.go
    one file per stage
```

Every stage implements:

```go
type Stage interface {
    Name() string
    Description() string
    DependsOn() []string
    Validate(ctx context.Context, rc *RunContext) error
    Execute(ctx context.Context, rc *RunContext) error
    Rollback(ctx context.Context, rc *RunContext) error
    Outputs() []string
}
```

No stage knows where it sits in the pipeline -- it only declares
`DependsOn()`. `Engine` resolves the actual run order from the full graph
(`internal/deploy/engine.go`'s `order()`, a standard Kahn's-algorithm
topological sort). Today the graph happens to be a straight line; it
doesn't have to stay one -- a stage could declare two dependencies and the
engine would still order it correctly.

`veil` deliberately does **not** reimplement the on-chain deployment logic
that already existed in `tools/cmd/{deploy-contract,register-extension,
allow-tee-version,set-governance,register-tee,test-setup,run-test}`. Those
programs are the actual deployment logic; `veil` is the new thing wrapped
around them -- it runs them (`go run ./cmd/<tool>` inside `tools/`),
captures their output, and turns "run six scripts in the right order and
hand-copy values between them" into "run one command."

## The pipeline

```
validate -> contracts -> extension -> machine -> frontend -> smoke
```

Run `veil explain <stage>` for what any one of these consumes, produces,
and commonly fails on. Short version:

| Stage | Replaces | Does |
|---|---|---|
| `validate` | (new) | Checks config + tooling + required files. No side effects. |
| `contracts` | `pre-build.sh`, `extension-setup.sh` | Compiles contracts, generates Go bindings, deploys `InstructionSender`, registers the extension, provisions test pairs/tokens. |
| `extension` | (the manual "now run docker compose up" step) | `docker compose up -d --build`, waits for the proxy to report healthy. |
| `machine` | `post-build.sh`, `extension-post-setup.sh` | Allows the TEE version, sets governance, registers the TEE machine, pins the extension ID, wires the TEE's signing address into `InstructionSender`. |
| `frontend` | `frontend/scripts/sync-config.ts` | Regenerates `frontend/src/config/generated.ts` from `deployment.json`. |
| `smoke` | `test.sh` | Sends a real instruction through the deployed pipeline end-to-end. |

## Configuration: `configs/<network>.yaml`

Config files hold only **static** settings -- RPC URLs, chain IDs, TEE
version, Docker Compose file, proxy URLs. They never hold anything a
deployment *produces* (that's `deployment.json`'s job) and they never hold
secrets.

Format is a deliberately small hand-written parser (see
`internal/deploy/config.go`): one `key: value` per line, `#` comments,
optional quotes, dotted keys for the one bit of nesting we need
(`frontend.port: 5173`). It's valid YAML, just a restricted subset -- there
was no reason to pull in a YAML library for eleven scalar fields, and
avoiding the dependency keeps `veil` buildable with nothing but the Go
standard library.

Three networks ship configs: `configs/local.yaml`, `configs/coston.yaml`,
`configs/coston2.yaml`. Copy one to add a new network.

### Secrets

`DEPLOYMENT_PRIVATE_KEY`, `PROXY_PRIVATE_KEY`, and `DIRECT_API_KEY` are
**never** read from a config file -- export them in your shell before
running `veil`:

```bash
export DEPLOYMENT_PRIVATE_KEY=<funded key for the target chain>
export PROXY_PRIVATE_KEY=<proxy signing key>
export DIRECT_API_KEY=<direct-endpoint api key>
```

Every subprocess `veil` runs (the `tools/cmd/*` programs, `docker compose`,
`cast`) inherits your full shell environment, so these reach them exactly
as before -- `veil` just never writes them to disk. Everything else those
programs used to read from `.env` (`CHAIN_ID`, `GOVERNANCE_SIGNERS`,
`INITIAL_OWNER`, `ADMIN_ADDRESSES`, ...) is derived from your config file
and set on the subprocess by `Config.EnvVars()`, with anything already in
your shell environment always taking priority over the config default.

## Deployment state: `deployment/deployment.json`

The single source of truth for what has and hasn't been deployed. Every
stage records its status (`pending` / `running` / `completed` / `failed` /
`rolled_back`), timestamps, and outputs here -- nothing is ever written to
a second, competing state file (register-tee's own internal resume file,
`deployment/register-tee.state`, is the one exception: it tracks progress
*inside* a single `machine` stage run, and `veil` treats it as an
implementation detail of that stage, not a second deployment record).

```bash
veil status              # human-readable summary
cat deployment/deployment.json | jq   # the raw thing
```

Writes are atomic (write to a temp file, `rename` into place), so a crash
mid-write never corrupts it.

## Resuming after a failure

```bash
veil deploy resume
```

Reads `deployment.json`, finds the first stage that isn't `completed`, and
runs it and everything after it -- completed stages are never re-run. This
is safe to call repeatedly; if the deployment is already fully complete it
says so and exits cleanly instead of doing anything.

Re-running a single stage directly (`veil deploy contracts`) always
re-executes it, even if already completed -- pass `-force` explicitly if you
mean it and want to bypass the "already completed, skipping" guard on
`veil deploy all`.

## Rollback

```bash
veil rollback
```

Rolls back completed stages in reverse order. Two of the six stages --
`contracts` and `machine` -- make on-chain transactions, which are
irreversible by nature. Rolling those back doesn't silently no-op; it
returns a clear "cannot be rolled back, here's why" and **stops** rollback
there rather than continuing past a stage whose prerequisites it just
removed:

```
X machine       cannot be rolled back: allow-tee-version, set-governance,
                register-tee, setExtensionId, and setTeeAddress are all
                on-chain transactions and cannot be undone; register a
                new TEE machine instead
```

`extension` (docker compose down) and `frontend` (delete the generated
file) roll back cleanly. `validate` and `smoke` have no effect to undo.

## Dry runs

```bash
veil deploy all -network coston2 -dry-run
```

Runs every stage's `Validate` and prints what it would do; `Execute` is
never called and `deployment.json` is never written. Use this to sanity
check a config/environment before touching anything real.

## Diagnosing problems

```bash
veil doctor             # go/docker/node/forge/cast/jq on PATH, RPC reachable,
                         # wallet funded, config valid, required files present,
                         # contract bindings generated
veil status              # what's completed, what failed, and why
veil explain <stage>     # what a stage needs, produces, and commonly fails on
```

Every stage failure is reported as:

```
FAILED
<Operation>
Reason
<what went wrong>
Suggested Fix
<how to fix it>
Run
<the next command to try>
```

not a bare Go error -- see `StageError.Report()` in `internal/deploy/stage.go`.

## Logs

```
deployment/logs/validate.log
deployment/logs/contracts.log
deployment/logs/extension.log
deployment/logs/machine.log
deployment/logs/frontend.log
deployment/logs/smoke.log
```

One file per stage, appended across runs, full subprocess stdout/stderr
included. `-verbose` / `-debug` control how much of that also prints to the
console; the log files always get everything.

## Known, deliberate gaps

- **`tee-proxy` is a genuinely separate component this repo doesn't
  vendor.** `docker-compose.yaml`'s `ext-proxy` service is
  `image: ${REGISTRY:-local}/tee-proxy` — a pre-built image, not something
  `docker compose` builds from source in this repo (unlike `extension-tee`,
  which is). Getting that image requires either a `registry:` in your
  config pointing somewhere it's published, or building it yourself from a
  separate `tee-proxy` checkout (`docker build -t local/tee-proxy <path>`).
  `veil doctor` and the `extension` stage both check for this up front and
  explain it clearly rather than letting `docker compose up` fail with a
  bare "image not found." This is a pre-existing architectural boundary,
  not something introduced by the redesign — no version of this repo has
  ever included `tee-proxy`'s source.
- **No non-Docker "local process" mode.** The old `--local` flag on
  `full-setup.sh` / `start-services.sh` ran the TEE node and proxy as
  background Go processes instead of Docker Compose, via
  `tools/cmd/start-proxy`. That program depended on
  `github.com/flare-foundation/tee-proxy` through a local-path `replace`
  directive in `tools/go.mod` pointing at a sibling checkout
  (`../../../tee-proxy`) — which meant the whole `tools` module (needed by
  every stage, on every network, not just local ones) failed to build for
  anyone who hadn't also checked out that sibling repo next to this one.
  `tools/cmd/start-proxy` has been removed and the `replace` directive
  deleted, so the repo now builds standalone; `--local` mode is gone with
  it. If you need it, run the equivalent by hand — a Go TEE + proxy
  process pair — then `veil deploy resume` to pick up from `machine`
  onward.
- **`register-tee -command rRap`.** Against real TEE hardware
  (`simulatedTee: false` in your config), `machine` automatically passes
  `-command rRap` instead of the tool's own default `rap`, because the
  lowercase form skips re-issuing the availability-check challenge once a
  TEE is registered -- any re-run against real hardware would otherwise
  fail with `Verification.ChallengeExpired`. See `docs/deployment-steps.md`
  step 8 for the full story. Simulated/local deployments don't need this.
- **`test-tokens.env`, `pairs.json`.** `contracts` still writes
  `config/<network>/test-tokens.env` and `config/<network>/pairs.json`
  itself (via the `test-setup` tool) rather than folding that data into
  `deployment.json` -- they're consumed by the Docker image build and by
  `frontend`'s pairs list, both of which expect them at those existing
  paths.
