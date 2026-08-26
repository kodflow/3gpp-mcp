---
description: Drive the local corpus pipeline to a reproducible, validated state — plan, run, resume, or report on it.
---

# /goal

Bring the repository to a **locally reproducible, incremental, resumable and
validated** state: the corpus can be rebuilt end to end on this machine, without
the retired remote GPU workflows, without redoing finished work, and without
rebuilding indexes whose data has not changed.

The logic lives in `cmd/goal` and `internal/goal`, not in this file. This command
only decides which subcommand to invoke and how to read the result — so the same
behaviour is available to a human, to CI, and to an agent, with no drift between
them.

## What to run

```bash
make goal-plan     # differential plan: what would run, and why. Changes nothing.
make goal          # execute that plan
make goal-status   # what is valid right now, read from persisted state alone
make goal-resume   # alias of `make goal`: every step is resumable by construction
```

Restrict the blast radius when iterating:

```bash
.local/bin/goal run --only build-go,smoke
.local/bin/goal run --from merge
.local/bin/goal invalidate embed     # forget a step; it and its dependants replay
```

## How to read a plan

Each step is `RUN` or `SKIP` with a reason. A step is skipped **only** when all
four hold: the previous run succeeded, the fingerprint is unchanged, every
declared output exists, and the cheap validation passes. A present file is not
proof; a timestamp is not proof; an old success is not proof.

`RUN` reasons name the actual cause — `implementation changed: rust/parse/src/lib.rs`,
`dependency merge is re-running`, `output missing: data/3gpp.duckdb`,
`validation failed: 17 clause(s) at/above Rel-99 still have no vector`.

## Expectations

- A second immediate run must execute **zero heavy steps**.
- A change confined to `cmd/server` must rebuild and re-smoke the server, and
  must not re-fetch, re-ingest, re-embed or rebuild the vector index.
- A new 3GPP release must fetch, convert and ingest **only the delta**, then
  embed only content hashes never seen before.

## When it fails

Read the step's log — the path is printed with the failure and recorded in
`.local/state/steps/<step>.json`. Failures keep their checkpoint, so relaunching
resumes rather than restarts. Nothing is hidden behind `|| true`: a step that is
allowed to fail declares it, visibly, in the pipeline definition.

If a run was killed, the lock left behind is reclaimed automatically once its
owning process is gone; a lock held by a **live** process is never stolen, so a
multi-hour GPU pass is safe.

## Guardrails

- Local only. Do not push, and do not publish artefacts, unless explicitly asked.
- Never mark a step successful because a subprocess exited 0 — the outputs must
  exist and validate. That confusion is what let the retired CI report success
  while its workers were failing.
- The pipeline is defined once, in `internal/goal/pipeline.go`. Do not restate it
  in a Makefile, a shell script or a document.
