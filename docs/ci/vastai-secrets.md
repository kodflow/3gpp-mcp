# vast.ai provider — required secrets & setup

The `vastai` GPU provider (see `.claude/plans/kaggle-vastai-provider-switch.md`) rents a
short-lived RTX 4090 to run the same Rust embed campaign as the Kaggle path. It needs the
following GitHub repo secrets/vars. **Kaggle stays the default; nothing below changes the
Kaggle path.**

## Secrets (Settings → Secrets and variables → Actions → Secrets)

| Name | Scope | Used by | Notes |
|------|-------|---------|-------|
| `VAST_API_KEY` | full vast.ai account | the CI **runner** only | Rents/destroys instances + bills. **NEVER passed into the rented box** (`--env`) — the host is root and would read it. Teardown is driver-side only. Rotate after a rental campaign. |
| `GHCR_READ_PAT` | `read:packages` | the **box** (to pull `3gpp-corpus`) | Read-only, short-lived. A malicious host can read it but cannot write to GHCR. Already used by the Kaggle path. |
| `GHCR_PAT` | `write:packages` | the **runner's** `publish-vec` step ONLY | The box never gets a write token; it produces a shard, the runner pulls it back (`vastai copy`) and pushes. |

## Variable (optional)

| Name | Values | Effect |
|------|--------|--------|
| `GPU_PROVIDER` | `kaggle` (default) \| `vastai` | The orchestrators read this to choose where to dispatch. A per-run `workflow_dispatch` input overrides it. |

## Cost guards (baked into `scripts/ci/vastai.sh`, overridable via env)

- `CEIL_DPH` (default `0.55`) — hard `$/hr` ceiling; no offer above it is rented.
- `MAX_HOURS` (default `9`) — wall-clock watchdog; the box is destroyed unconditionally after this.
- On-demand only (no interruptible/bid in blast mode).
- `vastai-reconcile.yml` (cron, every 30 min) destroys any orphaned `embed-*`-labelled instance — the backstop if the runner dies before its teardown trap fires.

## Rotation

1. Create the vast.ai account + an API key, add `VAST_API_KEY`.
2. After a campaign, **rotate `VAST_API_KEY`** (vast.ai account → API keys → regenerate).
3. The read PAT is short-lived; the write PAT never leaves the runner.
