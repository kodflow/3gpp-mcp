# 3GPP MCP

> **Verdict architectural figé. Toute déviation doit être justifiée par écrit dans une issue/MR et obtenir un commit explicite avant implémentation.**

## 0. Mission

Construire un serveur **MCP (Model Context Protocol)** qui expose **l'intégralité du corpus 3GPP** — de **Phase 1 (1992)** à la **dernière Release publiée** — sous forme interrogeable instantanément par Claude Code (et, à terme, tout client MCP).

Le serveur ne reformule pas, ne résume pas, ne reformule pas : **il retourne des fragments de spécification avec citation exacte** (spec_id, release, version, clause, URL). Claude raisonne, l'index sert.

Public visé : ingénieurs télécom (cœur EPC/5GC, RAN, LI, IMS) qui ont besoin de l'info juste, datée, sourcée — pas d'une approximation plausible.

## 1. Philosophie (non négociable)

| Principe | Conséquence |
|---|---|
| **Pas d'hallucination tolérée** | Chaque réponse cite `{spec_id, release, version, clause}` et expose l'URL DOCX 3GPP. Si on ne peut pas citer, on ne répond pas. |
| **Local-first** | Tout tourne sur le poste du dev en V1. Aucun appel cloud au moment du query. Internet uniquement pour l'ingestion (scrape FTP 3GPP). |
| **Mono-binaire** | `mcp-3gpp` compile en un binaire statique distribuable par `scp`. Pas de venv, pas de runtime à installer côté utilisateur. |
| **AI-assumed workflow** | Le projet est développé _avec_ Claude Code. Skills, agents et hooks de `kodflow/devcontainer-template` sont l'environnement par défaut. |
| **MCP retourne des documents, jamais des résumés** | Toute synthèse est faite par Claude côté client. Le serveur est un moteur de retrieval, pas un assistant. |
| **Reproductibilité d'ingestion** | Un script ingère un état du corpus à une date donnée et produit une DB déterministe (hash stable). |

## 2. Stack technique — **figée**

