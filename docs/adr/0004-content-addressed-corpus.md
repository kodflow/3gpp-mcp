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
