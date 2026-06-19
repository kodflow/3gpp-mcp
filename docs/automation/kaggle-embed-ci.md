# Runbook — Embed Rel-15→Rel-20 (→ whole corpus) via Kaggle GPU, wired into CI

**Goal:** vectorise the corpus on Kaggle free GPU, recent-release-first, resumable
across the 12h cap, and publish to the GHCR `3gpp-vec` semantic channel — driven
from GitHub Actions. **Status (2026-06-01): IMPLEMENTED on `feat/embed-maximization`**
(the throughput optimisations, recent-first ordering, the resumable kernel, the
shard driver, the local smoke, and the CI workflow all exist and are tested; the
only gated step left is the fp16 model artifact — see §7).

---

## 0. Corpus size (measured from the published `latest` DB)

| Release | clauses |
|---|---|
| Rel-17 | 240 322 |
| Rel-18 | 293 251 |
| Rel-19 | 298 896 |
| Rel-20 | 26 045 |
| **Total Rel-17→20** | **858 514** |

Whole corpus (Phase1/2 / Rel-99 … Rel-20) projects to ~10M clauses.

## 1. How long will the embed take?

Measured P100 baseline: **~6 clauses/s** fp32, unoptimised. The optimisations now in
the code stack up (all but fp16 are identity-safe and on by default where safe):

| Lever | env / default | multiplier |
|---|---|---|
| Length-bucketing the work-list | `EMBED_BUCKET_WINDOW=4096` (on) | ~2–2.5× |
| Tokenise/inference 2-stage pipeline | `EMBED_PIPELINE=1` (on) | ~1.3–2× |
| ORT graph-opt + CUDA EP tuning | `EMBED_GRAPH_OPT=1` (kernel sets it) | ~1.15× |
| **fp16 on a T4 (Tensor Cores)** | gated, §7 — **flips EmbedIdentity** | ~2–3× |

Realistic optimised throughput on a **T4 with fp16 ≈ 20–30 cl/s**:

- **858k (Rel-17→20):** ~40 GPU-h → **~9–10 GPU-h** ≈ one Kaggle session + one resume.
- **~10M (whole corpus):** ~460 GPU-h → **~110 GPU-h** ≈ **~4 weeks** on one free
  account's ~30 GPU-h/week — but recent-first means Rel-20→18 of every series (the
  slice you actually query) lands in the **first 1–2 weeks**.

