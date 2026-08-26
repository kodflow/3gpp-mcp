# ADR 0004 — The corpus is stored content-addressed, at paragraph granularity

**Status**: accepted, 2026-08-26
**Supersedes the deferral in** ADR 0003 (“vectors remain stored per clause … to
be done once the chain is green”). The chain went green on 2026-08-26.

## Context

Every clause is stored once per release, in full. 79.8 % of clause bodies are
byte-identical between releases, so most of the corpus is the same text written
down again under a different `(release, version)`.

The duplication was already exploited for **computation** — `rust/embedder`
keys a ledger on `sha(heading+text+model)` and fills a repeat by copy instead of
by GPU, a measured 2.74× — but never for **storage**.

Two requirements arrived together and look opposed:

- the database must be **as small as possible**, losing nothing;
- every clause and every paragraph must be **traceable across releases**
  (“introduced in Rel-16, still in 17 and 18, gone in 19”).

They are the same mechanism. Storing each distinct text once and recording where
it occurs *is* the delta encoding, and the occurrence table *is* the presence
matrix the lineage is computed from.

## The measurements that decided the shape

Taken on the real corpus (2 752 688 clauses, 2.87 GB of text), never estimated.

| Addressing unit | instances | distinct | distinct text |
|---|---:|---:|---:|
| clause | 2 752 688 | 806 173 | 1.52 GB |
| **paragraph** (blank-line separated) | 13 375 116 | 3 789 457 | **1.10 GB** |
| line | 71 305 558 | 10 858 833 | 0.60 GB |

**The paragraph is the optimum, and it is not the finest unit.** Lines halve the
text again but need 71 M mapping rows (~0.7 GB) to remember the order — more
than they save. Going finer than a paragraph costs more bookkeeping than it
returns.

Measured end state, same columns, both databases compacted the same way:

| | |
|---|---:|
| Today (lexical columns of `clauses`) | **4.38 GB** |
| Content-addressed (three tables below) | **2.03 GB** |
| | **2.16× lighter** |

## Decisions

### 1. The dedup key is `(heading, text)`, not `text`

897 556 distinct pairs against 806 173 distinct texts — 11 % more units for a
key that matches what the rest of the system already believes: the embedding
identity is `sha(heading + text + model)`, FTS indexes heading and text
together, and a served fragment carries its heading. Deduplicating on the text
alone would make the vectors and the bodies disagree.

We call that pair a **body**. It is the unit that is served, embedded and
indexed.

### 2. The paragraph is the dedup and traceability unit; the body is the served one

Embedding paragraphs would defeat the purpose:

| Embedded unit | vectors | size |
|---|---:|---:|
| distinct body | 897 556 | **3.6 GB** |
| distinct paragraph | 3 789 457 | 15.5 GB |
| distinct line | 10 858 833 | 44.5 GB |

Today it is 11.3 GB. Paragraph-level vectors would make the database *heavier*,
and a 213-character paragraph is a far weaker retrieval unit than a 1043-character
clause.

### 3. Empty parts are kept

`string_split(text, '\n\n')` produces empty parts wherever the source has a run
of blank lines. Dropping them is the one thing that breaks reversibility, so
they are stored like any other paragraph — the empty string is simply a
paragraph with many occurrences.

### 4. Two levels of addressing, not one

A body is not stored as a list of paragraph ids per occurrence; the *sequence*
is itself deduplicated. Since 79.8 % of clauses repeat verbatim, this drops
paragraph occurrences from 13 375 116 to **8 290 716**.

### 5. FTS moves to `paragraphs`, HNSW to `bodies`

DuckDB indexes a column of a table, so neither index can live on a view. They
move to the tables that hold the deduplicated content:

- **BM25 over `paragraphs`** — the same text indexed once instead of 3.4 times.
  A hit maps to its bodies through `body_seq`, and body scores aggregate from
  paragraph scores.
- **HNSW over `bodies.embedding`** — 897 556 vectors instead of 2 752 688.

This also fixes a retrieval defect measured the same day in `li-audit`: a
lexical top-K over the current `clauses` table returns *versions*, not
candidates. The whole 12-hit window for `CHECK_IMEI` was one clause repeated
across twelve releases while the real answer never entered it. With occurrences
factored out, a top-K is a top-K of distinct texts.

## Schema

```sql
paragraphs(para_id INTEGER PRIMARY KEY, part VARCHAR)          -- 3 789 493
bodies(body_id INTEGER PRIMARY KEY, heading VARCHAR,
       embedding FLOAT[1024], embedding_hash VARCHAR)          --   897 556
body_seq(body_id INTEGER, ord SMALLINT, para_id INTEGER)       -- 8 290 716
clause_occ(spec_id, release, version, clause_path,
           is_normative, body_id INTEGER)                      -- 2 752 688
```