> **`arch-change` 2026-06-19 — write-side Rust / read-side Go.** La frontière
> d'écriture DuckDB a migré vers Rust (plan `rust-writeside-go-readside.md`). **Rust
> écrit TOUT** le `.duckdb` (parse, ingest, embed dense+sparse, FTS, HNSW, merge,
> discover-parité) ; **Go ouvre read-only** et sert (MCP, search, rerank). Une
> implémentation d'inférence unique (`rust/embed-core`, cdylib BGE-M3 dense+sparse) :
> l'embedder bulk l'utilise comme crate, et Go-serve l'embedding de requête via FFI
> (`-tags embed_ffi`, `ort` en **load-dynamic** partageant l'unique onnxruntime avec
> le reranker). Le Go ONNX embed backend a été **supprimé** (l'image livrée build
> `onnx,embed_ffi`). `internal/onnxrt` **reste** (le reranker l'utilise). §2.1
> ci-dessous décrit le « pourquoi Go » HISTORIQUE ; il vaut toujours pour le read-side.

```
┌─────────────────────────────────────────────────────────────┐
│  mcp-3gpp  (binaire Go+CGO read-side ; producteurs = bins Rust) │
└─────────────────────────────────────────────────────────────┘

Langage       : Go 1.23+               (productivité solo dev, mono-binaire)
CGO           : autorisé               (DuckDB, KuzuDB, ONNX Runtime)
MCP SDK       : github.com/mark3labs/mcp-go
Stockage      : DuckDB (CGO)           (FTS + VSS HNSW + SQL OLAP)
Graph (V2)    : KuzuDB embedded (CGO)  (relations NE↔NF, evolutions)
Embeddings    : ONNX Runtime (CGO) + BGE-M3 (1024 dim, 8k ctx, dense+sparse)
Reranker      : BGE-reranker-v2-m3 ONNX (optionnel, V2)
Doc parsing   : LibreOffice --convert-to html → rust/parse (cf. §13)
Scraping      : scripts/corpus.sh + rust/discover (3GPP FTP, archives .zip)
```

### 2.1 Pourquoi **Go + CGO** et rien d'autre

- Python est éliminé : pas de venv, pas de pip, pas de gestion de Python systèmes.
- Rust est viable mais ralentit la vélocité solo. Go atteint 80 % des perfs Rust avec 30 % de l'effort.
- CGO est accepté : ses inconvénients (cross-compile, build plus lent) sont marginaux face au gain qualitatif (HNSW vrai, FTS BM25, ONNX natif).

### 2.2 Pourquoi **DuckDB** plutôt que SQLite

| Critère | SQLite + sqlite-vec | **DuckDB** |
|---|---|---|
| Vecteurs > 500k chunks | brute force, dégradation rapide (>80 ms) | **HNSW vrai (~3 ms)** |
| Queries analytiques (diff release, agrégats) | 8-15 s | **80-200 ms** |
| Format d'export | seulement `.db` | **Parquet/Arrow natif** |
| FTS (BM25) | FTS5 (excellent) | FTS extension (suffisant) |
| Concurrence écriture | 1 writer | 1 writer (read-mostly OK) |

Sur le corpus complet (~10 M chunks projetés), SQLite + sqlite-vec ne tient pas. DuckDB reste plat. **Pas de débat.**

### 2.3 Pourquoi **KuzuDB** plutôt que Neo4j (pour la couche graphe V2)

- Embedded, pas de daemon
- Columnar, même philosophie que DuckDB
- ~50 MB de footprint, vs JVM Neo4j
- Cypher supporté
- Conçu pour OLAP-graphs (transitive closure ultra-rapide)

### 2.4 Pourquoi **pas d'Ollama** dans le chemin de query

Ollama (modèles 7B-32B locaux) **est interdit au moment de la requête**. Raisons :

1. Claude est déjà le moteur de raisonnement client. Ajouter un second LLM = double latence + perte d'information.
2. Les 7B-32B hallucinent sur la terminologie 3GPP (confondent releases, inventent des IE, mélangent N1/N2/N4).
3. Le rôle du MCP est de **retrieval**, pas de synthèse.

Ollama / vLLM **peuvent** servir **uniquement en batch offline** pour :
- l'extraction d'entités depuis les annexes architecturales (drafts de `Evolution(from, to, type)`),
- la génération de brouillons de glossaire validés par l'humain,
- jamais en production query path.

## 3. Architecture du retrieval (router-based)

```
            Requête Claude (via MCP)
                       │
                       ▼
            ┌────────────────────────┐
            │  Intent router (regex) │
            └────────────────────────┘
              │       │       │       │
              ▼       ▼       ▼       ▼
          ┌──────┐ ┌──────┐ ┌──────┐ ┌──────────┐
          │ BM25 │ │ VEC  │ │GRAPH │ │CHANGELOG │
          │ FTS  │ │HNSW  │ │KuzuDB│ │ SQL diff │
          └──────┘ └──────┘ └──────┘ └──────────┘
              │       │       │       │
              ▼       ▼       ▼       ▼
              └───────┴───────┴───────┘
                       │
                       ▼
            Hybrid fusion (RRF k=60)
            + Reranker (BGE-reranker-v2-m3) optionnel V2
                       │
                       ▼
              JSON: [{spec_id, release, version,
                      clause, heading, text, url,
                      score}] avec citations
```

**Règles de routing** (intentionnellement simples, regex-based) :

| Pattern détecté | Backend |
|---|---|
| `TS \d\d\.\d\d\d` | BM25 direct (filtrage par spec) |
| `(remplace|équivalent|évolution|migration|maps to)` | Graph (V2) |
| `(diff|change|évolution|différence) entre Rel-\d+ et Rel-\d+` | SQL `changes` |
| `(défini|définition|expansion)` + acronyme tout-en-majuscules | Glossary lookup |
| sinon | Hybride BM25 + Vector + RRF |

## 4. Schéma de données (DuckDB)

> **La source de vérité est `internal/store/schema.sql`**, pas ce fichier. Ce qui
> suit est la forme des tables de base, pour lire le code sans ouvrir la DB. Le
> corpus en porte **16** au total ; les dix autres sont listées en fin de section.

```sql
-- Catalogue de specs
CREATE TABLE specs (
    spec_id       VARCHAR PRIMARY KEY,   -- '23.501'
    series        VARCHAR,                -- '23'
    title         VARCHAR,
    doc_type      VARCHAR,                -- 'TS' | 'TR'
    working_group VARCHAR                 -- 'SA2'
);

-- Versions par release
CREATE TABLE spec_versions (
    spec_id         VARCHAR,
    release         VARCHAR,              -- 'Rel-18'
    version         VARCHAR,              -- '18.5.0'
    freeze_date     DATE,
    docx_url        VARCHAR,
    status          VARCHAR,
    metadata_source VARCHAR,              -- d'où viennent freeze_date/status
    PRIMARY KEY(spec_id, release, version)
);

-- Chunks au niveau clause (jamais token-window arbitraire)
CREATE TABLE clauses (
    chunk_id       UBIGINT PRIMARY KEY,
    spec_id        VARCHAR,
    release        VARCHAR,
    version        VARCHAR,
    clause_path    VARCHAR,               -- '5.2.3.1'
    heading        VARCHAR,
    text           VARCHAR,
    is_normative   BOOLEAN,
    embedding      FLOAT[1024],           -- HNSW index dessus
    embedding_hash VARCHAR                -- sha(heading+text+model) : un texte
);                                        -- identique garde son vecteur au merge

PRAGMA create_fts_index('clauses', 'chunk_id', 'heading', 'text');
INSTALL vss; LOAD vss;
CREATE INDEX clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric='cosine');

-- CR (Change Request) — granularité atomique des évolutions
CREATE TABLE changes (
    cr_number    VARCHAR,
    cr_revision  INTEGER,
    spec_id      VARCHAR,
    from_version VARCHAR,
    to_version   VARCHAR,
    meeting      VARCHAR,                 -- 'SA2#150'
    category     VARCHAR,                 -- 'A'|'B'|'C'|'D'|'F'
    clauses      VARCHAR[],
    summary      VARCHAR,
    tdoc_url     VARCHAR
);

-- Glossaire (seed depuis TS 21.905)
CREATE TABLE acronyms (
    term          VARCHAR,
    expansion     VARCHAR,
    domain        VARCHAR,                -- '5GC'|'EPC'|'IMS'|'RAN'
    first_release VARCHAR,
    last_release  VARCHAR,
    source_series VARCHAR,
    PRIMARY KEY(term, domain)
);

-- Evolutions inter-acronymes (MME → AMF+SMF, eNB → gNB, ...)
CREATE TABLE evolutions (
    from_term            VARCHAR,
    to_term              VARCHAR,
    evolution_type       VARCHAR,         -- 'SPLIT'|'MERGE'|'RENAME'|'REPLACED_BY'|'EXTENDED_BY'
    justification_spec   VARCHAR,
    justification_clause VARCHAR,
    confidence           FLOAT
);
```

**Les dix autres tables**, posées par `enrich` (overlays) ou par les sujets :

| Table | Posée par | Contenu |
|---|---|---|
| `releases` | seed | `(code, name, status, start_date, freeze_date, freeze_meeting)` — l'ordre des releases n'est pas déductible du semver |
| `api_operations`, `api_schemas` | `ingest-openapi` | opérations et schémas des YAML OpenAPI 5GC, épinglés au SHA Forge (`search_api`) |
| `li_events`, `li_event_fields`, `li_nf_clauses`, `asn1_types` | `ingest-li` | registre ASN.1 de TS 33.128 (`li_events`) |
| `clause_sparse` | `embed --sparse-only` | bras sparse — **produit mais consommé par personne**, cf. §13 |
| `ingest_log` | `ingest`/`merge` | ce qui a été réellement ingéré, pour que `--resume` interroge le corpus et pas seulement le shard |
| `schema_meta` | toutes | `(key, value)` : identité d'embedding, état HNSW, version de schéma — c'est ce que le serveur lit pour refuser de démarrer sur un désaccord |

## 5. Surface MCP — **10 tools au cœur, + 1 par sujet**

Le cœur est figé dans `internal/mcp/server.go`. Un **sujet** (vertical métier,
`internal/subject/`) peut en ajouter via `internal/registry` — c'est le seul
chemin autorisé pour étendre la surface. Aujourd'hui : 10 + `li_events` = **11**.

| Tool | Signature | Rôle |
|---|---|---|
| `search_spec` | `(query, release?, series?, spec_type?, top_k=10, mode='hybrid' \| 'lexical' \| 'semantic')` | Retrieval hybride avec citations |
| `get_spec` | `(spec_id, release, version?, clause?)` | Fetch d'une spec ou d'une clause précise |
| `get_changelog` | `(spec_id, from_release, to_release, clause?)` | Liste des CRs et leur impact |
| `list_releases` | `(spec_id)` | Toutes les `(release, version, freeze_date)` |
| `resolve_term` | `(term, release?)` | Définition + domaine + références |
| `trace_evolution` | `(entity, from_release, to_release)` | Sous-graphe d'évolution |
| `find_cross_references` | `(spec_id, clause?)` | Specs/clauses référencées |
| `list_specs` | `(release?, series?, working_group?)` | Catalogue filtré |
| `search_api` | `(query, release?, service?, service_family?, spec_id?, method?, kind?, top_k=10)` | Opérations et schémas OpenAPI 5GC (TS 29.5xx), citations épinglées au SHA |
| `server_info` | `()` | Capacités de retrieval, et **pourquoi** le sémantique est on/off |
| `li_events` *(sujet `li`)* | `(nf?, release?, interface?)` | Couverture Lawful Interception TS 33.128 : inventaire X1/X2/X3, ou événements d'un NE/NF |

**Toute réponse contient un bloc `citations: [{spec_id, release, version, clause, url}]`.**

## 6. Pipeline d'ingestion

Depuis le 2026-08-23, tout passe par **une machine** et une seule commande :
`cmd/goal` + `internal/goal`, une machine à états de **20 étapes** reprenable.
Runbook complet : `docs/local-pipeline.md`. Décision : `docs/adr/0003`.

```
toolchain ─┬─ build-go ─┬─ test
           │            └──────────────────────┐
           ├─ build-rust ─────────┐            │
           └─ build-embedder ──┐  │            │
                               │  │            │
             seed ── discover ─┼──┴── fetch ── ingest ── merge ─┬─ embed ─┐
                                                                └─ enrich ┴─ paragraphs ─┬─ index
                                                                                         │
                                                                          validate ── smoke
```

| Étape | Fait quoi | Coût mesuré |
|---|---|---|
| `discover` | diffe le status report 3GPP vivant contre l'ancre locale | ~3 s |
| `fetch` | télécharge le delta et **convertit via LibreOffice → HTML** | 4m10, CPU-bound |
| `ingest` | parse le HTML en shards DuckDB par série (Rust) | minutes/série |
| `merge` | plie les shards dans le corpus, réécrit l'ancre, construit le FTS | ~6 min |
| `embed` | vectorise sur GPU en réutilisant chaque hash de contenu connu | le long pôle |
| `enrich` | catalogue DynaReport, OpenAPI 5GC, registre LI | ~2 min |
| `paragraphs` | stocke chaque paragraphe une fois et pointe dessus (ADR 0004) | ~9 min |
| `index` | construit et **gèle** le HNSW cosine | 1m46 |
| `validate` | contrat de complétude + `anchorcheck` | ~30 s |
| `smoke` | démarre le vrai serveur et prouve que le vectoriel est resté actif | ~30 s |

Points qui ne se devinent pas en lisant le code :

- **On parse le HTML de LibreOffice, pas le DOCX en natif.** Le chemin
  `zip.NewReader → word/document.xml` a existé (`internal/ooxml`) et a été retiré.
  Conséquence : LibreOffice est le goulot d'étranglement, pas le GPU.
- **`merge` rend d'abord au corpus la forme que le write side connaît.** Un corpus
  converti sert `clauses` comme une VUE ; `merge --base` recopie la base *table par
  table* (`duckdb_tables()`), donc la vue est laissée derrière et `schema.sql` la
  recrée **vide** dans la destination. Le fold remplirait cette table vide pendant
  que `clause_occ` garde ses 2 752 688 occurrences — et `max_chunk_id()` lirait 0,
  donnant au shard des `chunk_id` qui entrent en collision avec l'existant. Donc
  `merge` lance `migrate-paragraphs --restore` avant de plier, et `paragraphs`
  reconvertit après. Le write side ne connaît jamais ADR 0004 : c'est ADR 0001 qui
  l'exige, et ça coûte une reconstruction groupée (1m47 pour 2,87 Go, mesuré).
- **`merge` avant `embed`**, l'inverse de l'ancienne CI — cf. §13.
- **Le batch d'embedding est dynamique**, dimensionné sur `nvidia-smi` et
  `--vram-fraction`, avec repli réversible sur OOM. Pas de « batch de 32 ».
- **Chaque étape est adressée par contenu.** `goal plan` donne le différentiel ;
  `goal run --only <étapes>` exécute un sous-ensemble **sans** sauter les
  préconditions. Un `goal run` nu après un changement dans `internal/goal`
  refait 45 min de GPU pour rien : `--only` est impératif.
- **`goal status` ne recalcule rien**, il relit l'état persisté. Seul
  `goal plan` fait le différentiel.

## 7. Périmètre réel du corpus

Le cadrage MVP « Rel-17/18/19, séries 23/24/29/33/38, ~150 specs » est **dépassé**.
Ce que le corpus contient au 2026-08-26, mesuré :

| | |
|---|---|
| Clauses | **2 752 688** |
| Specs distinctes / versions | **3 568** / **20 163** |
| Séries | **31** — 03, 21 à 38, 41 à 52, 55 |
| Releases | **19** — Rel-4 à Rel-20 (plus Phase 1/2 sur les vieilles séries) |
| Opérations API 5GC | **8 562** (+ 27 889 schémas) |
| Événements LI | **405** (Rel-19), 1 697 champs, 1 039 types ASN.1 |
| ETSI | **14** livrables Lawful Interception, base séparée |

| Capacité | État |
|---|---|
| FTS BM25 (DuckDB) | ✓ |
| Vecteurs HNSW cosine (VSS), gelés | ✓ |
| Reranker BGE-v2-m3 | ✓ seam optionnelle (build tag `onnx`) |
| Glossaire TS 21.905 + miner | ✓ |
| Changelog (Change History Annex) | ✓ ; pipeline CR complet toujours reporté |
| OpenAPI 5GC / registre LI | ✓ overlays, cf. `docs/local-pipeline.md` |
| Bras sparse | produit, **jamais consommé** — cf. §13 |
| Graph KuzuDB | ❌ jamais construit |

**Plancher d'embedding : `Rel-99`.** Tout ce qui est antérieur reste
délibérément à `NULL` et n'est jamais compté par le contrat — un `validate` qui
oublie de transmettre ce plancher recale un corpus complet.

## 8. Pièges identifiés (à éviter dans le code)

1. **"R0" n'existe pas.** Lineage réel : Phase 1 (1992) → Phase 2 → Rel-96 → Rel-97 → Rel-98 → Rel-99 → Rel-4 → ... → Rel-19 (en cours) → Rel-20 (2027).
2. **DOCX, pas PDF.** Les PDF 3GPP sont des artefacts dérivés. On parse les `.docx` (et `.zip` qui les enveloppent). **Jamais d'OCR** sur ce corpus.
3. **Versions non monotones.** `(release, version, publication_date)` est nécessaire pour ordonner ; semver seul ne suffit pas. Exemple : `23.501 v16.15.0` peut paraître _après_ `v17.5.0`.
4. **CR multi-spec.** Une seule CR peut modifier `TS 23.501 + TS 23.502 + TS 29.501` simultanément. Table `changes` indexée par `cr_number`, pas par `spec_id`.
5. **Acronymes contextuels.** `AMF` = Access and Mobility Management Function (5GC) **ou** Application Management Function (IMS legacy). Toujours qualifier par release/domain.
6. **NE → NF n'est pas 1:1.** MME se sépare en AMF + SMF + (parties résiduelles). Modèle many-to-many avec `confidence` et `evolution_type`.
7. **TS ≠ TR.** TS = normatif (~700+ actives), TR = informatif/étude. Toujours filtrer par défaut sur TS.
8. **Frozen ≠ stable.** Rel-18 frozen en juin 2024 mais ASN.1 + corrections continuent ~12 mois. Distinguer drafts (`xx.0.0`) vs versions stables.

## 9. Structure de repo (réelle, 2026-08-26)

```
3gpp-mcp/
├── cmd/
│   ├── goal/            # LE point d'entrée : la machine à états 20 étapes
│   ├── server/          # MCP server (stdio + HTTP) + bootstrap subcommand
│   ├── validate/        # contrat de complétude des données
│   ├── anchorcheck/     # l'ancre ne doit pas revendiquer du texte absent
│   ├── discover-etsi/   # énumère les livrables ETSI LI
│   ├── li-audit/        # cross-check an LI event catalogue vs indexed text
│   ├── embedid/         # imprime l'identité d'embedding (source unique)
│   ├── export-delta/    # exporte le delta d'un corpus
│   ├── dbcount/         # comptages de sanité sur une DB
│   ├── split/           # découpe un corpus en shards
│   └── bench/           # offline retrieval benchmark (IR metrics)
├── rust/                # LE CÔTÉ ÉCRITURE (cf. docs/adr/0001)
│   ├── parse/           # HTML LibreOffice → ParsedSpec
│   ├── ingest/          # parse → shards DuckDB (+ branche --etsi)
│   ├── discover/        # diff status report ↔ ancre, plan de réparation
│   ├── embedder/        # embedder dense GPU (ONNX Runtime + CUDA)
│   ├── embed-core/      # tokenisation et fenêtrage partagés
│   ├── identity/        # EmbedIdentity, miroir de contracts/identity.toml
│   └── store/           # bins : merge, embed_io, overlay, freeze_hnsw
├── internal/
│   ├── goal/            # la machine à états : DAG, fingerprints, runner, état
│   ├── model/           # Spec, Clause, Change, Acronym, Evolution
│   ├── store/           # DuckDB (FTS + HNSW) wrappers, sharded reads, schema.sql
│   ├── catalog/         # DynaReport metadata overlay (WG, title, freeze_date)
│   ├── enrichmeta/      # métadonnées d'enrichissement
│   ├── etsicat/         # catalogue ETSI (énumération + résolution)
│   ├── evolseed/        # seed des évolutions NE → NF
│   ├── embed/           # BGE-M3 ONNX embedder seam (build tag onnx)
│   ├── rerank/          # optional BGE-reranker-v2-m3 seam (build tag onnx)
│   ├── onnxrt/          # shared ONNX Runtime init (process-global)
│   ├── search/          # intent router + BM25/vector backends + RRF
│   ├── releaseview/     # release-scoped clause views
│   ├── eval/            # IR eval harness (graded queries + metrics)
│   ├── metrics/         # compteurs de service
│   ├── retry/           # backoff partagé (403 ≠ transitoire, cf. §8)
│   ├── bootstrap/       # self-provisioning: fetch DB snapshot + models
│   ├── mcp/             # Tools MCP (search_spec, get_changelog, ...)
│   ├── registry/        # wires the set of enabled subjects
│   ├── subject/         # domain-vertical plugins (glossary, li/asn1)
│   └── subjectmeta/     # métadonnées et empreintes des sujets
├── contracts/
│   ├── identity.toml            # l'identité d'embedding, source unique
│   └── accepted-absences.txt    # absences légitimes, AVEC leur raison
├── scripts/
│   ├── local/           # toolchain portable Windows (bootstrap + env)
│   ├── lib/             # helpers de conversion partagés
│   ├── corpus.sh        # download → unzip → LibreOffice → HTML
│   ├── etsi-corpus.sh   # idem côté ETSI
│   ├── fetch-5g-apis.sh # overlay OpenAPI 5GC
│   ├── fetch-li-asn.sh  # overlay registre ASN.1 TS 33.128
│   └── *_test.sh        # suites shell, exécutées par l'étape `test`
├── data/              # gitignored
│   ├── 3gpp.duckdb    # le corpus
│   ├── etsi.duckdb    # ETSI, SÉPARÉ, jamais fusionné
│   ├── sources/       # origin (zip), convert (html), asn, 5g-apis
│   └── models/
│       └── bge-m3.onnx
├── .local/            # gitignored : toolchain, binaires, shards, logs, état
├── docs/              # local-pipeline.md, adr/, architecture, research
├── mk/                # fragments Makefile (local.mk)
├── tests/
├── .devcontainer/     # template kodflow synchronisé via /update
├── .claude/           # commandes/agents/hooks via image template
├── .github/           # CI (crons corpus désactivés, cf. §13)
├── .githooks/
├── .vscode/
├── .mcp.json          # branchement client du serveur local
├── AGENTS.md          # specs des agents IA utilisés
├── CLAUDE.md          # ce fichier
├── Makefile           # build, ingest, serve, test
├── go.mod
└── README.md
```

**Ce qui a disparu et ne reviendra pas** : `cmd/ingest`, `cmd/merge`,
`cmd/discover`, `cmd/ingest-catalog`, `cmd/ingest-openapi`, `internal/ingest`,
`internal/htmlparse`, `internal/ooxml`, `internal/openapi` — le côté écriture est
passé en Rust (`docs/adr/0001`). `data/3gpp.kuzu/` n'a jamais existé.

## 10. Workflow de dev assumé avec Claude Code

Le projet est conçu pour être développé **avec** Claude Code, pas malgré lui.

| Étape | Commande recommandée |
|---|---|
| Démarrer une session | `/warmup` |
| Nouvelle fonctionnalité | `/plan "..."` → review → `/do` |
| Doc / recherche normative | `/search "..."` (local-first sur `~/.claude/docs/`, fallback web) |
| Commits conventionnels | `/git --commit` |
| PR GitHub | `/git --pr` (auto-détection GitHub) |
| Code review en local | `/review` (5 agents specialists en parallèle) |
| Linting Go | `/lint` (golangci-lint piloté par hooks) |
| Sync template | `/update` (tarball + merge `devcontainer.local.json`) |
| Secrets | `/secret` (1Password CLI, vault `halys/3gpp-mcp/...`) |

**Règles propres au projet** :

- Le **specialist Go** (`developer-specialist-go`) fait foi pour le style. `gofmt`, `go vet`, `golangci-lint` sont bloquants.
- Aucun `nolint` sans commentaire d'explication.
- Aucune dépendance ajoutée sans justification dans la MR.
- Toute réponse de l'IA qui inclurait du code Python pour ce projet doit être refusée — **Go uniquement**.

## 11. Hooks et garde-fous

Les hooks fournis par `kodflow/devcontainer-template` sont actifs sans modification :

- **PreToolUse** : git-guard, security scan, RTK token rewrite.
- **PostToolUse** : format + lint Go, security, test, feature update.
- **Stop** : résumé de session.
- **PreCompact** : préservation du contexte.

Ajouts spécifiques au projet (à venir, dans `.githooks/`) :

- `pre-commit` : refuser tout fichier `.py` dans `cmd/` ou `internal/`.
- `pre-commit` : refuser toute modif de `CLAUDE.md` sans label `arch-change` dans la MR.

## 12. Données externes utilisées

| Source | URL | Usage |
|---|---|---|
| 3GPP archive | https://www.3gpp.org/ftp/Specs/archive/ | Toutes les versions de toutes les specs |
| 3GPP dynareport | https://www.3gpp.org/dynareport/ | État des releases, mapping spec→WG, CR DB |
| TSG FTP | https://www.3gpp.org/ftp/tsg_ran/ , /tsg_sa/ , /tsg_ct/ | CRs et tdocs (V2) |
| TS 21.905 | série 21 sur le FTP | Glossaire 3GPP canonique (seed) |

## 13. Verrouillages explicites

Les décisions ci-dessous sont **figées**. Une PR/MR proposant l'inverse doit être explicitement justifiée _et_ recevoir le label `arch-change` :

- ✅ **Write-side Rust / read-side Go** (`arch-change` 2026-06-19, cf. §2) : les
  producteurs DuckDB sont des binaires Rust ; Go ouvre read-only + sert. Inverser
  (réintroduire un write-side Go) demande un nouvel `arch-change`.
- ❌ Pas de Python (sauf kernels Kaggle GPU offline — embed/export, jamais au query)
- ❌ Pas d'Ollama / LLM local au moment du query
- ❌ Pas de KV store nu (Bolt, Badger, LMDB) — KV n'a ni FTS, ni vector, ni SQL
- ❌ Pas de Neo4j (JVM) — KuzuDB est préféré pour rester embedded
- ❌ Pas d'Elasticsearch (licence, opex)
- ⚠️ **Parsing** : HTML (LibreOffice-convert) est le chemin 3GPP primaire ; ETSI
  extrait la couche-texte des PDF (poppler, **jamais d'OCR**). « DOCX uniquement »
  était la cible V1 ; corrigé par la réalité du corpus (cf. mémoire reference_3gpp_doc_format).
- ❌ Pas d'OCR
- ❌ Pas de chunking par token-window arbitraire — toujours clause-aware
- ❌ Pas de résumés côté serveur — Claude synthétise
- ✅ **`merge` AVANT `embed`** (l'inverse de l'ancienne CI). `ingest` rebase les
  `chunk_id` à ~0 par shard : partager un ledger entre shards fait **sauter des
  clauses par collision**, en silence. Après le merge les ids sont uniques, donc
  un ledger unique est sûr *et* donne la dédup de contenu sur tout le corpus
  (2,74× de GPU économisé, mesuré).
- ✅ **fp32, pas fp16.** La précision fait partie de l'`EmbedIdentity` : basculer
  coûte un re-embed intégral du corpus.
- ⚠️ **Le bras sparse est produit mais consommé par personne.** Son fold au bake
  n'a jamais été écrit, donc la couche n'atteint pas l'image servie. Ne pas
  relancer de campagne GPU dessus tant que le consommateur n'existe pas.
- ❌ **Ne pas réactiver les crons corpus** de `.github/workflows/` (marqueur
  `# [local-pipeline] trigger desactive`, `workflow_dispatch` conservé) : ils
  échouaient ~28 fois par jour contre une infra qui n'indexe plus.
- ✅ **ETSI reste SÉPARÉ** (`etsi.duckdb`), jamais fusionné dans `3gpp.duckdb`.
  Le serveur fédère à la lecture via `--etsi-db`.

## 14. Liens utiles

- Repo GitHub : https://github.com/kodflow/3gpp-mcp
- Template parent : https://github.com/kodflow/devcontainer-template
- DuckDB Go : https://github.com/marcboeker/go-duckdb
- KuzuDB Go : https://github.com/kuzudb/go-kuzu
- ONNX Runtime Go : https://github.com/yalue/onnxruntime_go
- MCP Go SDK : https://github.com/mark3labs/mcp-go
- BGE-M3 : https://huggingface.co/BAAI/bge-m3
- 3GPP portal : https://portal.3gpp.org/