On a P100 (no Tensor Cores) skip fp16 (it's a wash there) — expect ~15 cl/s from the
identity-safe levers alone, ~16 GPU-h for 858k.

## 2. Retries if Kaggle kicks you out — DONE, lossless

Embed is micro-granular and **now resumable across sessions**:

- Each clause carries `embedding_hash`; a re-run embeds only NULL/changed rows
  (`cmd/embed --resume`).
- The kernel wraps the embed in `timeout ${EMBED_TIME_BUDGET:-39000}` (~10.8h) so it
  always reaches the validate+version tail before Kaggle's 12h kill. A timeout
  (`rc=124`) is a **successful increment**, not a failure.
- **Resume state persists between sessions** via a per-series Kaggle **Dataset**
  `${USER}/3gpp-embedded-s<NN>`: the kernel versions the partial DB at the end and
  mounts it at the start of the next run. recent-first ordering means a kill leaves
  the **newest** releases done.
- **The Kaggle Dataset is only a cache** — if it is deleted (quota purge, account
  hiccup), the kernel falls back to the **durable GHCR `3gpp-vec` channel we own**:
  it fetches the precision-scoped manifest (`latest` / `latest-fp16`) with the
  already-authenticated crane, pulls the relevant `s<NN>.duckdb.zst` sub-base blobs
  one at a time (disk-bounded), and carries their vectors onto the fresh slice via
  the same natural-identity match (`spec_id, release, clause_path, text`). Only
  never-published clauses re-embed; a lost Dataset never costs a full re-embed.
- `--checkpoint-every 2000` flushes durably mid-run, so even a hard kill loses at
  most ~2000 clauses of progress.
- The driver `scripts/kaggle-embed-campaign.sh` retries `MAX_RETRIES` times on ERROR and
  relaunches on a partial; the CI job is `continue-on-error` and resumes next dispatch.

## 3. Where to put the Kaggle token in GitHub Secrets

Repo → **Settings → Secrets and variables → Actions → New repository secret**.
Direct URL: `https://github.com/kodflow/3gpp-mcp/settings/secrets/actions`

The rewritten Kaggle CLI (2.x) **dropped** the old `KAGGLE_USERNAME` + `KAGGLE_KEY`
env auth. It now uses an **access token** (`KGAT_…`). Create **two** secrets:

| Secret name | Value |
|---|---|
| `KAGGLE_API_TOKEN` | the access token from kaggle.com → **Settings → API → "Generate New Token"** (`KGAT_…`). Shown once — copy it immediately. |
| `KAGGLE_USERNAME` | `makingcodes` — used **only** as the handle that namespaces the kernel/dataset slugs (`KAGGLE_USER`), never for auth. |

The CI workflow exports `KAGGLE_API_TOKEN` as env; the `kaggle` CLI reads it
directly (or `~/.kaggle/access_token`) and introspects it server-side — zero files
on disk, zero creds in the kernel. NEVER commit the token or paste it into a kernel.
To rotate: generate a new token, update the secret; the old one can be revoked under
Settings → API.

## 4. What is implemented (the artifacts)

| Concern | Artifact |
|---|---|
| Recent-first ordering | `store.ClausesNeedingEmbedding(EmbedScan{...})` (Rel-20→…→Rel-99; Rel-99 correctly oldest) |
| Bounded-session flags | `cmd/embed --series --limit --order recent\|chunk --resume --checkpoint-every --count-null-at-floor` |
| Throughput | length-bucketing (`internal/embed/apply.go`), tokenise/run pipeline + ORT graph-opt/CUDA tuning (`embed_onnx.go`) |
| Scalable writes | `store.SetEmbeddingsBatch` (one txn/window) via `embed.ApplyBatched` |
| Resumable kernel | `scripts/kaggle/kernel-embed.sh` (Dataset resume-or-slice + timeout + self-version) |
| Shard driver | `scripts/kaggle-embed-campaign.sh one <series> \| all \| status` |
| CI workflow | `.github/workflows/corpus-embed-kaggle.yml` (workflow_dispatch, series matrix, publish-vec → GHCR) |
| Local proof | `make embed-smoke` → `scripts/embed-local-smoke.sh` (no Kaggle/GPU) |

## 5. How to run it

**Two decoupled CI jobs (the architecture you confirmed):**
- **LEXICAL build** = `corpus-matrix.yml`. Runs AUTOMATICALLY on every merge to `main`
  (`push`) — a delta/append build that publishes `latest`, includes Phase 1/2 (GSM),
  and does NO embedding. `concurrency: cancel-in-progress` so back-to-back merges never
  stack two builds (the newest supersedes). Also manual via dispatch.
- **EMBED campaign** = `corpus-embed-kaggle.yml`. MANUAL only (Kaggle quota). Consumes
  the published `latest`, embeds on Kaggle GPU, publishes vectors to GHCR `3gpp-vec`.

```bash
# 0. one-time: set KAGGLE_API_TOKEN (KGAT_… access token) + KAGGLE_USERNAME
#    (=makingcodes, slug handle) repo secrets (§3).

# 1. FIRST full lexical build (one-time, to seed `latest` with the whole corpus
#    Phase 1 → Rel-20): Actions → "Corpus · Build" → Run workflow:
#      full=true, release_floor=Rel-99, include_legacy_gsm=true, publish_to_latest=true, embed=false
#    Thereafter EVERY merge to main auto-runs a DELTA build (only changed series).

# 2. EMBED campaign (Rel-17 → Rel-20 for this first run):
#    Actions → "Corpus · Embed (Kaggle GPU)" → Run workflow:
#      series_list = "33"   (one series first — shakedown), then "" (all 16)
#      embed_floor = Rel-17
#      publish     = true
#    A later MR lowers embed_floor toward Rel-99 once this works.

# 2'. …or locally (operator box; auth via KAGGLE_API_TOKEN or ~/.kaggle/access_token):
KAGGLE_API_TOKEN=KGAT_xxx KAGGLE_USER=makingcodes EMBED_FLOOR=Rel-17 \
  scripts/kaggle-embed-campaign.sh one 33

# 3. PROVE IT WORKS LOCALLY (no Kaggle, no GPU; real BGE-M3 on CPU if present):
make embed-smoke
```

## 6. Local testability

`make embed-smoke` exercises the exact pipeline the kernel runs (seed → embed →
validate) three ways: the Go embed-path tests, a real `cmd/embed` CLI run on a
seeded DB with `EMBEDDER=local` (asserts `null_at_floor==0` + recent-first `--limit 1`),
and — when `data/models/bge-m3` is present (`make model`) — the **real BGE-M3 over
ONNX on CPU** (`ORT_EP=cpu`, the Kaggle path minus CUDA) plus the byte-identity
tests (pipeline/batch/graph-opt). Verified green incl. the real model.

## 7. Model registry + fp16 (implemented; one operator step + a §0 decision)

Models are now declarative — `internal/embed/models.yaml` (embedded default; override
at `EMBED_MODELS_CONFIG` or `data/models/models.yaml`) declares each model's dir,
precision, dim, normalization, revision and ONNX I/O names. Swap model/precision with
`EMBED_MODEL=<name>` (e.g. `bge-m3-fp16`). Precision is folded into the EmbedIdentity,
so fp16 and fp32 vectors can never share one HNSW; selecting an absent model DISABLES
rather than running the wrong one under its identity. Deployment paths stay in env
(`EMBED_MODELS_BASE` for relative dirs, `EMBED_MODEL_DIR` absolute override).

**fp16 (the only remaining ~2–3× on T4 Tensor Cores; flips EmbedIdentity → forces a
full re-embed, so decide ONCE before any large run):**
1. Provide a validated fp16 dense export at the registry's `bge-m3-fp16` dir:
   `FP16_ONNX_URL=… FP16_DATA_URL=… WITH_FP16=1 make model`. (Converting fp32→fp16
   needs an offline toolchain outside this repo; supply a trusted/own export.)
2. Gate it: `EMBED_MODELS_BASE=$PWD go test -tags onnx ./internal/embed -run TestFP16GateVsFP32`
   (requires mean `cos(fp32,fp16) ≥ 0.9995`). Add an nDCG@10 parity check on `internal/eval`.
3. Run fp16: `EMBED_MODEL=bge-m3-fp16` on a **T4** (fp16 is a wash on P100/Pascal).

Until an fp16 artifact is provided + gated, the campaign runs fp32 (identity-safe,
already fast via §1's identity-safe levers — all on by default).

**Phase 1/2 (pre-Rel-99 GSM) — IMPLEMENTED (lexical-only, §0-approved).** The whole
MODERN corpus (Rel-99→Rel-20) is the default lexical build. Legacy GSM Phase 1/2 is
now indexable LEXICALLY under the single honest `release="GSM"` bucket (the ingest
stamps it for 4-digit specs; `spec_id`, `clause`, the literal code-version and `url`
stay exact). Because `ReleaseOrdinal("GSM")` is false, GSM clauses are below EVERY
embed floor → searchable via BM25/LIKE but **never vectorised** (vectors stay
Rel-99→latest, exactly as you asked). Enable it:
- locally: `INCLUDE_LEGACY_GSM=1 scripts/corpus.sh --series "04 05 08"` (legacy series);
- in CI: the single ingest job (`corpus-matrix.yml`) input `include_legacy_gsm=true`
  paired with the legacy series in `series_scope`.

## 8. Pointers

- Plan: `.claude/plans/embed-maximization.md` (15-commit roadmap; 1,3-8,10,12-14 done).
- Hardening branch this builds on: `feat/append-resume-hardening` (PR #62).
- CI semantic channel: GHCR `3gpp-vec:latest`, serve `--vec-ghcr` / `--vec-manifest`.

## 9. Two Kaggle accounts (automatic fallback + one-click switch)

The Kaggle GPU quota is per-account and weekly. To keep the campaign moving when one
account is rate-limited or its token expires, the CI carries **two** accounts. Each of
the three Kaggle workflows loops over them: it auth-probes an account, runs the kernel,
and **falls back to the other account** if the first either fails auth or gets **no GPU**
(weekly quota exhausted). The run stops **only if NO account is granted a GPU**.

**Secrets / variables (Settings → Secrets and variables → Actions):**

| Account  | Token (secret)               | Username (variable) |
|----------|------------------------------|---------------------|
| primary  | `KAGGLE_API_TOKEN`           | `KAGGLE_USERNAME`           (e.g. `makingcodes`) |
| fallback | `KAGGLE_API_TOKEN_FALLBACK`  | `KAGGLE_USERNAME_FALLBACK`  (e.g. `extazy937`)   |

Tokens are KGAT access tokens (Kaggle → Settings → API → *Generate New Token*).
Usernames are not sensitive → store them as repo **variables**.

**How it picks:** the `kaggle_account` dispatch input chooses which account is tried
**first** (the other is the automatic fallback):
- `primary` (default) → try `[primary, fallback]`
- `fallback` → try `[fallback, primary]`

So switching accounts is a one-field change in the *Run workflow* dialog of any Kaggle
workflow (`Corpus · Rust embed`, `Corpus · Sparse`, `Corpus · Embed`).

**Two fallback triggers (all three workflows: Rust embed / Sparse / Embed):**
1. **Auth failure** (expired/invalid token) — the per-account loop auth-probes
   (`kaggle kernels list -m`) and skips an account that fails to authenticate.
2. **GPU quota** (no GPU allocated) — the loop *runs* the kernel on an account and, if it
   got **no GPU** (weekly quota exhausted → the kernel logs `gpu=absent`, or the session
   errors with no output), it **automatically re-runs on the other account**.
   `scripts/kaggle-gpu-check.sh` reads the pulled output's `gpu=present`/`gpu=absent`
   marker (emitted by every kernel) to classify the run as `gpu` vs `quota`.

In practice quota exhaustion makes the Kaggle session error/404 quickly (no committed
output), so the fallback fires fast rather than after a full CPU run.

> Cross-account note: each account has its **own** per-shard resume Dataset
> (`<user>/3gpp-rust-embedded-<shard>`, `<user>/3gpp-embedded-s<NN>`). Falling back to
> account B continues from B's own state (or a fresh slice) — re-embedding is idempotent
> (keyed by `embedding_hash`), so no corruption, but B may redo work A had done. Progress
> is never lost.

**Add a third account?** Add the matching secret/variable + the workflow `env`, and
extend each workflow's per-account loop (`PRIM_*`/`FB_*` capture + the `case`/`ORDER`).
The GPU/quota verdict is unit-tested offline by `scripts/kaggle-gpu-check_test.sh`
(7 fixtures, no network).

**Note (in-kernel versioning):** the CI selects the account that *pushes/mounts* the
kernel. The kernel's own dataset-versioning tail (`KAGGLE_USERNAME`/`KAGGLE_KEY` read
on Kaggle) uses the **Kaggle Secrets attached to the kernel** for that account — keep
those in sync per account on the Kaggle side.