Reconstruct a clause at any release: an ordered join. Trace a paragraph: a
`GROUP BY` over the occurrences carrying it. The `+/-` between two releases: a
`FULL OUTER JOIN` of two sets of `para_id`. Nothing is replayed, nothing is
reconstructed to answer a question about presence.

## Proofs, not intentions

- **The split is reversible byte-for-byte**: splitting on `\n\n` and re-joining
  reproduces the original for **2 752 688 / 2 752 688** clauses.
- **Reconstruction from the tables is exact**: **806 173 / 806 173** bodies
  rebuilt identical, 0 divergent, when the design was validated on text-only
  keys before the `(heading, text)` decision.

Both are re-runnable; the migration asserts them and refuses to publish a corpus
that fails either.

## The compatibility view does not work — measured, then abandoned

The obvious migration path was a `clauses` VIEW rebuilding the old columns,
leaving all 26 read-side call sites untouched. Both shapes were tried on the
real corpus, reading one spec-version (1 191 clauses). All of them return
identical rows and identical byte counts, so this is purely about cost:

| shape | time |
|---|---:|
| the real `clauses` table | **0.146 s** |
| view, correlated scalar subquery | 5.34 s |
| view, join against a grouped reconstruction | **97 s** |
| **two-step read** (filter, then rebuild only the retained bodies) | **0.885 s** |

Neither view survives contact. The correlated form is not decorrelated into an
indexed lookup; the join form rebuilds all 897 556 bodies before the filter is
applied. A view cannot guarantee the filter reaches `body_seq`, and that
guarantee is the entire performance story: rebuilding a *bounded* set of bodies
costs 0.035 ms each, rebuilding all of them costs a minute and a half.

So the read side is converted rather than wrapped, in two steps: filter
`clause_occ` joined to `bodies` and collect the `body_id`s, then rebuild exactly
those with `WHERE body_id IN (…)`. Six times slower than the table it replaces
for a whole spec-version — the honest cost — and not measurable for a search
returning ten hits. The alternative, keeping the text materialised to avoid it,
is the 1.53 GB this change exists to remove.

### …but the view IS what replaces the table, for everything that reads metadata

The conclusion above was drawn from queries that select the text, and it is only
true of them. Measured afterwards: **DuckDB prunes the text column when it is not
selected.** Through the view, over 2 752 688 rows:

| | |
|---|---:|
| `count(*)` | **0.62 s** |
| `count(DISTINCT release)` | **0.44 s** |

So when `clauses` is dropped a VIEW takes its name, and every caller that reads
metadata off it keeps working untouched — `cmd/validate`'s counts,
`cmd/anchorcheck`'s (spec, release, version) sweep, `cmd/split`, the sparse join.
Only the paths that genuinely need text were converted in Go, and those are the
ones that would have paid the 5.34 s.

`CREATE TABLE IF NOT EXISTS clauses` is a no-op against an existing view of that
name — verified, and pinned by a test, because a silent resurrection would put an
empty table under the corpus and make it read as empty.

**That was true and it was not enough, and the gap is instructive.** The test
asserted it against a two-column stand-in for the schema. The real `schema.sql`
also carries three `CREATE INDEX ... ON clauses`, and DuckDB answers those with

```
Binder Error: can only create an index on a base table
```

