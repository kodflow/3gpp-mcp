# DATA NOTICE — corpus & data artifacts

> This notice is **separate from `LICENSE`** on purpose. `LICENSE` governs the *code*.
> It grants **no rights whatsoever** over the *data* this project ingests, indexes, or
> redistributes. Do not infer any data permission from the code license.

## What the artifacts contain

The pipeline ingests **3GPP technical specifications** (TS/TR) and stores their
**verbatim clause text** (`clauses.text`) alongside derived data (embeddings, FTS/HNSW
indexes, catalogue metadata). The retrieval contract is "documents, not summaries":
tools such as `get_spec` return the original specification text **unmodified**.

## Ownership

3GPP specifications are © the 3GPP Organizational Partners (ARIB, ATIS, CCSA, ETSI,
TSDSI, TTA, TTC). 3GPP / ETSI retain all rights. The specifications are freely
**downloadable** from 3GPP, but free download does **not** equal a right to
**redistribute** or to operate a hosted derivative service.

## Rules for this repository

- The **3GPP corpus is NOT licensed by this repository.** No file here grants any
  right to the underlying specification text.
- **Corpus / data artifacts are internal and private** (DuckDB snapshots with full
  text, per-release DBs, embedded databases, work artifacts). They MUST NOT be
  published on any public or semi-public channel.
- **While this repository is public**, no GitHub Release asset, GitHub Actions
  artifact, Actions cache, log, or public container layer may contain `clauses.text`,
  a full-text DuckDB database, or any reconstructible export of the corpus.
- Corpus storage stays on **private channels** (private Kaggle datasets, private GHCR
  packages). Runtime images that are public are **binary-only** and never bake the DB.
- **Embeddings are not automatically "clean":** a vector set derived from copyrighted
  text may still be a derivative. Do not treat "vectors-only" as public-safe without a
  dedicated review.

## Public distribution

Any public distribution of the 3GPP text (or a reconstructible derivative) is **out of
scope** and requires a dedicated review of the applicable 3GPP / ETSI IPR terms and an
explicit decision by the repository owner. Internal users remain responsible for
complying with the 3GPP / ETSI conditions applicable to the specifications they use.
