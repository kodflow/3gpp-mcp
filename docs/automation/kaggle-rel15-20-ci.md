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
- `--checkpoint-every 2000` flushes durably mid-run, so even a hard kill loses at
  most ~2000 clauses of progress.
- The driver `scripts/kaggle-rel15-20.sh` retries `MAX_RETRIES` times on ERROR and
  relaunches on a partial; the CI job is `continue-on-error` and resumes next dispatch.

## 3. Where to put the Kaggle token in GitHub Secrets

Repo → **Settings → Secrets and variables → Actions → New repository secret**.
Direct URL: `https://github.com/kodflow/3gpp-mcp/settings/secrets/actions`

Create **two** secrets (the Kaggle CLI reads `KAGGLE_USERNAME` + `KAGGLE_KEY`):

| Secret name | Value |
|---|---|
| `KAGGLE_USERNAME` | `makingcodes` |
| `KAGGLE_KEY` | the `key` field from a fresh `kaggle.json` (kaggle.com → Account → *Create New API Token*) |

The CI workflow exports both as env so the `kaggle` CLI authenticates with zero
files on disk. NEVER commit the token or paste it into a kernel's code.

## 4. What is implemented (the artifacts)

| Concern | Artifact |
|---|---|
| Recent-first ordering | `store.ClausesNeedingEmbedding(EmbedScan{...})` (Rel-20→…→Rel-99; Rel-99 correctly oldest) |
| Bounded-session flags | `cmd/embed --series --limit --order recent\|chunk --resume --checkpoint-every --count-null-at-floor` |
| Throughput | length-bucketing (`internal/embed/apply.go`), tokenise/run pipeline + ORT graph-opt/CUDA tuning (`embed_onnx.go`) |
| Scalable writes | `store.SetEmbeddingsBatch` (one txn/window) via `embed.ApplyBatched` |
| Resumable kernel | `scripts/kaggle/kernel-embed.sh` (Dataset resume-or-slice + timeout + self-version) |
| Shard driver | `scripts/kaggle-rel15-20.sh one <series> \| all \| status` |
| CI workflow | `.github/workflows/corpus-embed-kaggle.yml` (workflow_dispatch, series matrix, publish-vec → GHCR) |
| Local proof | `make embed-smoke` → `scripts/embed-local-smoke.sh` (no Kaggle/GPU) |

## 5. How to run it

```bash
# 0. one-time: set KAGGLE_USERNAME / KAGGLE_KEY repo secrets (§3).

# 1. WHOLE-CORPUS LEXICAL build (Phase1/2 not yet — see §7; Rel-99..Rel-20 today):
#    dispatch corpus-matrix.yml with full=true, release_floor=Rel-99, publish_to_latest=true.
#    (scripts/corpus.sh already defaults SET=Rel-99; cmd/discover --all --floor Rel-99
#     covers series 21..38 — no code change, it's a dispatch input.)

# 2. EMBED campaign — from GitHub:
#    Actions → "Corpus · Embed (Kaggle GPU)" → Run workflow
#      series_list = ""           (driver default, core-network first)
#      embed_floor = Rel-15
#      publish     = true         (push to GHCR 3gpp-vec when complete)

# 2'. …or locally (operator box with ~/.kaggle/kaggle.json):
KAGGLE_USER=makingcodes scripts/kaggle-rel15-20.sh all       # every series, resumable
KAGGLE_USER=makingcodes scripts/kaggle-rel15-20.sh one 33    # just series 33

# 3. PROVE IT WORKS LOCALLY (no Kaggle, no GPU; uses real BGE-M3 on CPU if present):
make embed-smoke
```

## 6. Local testability

`make embed-smoke` exercises the exact pipeline the kernel runs (seed → embed →
validate) three ways: the Go embed-path tests, a real `cmd/embed` CLI run on a
seeded DB with `EMBEDDER=local` (asserts `null_at_floor==0` + recent-first `--limit 1`),
and — when `data/models/bge-m3` is present (`make model`) — the **real BGE-M3 over
ONNX on CPU** (`ORT_EP=cpu`, the Kaggle path minus CUDA) plus the byte-identity
tests (pipeline/batch/graph-opt). Verified green incl. the real model.

## 7. The one gated step left: fp16 (flips EmbedIdentity)

fp16 is the only remaining ~2–3× (T4 Tensor Cores) and the ONLY change that flips
`EmbedIdentity` → it forces a full re-embed, so it must be decided ONCE, **before**
any large run, and validated:

1. Build `bge-m3_fp16.onnx` offline from the pinned `BAAI/bge-m3 /onnx/model.onnx`
   via `onnxruntime.transformers.optimizer` → `convert_float_to_float16(keep_io_types=True)`,
   blocking LayerNorm/Softmax/ReduceMean to fp32. Pin the SHA-256 of *our* artifact.
2. Stamp `bgePrecision="fp16"` into `internal/embed/identity.go` + the bootstrap manifest.
3. Gate-accept only if mean `cosine(fp32, fp16) ≥ 0.9995` AND nDCG@10 parity on
   `internal/eval`. Switch the Kaggle accelerator to **T4×2** (fp16 is a wash on P100).

Until then the campaign runs fp32 (identity-safe, already fast via §1). Phase 1/2
(pre-Rel-99 GSM) is a separate parser PR (corpus.sh 4-digit gate + ReleaseOrdinal
for majors 0/1/2 + legacy `DecodeVersionCode`).

## 8. Pointers

- Plan: `.claude/plans/embed-maximization.md` (15-commit roadmap; 1,3-8,10,12-14 done).
- Hardening branch this builds on: `feat/append-resume-hardening` (PR #62).
- CI semantic channel: GHCR `3gpp-vec:latest`, serve `--vec-ghcr` / `--vec-manifest`.