Schema application is all-or-nothing on both sides — Go's `Exec`, Rust's
`execute_batch` — so that one statement took down **every write-side tool at
bootstrap**, before it read a row: `merge`, `embed-io`, the three `enrich`
ingesters, and `freeze-hnsw`, whose entire job is to index a corpus in exactly
that state. Go's `migrate()` had a second one, `ALTER TABLE clauses ADD COLUMN
IF NOT EXISTS embedding_hash` → *"Can only modify view with ALTER VIEW
statement"*.

Nothing was silently wrong, which is the one piece of luck here: the tools
refused to open rather than corrupting anything. But a conversion whose stated
premise is "the write side needs no change" has to mean the write side can still
*open* the corpus.

So the three index statements are bracketed in `schema.sql` by
`-- @clauses-indexes-begin` / `-- @clauses-indexes-end`, and both readers of that
file strip them when the name resolves to a view. The markers live in the shared
file precisely so the two languages cannot drift; the test now applies the real
schema and asserts the raw form still fails.

Opening is not writing. `merge` and `embed` genuinely modify `clauses`, and no
guard makes an UPDATE against a view work — those call
`migrate-paragraphs --restore` first (below). `freeze-hnsw` is the one that must
work *on* the converted shape, and it is the Go `cmd/freeze-hnsw` for that
reason: `internal/store.hnswTarget` puts the index on whichever table holds the
vectors, so it builds `bodies_hnsw` over 897 556 vectors instead of failing to
build `clauses_hnsw` over 2 752 688 references to them.

## What the conversion broke afterwards, and what the four misses share

The design above held. What did not hold was everything downstream that had been
written against the old shape and was never asked whether it still applied. Four
separate places, all found by running things rather than by the test suite, and
all the same mistake in different clothes.

### The markers have to move with the vectors

Two things describe the vector population to the server, and both were left
pointing at the old shape:

- `store.LoadVSS` — the gate that decides whether the frozen index may be trusted
  — looked for `clauses_hnsw` **by name**, while `BuildAndFreezeHNSW` had already
  been taught to build `bodies_hnsw`;
- `schema_meta.embedding_count` still held 2 207 218, the pre-conversion count,
  against the 821 146 vectors the corpus actually holds.

Either one alone turns vector search off. Neither reports an error: the server
logs *"HNSW unavailable, vector search uses exact scan"*, answers every query
correctly by scanning all 821 146 vectors, and nothing downstream notices. That
is precisely the failure the freeze markers exist to catch, arriving through the
check meant to catch it.

Both were found by running the real server over the real corpus — `hnsw=false` in
its own startup line — and not by any test. So the guard now resolves its name
through `hnswTarget()`, the conversion re-stamps `embedding_count` and clears
`hnsw_state` when no index yet exists on `bodies`, and there are two tests: one
that fails with the old hardcoded name, one that fails without the re-stamp.

The rule the misses share: **whatever describes the vectors has to move with
them.** The conversion changes the vector population, so the conversion is what
must say so.

### Three gates asked three different questions

Once the serve guard was wrong, the interesting part was that nothing caught it.
There are three checks of "is the vector index usable", and they had drifted into
three different questions:

| | asked | caught the defect |
|---|---|---|
| `validate --require-hnsw` | `hnsw_state` + `HNSWIndexPresent` (name via `hnswTarget`) | no |
| `check-data` (fails a `full` image build) | `hnsw_state == "frozen"` | no |
| `store.LoadVSS` (what the server does) | flag + index present under the right name + `embedding_count` agrees | — it *is* the thing that failed |

The weakest one was the image gate, which is the last thing standing between a
corpus and production. All three now call `LoadVSS` — it never creates an index,
and its error names which condition failed, which is more than a boolean.

The general form is worth keeping: **a gate that re-implements the check instead
of running it will eventually ask a different question than the code it gates,**
and the day it does, it passes.

### The schema itself could not be applied

The first of the four, and the one that made the pipeline unable to complete a
run on the shape it produces — see the CREATE INDEX section above. It is listed
here because it is the same mistake: `schema.sql` was written for a corpus whose
`clauses` is a table, and nobody asked it whether that was still true.

### The image path had the same shape of bug, three more times

`scripts/local/build-image.sh` bakes a locally produced corpus into the two
images. It could not be run here — no Docker, no Podman, and WSL carries no
distro — so it was run against a stub `docker` that prints its argv. That found
three things a reading had not:

- **It built the wrong image.** `light` is the last stage in the Dockerfile by
  design, so that a bare `docker build .` produces the one target needing no data
  image. Without `--target full`, the script silently built the lexical-only
  image, ignored `DATA_IMAGE`, and tagged the result as the full one.
- **`${CONTRACT:+--build-arg "DATA_CONTRACT_FLAGS=$CONTRACT"}` looks quoted and
  is not** — the expansion is word-split afterwards, so docker got
  `--build-arg DATA_CONTRACT_FLAGS=--require-fts` and two loose positional
  arguments. An array fixes it. Note that a stub echoing `"$*"` cannot show this:
  the collapsed line is identical either way. It has to print argv one per line.
- **`io.kodflow.3gpp.duckdb.rows` carried `spec_versions=20163`**, the first line
  of a multi-line `dbcount` report — a catalogue size labelled as a row count, on
  the label an operator reads to find out what they pulled.

And the in-image guard, `check-data`, was the weakest of the three checks in the
table above: `hnsw_state == "frozen"` and nothing else, not even that the index
existed. It is the last gate before a corpus starts answering queries.

CI's `image-smoke` builds the **light** target on every push and passes on this
branch, so the Dockerfile — schema changes included — does build. The **full**
target, the one that inherits the ~12 GB data layer and runs the `check-data`
guard against it, has never been built. That needs a container runtime this
machine does not have, and installing one is not a thing to do unasked.

## Consequences

- The read side is converted, not wrapped: two steps at each call site that
  needs text. Several get *simpler* instead — availability and lineage never
  touch the text and read `clause_occ` directly.
- `internal/store/hnsw.go` asserts the index is `clauses_hnsw`. It moves to
  `bodies`. This is the read-side surgery ADR 0003 named and deferred.
- `chunk_id` stops being the natural key of a served fragment; `body_id` is what
  a vector belongs to, and an occurrence is `(spec_id, release, version,
  clause_path)`.
- Projected whole-corpus effect: **~30 GB → ~10 GB**, which also moves the data
  image (14 GB today).

## Where it stands, and what is deliberately not done

Executed and measured on the real corpus:

| | |
|---|---:|
| Corpus before | 30.25 GB |
| Corpus after | **12.36 GB** |
| Occurrences verified byte-for-byte | 2 752 688 / 2 752 688 |
| Vectors indexed | 821 146 (was 2 752 688 references to them) |
| HNSW build | 6 m 33 |
| BM25 over paragraphs | 46 s |
| Lexical nDCG@10 | 0.014 → **0.072** |
| `smoke` | 45 s → **4 s** |

`paragraphs` is a pipeline step between `enrich` and `index`, so a fresh clone
produces this shape rather than the old one. That position is why the Rust write
side keeps working: it goes on producing `clauses` with its text and its vectors,
and converting afterwards carries those vectors onto the bodies that own them.

An earlier draft of this ADR said the write side "needs no change at all". That
turned out to be one line short of true: `Store::open_rw` had to learn not to
apply `CREATE INDEX ... ON clauses` when that name is a view, or every write-side
tool died at bootstrap. It is not a change that teaches it ADR 0004 — it is the
narrower rule that you cannot index a view — but the stronger claim was wrong and
saying so here is cheaper than letting the next reader trust it.

## A delta run: restore the old shape, fold, convert again

The conversion is not incremental, and does not need to be — but the reason a
delta run breaks without help is worth writing down, because nothing about it is
guessable from the code.

`merge --base` starts by compact-copying the base **table by table**, from
`duckdb_tables()`. A view is not a table. So the copy leaves `clauses` behind,
`schema.sql` recreates it EMPTY in the destination, and the fold writes the
changed buckets into that empty table. The result is a corpus whose `clauses`
holds the increment while `clause_occ` still holds all 2 752 688 occurrences —
exactly the input a re-derivation would collapse the corpus onto.

Three more things break in that same state, all silently:

| | |
|---|---|
| `max_chunk_id()` | reads `clauses`, so the shard's offset is 0 and its `chunk_id`s collide with occurrences already present |
| `changed_buckets()` | compares the shard against an empty table, so every bucket looks changed and the delta stops being one |
| `stash_bucket_vectors()` | carries embeddings across a bucket replacement by reading `clauses`, and finds none |

Teaching the write side about paragraphs, bodies and occurrences fixes all four.
It also puts ADR 0004's storage layout inside the one component ADR 0001
arranged for it not to be in, and each of those four is a place where being
subtly wrong yields a corpus that passes every gate.

So the pipeline gives the write side back the shape it has always known, and
converts again afterwards. `migrate-paragraphs --restore` is the exact inverse of
`--drop-clauses`: one grouped reconstruction into a real table, then the
content-addressed tables are dropped. `merge` runs it before folding.

It is one grouped pass, not the view: the view rebuilds a body with a correlated
scalar subquery, which is right for the bounded reads it exists for and quadratic
here — 5.34 s for 1 191 clauses is over three hours for the corpus. Grouping
`body_seq` once costs **1 m 47 for all 2.87 GB**.

The check runs while the originals are still on disk, because afterwards there is
nothing left to compare against: one row per occurrence, no fanout, no NULL text.
It is not the byte-for-byte proof `verify` runs — it cannot be, since the only
reference for the text is the tables being read — and the code says so.

Measured on a real 46 440-occurrence slice (TS 23.501, 24.501, 29.273, 33.128 and
51.010-1, the last of which carries 16 509 rows with an empty `clause_path`),
running the whole loop — convert → restore → delete a bucket → fold it back with a
`chunk_id` offset → convert again:

| | |
|---|---:|
| Occurrences identical to an independent reconstruction | 46 440 / 46 440 |
| Text bytes | 43 990 280, unchanged |
| Vectors carried back | 36 538, matching the source exactly |
| After the delta round trip: rows lost / invented | **0 / 0** |
| Paragraphs, bodies, sequences after re-conversion | identical counts |

`refuseToShrink` stays. It is no longer describing the expected path — it is the
backstop for the day someone re-derives from a `clauses` that is not whole, and
it refuses rather than letting every gate agree with a corpus reduced to its last
increment.

**One limit worth stating in answers, not just in code.** The dedup unit is the
exact byte string, so a re-wrapped line reads as a change. TS 23.501 §5.4.4a
shows it: Rel-15 and Rel-16 carry the same sentence with a line break moved, and
`trace_clause` reports two paragraphs. A normalised hash alongside the exact one
would fix it and is not done.
