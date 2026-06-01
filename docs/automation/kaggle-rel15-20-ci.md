# Runbook — Embed Rel-15→Rel-20 via Kaggle GPU, then wire into CI

**Goal (tomorrow):** vectorise the WHOLE Rel-15→Rel-20 slice of the corpus on Kaggle
free GPU, publish the semantic DB, and make it a repeatable CI step (PR-11b).

**Status:** the POC is PROVEN. On 2026-06-01 a real run embedded series-21 Rel-15→20
(1516 clauses, `null_at_floor=0`, 1024-dim, HNSW frozen) on a Tesla P100 via
`ORT_EP=cuda`. The kernel is `scripts/kaggle/kernel-embed.sh`, driver
`scripts/kaggle-embed-poc.sh`. Account `makingcodes` is phone-verified (GPU+Internet on).

---

## 0. Corpus size (measured from the published `latest` DB)

| Release | clauses |
|---|---|
| Rel-17 | 240 322 |
| Rel-18 | 293 251 |
| Rel-19 | 298 896 |
| Rel-20 | 26 045 |
| **Total Rel-15→20** | **858 514** |

(No Rel-15/16 clauses exist in the current corpus — the floor just captures ≥15.)

## 1. How long will the full embed take?

Measured P100 run: 1516 clauses in 507 s **wall** — but that 507 s INCLUDED ~250 s of
fixed setup (Go build + 2.3 GB model download + ORT + DB download). Pure embed was
~257 s → **~6 clauses/s** on P100, unoptimised (mean-pool windowing ON).

- **Naïve, as-is:** 858 514 / 6 ≈ **~40 h of GPU compute.**
- Kaggle limits: **12 h max per session**, **~30 h/week** GPU quota.
- ⇒ As-is, the full slice does NOT fit one session and barely fits a week. **Must shard.**

**Two levers to bring it down:**
1. **Shard by series** (e.g. 28 series → 28 kernels), each its own ≤12 h session. Most
   series are small; the heavy ones (23/24/29 core-network) dominate.
2. **Optimise the embed** (separate work, ~3–5×): disable `EMBED_WINDOWING=mean_pool`
   (truncate@512), bigger `BGE_BATCH`, fp16. At ~20 cl/s the whole slice ≈ **~12 h**.

**Realistic plan for tomorrow:** shard by series, run a handful of kernels in parallel
(Kaggle allows a few concurrent), spread the heavy ones across 2–3 days if the weekly
quota bites. Expect **~40 h of GPU** unoptimised, or **~12 h** if we turn off windowing
first. Recommendation: do the windowing/batch optimisation FIRST (1 commit), then shard.

## 2. Retries if Kaggle kicks you out

Kaggle kills a kernel at the 12 h cap or on a transient worker error. The design already
survives this, because **embed is micro-granular and idempotent**:

- The kernel embeds into the DB and `embedding_hash` marks each done clause.
- A re-run **skips already-embedded clauses** (`ClausesNeedingEmbedding` only returns
  NULL/old-hash rows) — so re-launching the SAME kernel resumes where it stopped, as long
  as the partial DB is persisted.
- **Caveat for the POC kernel:** it currently rebuilds the lexical slice from the public
  release each run (fresh DB → no resume). To get resume, the kernel must **reload its own
  previous output** as the starting DB. Two options (pick in step 4):
  - (a) push the partial embedded DB to a Kaggle **Dataset** at the end of each run and
    re-mount it next run (kernel output → versioned dataset → input); or
  - (b) once in CI, the GHCR per-series sub-base IS the resume baseline (the CI path
    already does this with `embedding_hash`).
- **Driver-level retry:** `scripts/kaggle-rel15-20.sh` (created below) re-pushes a series
  kernel up to N times on ERROR, and skips series whose sub-base is already complete
  (`null_at_floor==0`). So "thrown out" = automatic relaunch, no lost work.

## 3. Where to put the Kaggle token in GitHub Secrets

Repo → **Settings → Secrets and variables → Actions → New repository secret**.
Direct URL: `https://github.com/kodflow/3gpp-mcp/settings/secrets/actions`

Create **two** secrets (the Kaggle CLI reads `KAGGLE_USERNAME` + `KAGGLE_KEY`):

| Secret name | Value |
|---|---|
| `KAGGLE_USERNAME` | `makingcodes` |
| `KAGGLE_KEY` | the API token (the `KGAT_…` key, or the `key` field from a fresh `kaggle.json`) |

> Prefer a **fresh** key from kaggle.com → Account → "Create New API Token" (downloads
> `kaggle.json` with `username`+`key`). Put `key` into `KAGGLE_KEY`. The CI step exports
> both as env so the `kaggle` CLI authenticates with zero files on disk.
> NEVER commit the token or paste it in code/logs.

(Local dev keeps using `~/.kaggle/kaggle.json` or `~/.kaggle/access_token` — already set.)

## 4. Tomorrow's steps (in order)

1. **(optional but high-value) Optimise embed** — 1 commit: env knob to disable mean-pool
   windowing for the GPU path + raise `BGE_BATCH`; re-run series-21 POC to measure the new
   cl/s. Decide canonical precision (fp32 vs fp16) and fold it into `EmbedIdentity` (PR-6).
2. **Add resume to the kernel** — option (a): at end, `kaggle datasets version` the embedded
   DB; at start, if the dataset exists, start FROM it instead of the fresh slice. This is
   what makes "thrown out → relaunch" lossless.
3. **Shard driver** — `scripts/kaggle-rel15-20.sh` loops the series list, pushes one kernel
   per series (`SERIES=NN`), polls, pulls, retries on ERROR, skips already-complete series.
4. **Run it** — launch the small/medium series first (fast wins), then the heavy ones; let
   the driver retry across the 12 h cap / weekly quota.
5. **Publish + CI (PR-11b)** — push each series' embedded sub-base to **GHCR `3gpp-vec`**
   (the semantic channel the serve path already reads via `--vec-ghcr`). Add a manual
   `workflow_dispatch` CI job that runs the same kernel driver using the
   `KAGGLE_USERNAME`/`KAGGLE_KEY` secrets, so the embed can be triggered from GitHub.

## 5. Quick commands

```bash
# local prereqs (already done in this env; re-run if fresh box):
python3 -m pip install --user --break-system-packages kaggle
mkdir -p ~/.kaggle && printf '%s' "$KAGGLE_KEY" > ~/.kaggle/access_token && chmod 600 ~/.kaggle/access_token

# one series, end to end (push → poll → pull → validate):
SERIES=33 scripts/kaggle-rel15-20.sh one         # (driver created tomorrow)

# whole Rel-15→20, sharded with retries:
scripts/kaggle-rel15-20.sh all

# validate a pulled sub-base:
duckdb out/3gpp-embedded.duckdb -box "SELECT count(*) total, \
  count(*) FILTER (WHERE embedding IS NOT NULL) vec FROM clauses;"
```

## 6. Pointers

- Kernel body: `scripts/kaggle/kernel-embed.sh` (zero-upload: clones public repo, pulls
  `latest` release DB, slices `SERIES` Rel-15→20, fetches BGE-M3 + ORT-GPU, embeds on CUDA).
- Driver: `scripts/kaggle-embed-poc.sh` (single-series POC) → generalise to `kaggle-rel15-20.sh`.
- Embed identity / floor logic: `internal/embed`, `cmd/embed`, `model.PipelineVersion`.
- CI semantic channel: GHCR `3gpp-vec:latest`, serve `--vec-ghcr` / `--vec-manifest`.
- Hardening branch with all the correctness fixes: `feat/append-resume-hardening` (PR #62).
