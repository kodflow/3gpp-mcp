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

```
┌─────────────────────────────────────────────────────────────┐
│  mcp-3gpp  (binaire Go avec CGO, ~25 MB statique)           │
└─────────────────────────────────────────────────────────────┘

Langage       : Go 1.23+               (productivité solo dev, mono-binaire)
CGO           : autorisé               (DuckDB, KuzuDB, ONNX Runtime)
MCP SDK       : github.com/mark3labs/mcp-go
Stockage      : DuckDB (CGO)           (FTS + VSS HNSW + SQL OLAP)
Graph (V2)    : KuzuDB embedded (CGO)  (relations NE↔NF, evolutions)
Embeddings    : ONNX Runtime (CGO) + BGE-M3 (1024 dim, 8k ctx, dense+sparse)
Reranker      : BGE-reranker-v2-m3 ONNX (optionnel, V2)
Doc parsing   : encoding/xml + archive/zip (DOCX = ZIP+XML, parsing natif)
Scraping      : net/http + golang.org/x/net (3GPP FTP — DOCX uniquement)
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
    spec_id      VARCHAR,
    release      VARCHAR,                 -- 'Rel-18'
    version      VARCHAR,                 -- '18.5.0'
    freeze_date  DATE,
    docx_url     VARCHAR,
    PRIMARY KEY(spec_id, release, version)
);

-- Chunks au niveau clause (jamais token-window arbitraire)
CREATE TABLE clauses (
    chunk_id     UBIGINT PRIMARY KEY,
    spec_id      VARCHAR,
    release      VARCHAR,
    version      VARCHAR,
    clause_path  VARCHAR,                 -- '5.2.3.1'
    heading      VARCHAR,
    text         VARCHAR,
    is_normative BOOLEAN,
    embedding    FLOAT[1024]              -- HNSW index dessus
);

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

## 5. Surface MCP — **8 tools, pas plus**

| Tool | Signature | Rôle |
|---|---|---|
| `search_spec` | `(query, release?, series?, spec_type?, top_k=10, mode='hybrid'|'lexical'|'semantic')` | Retrieval hybride avec citations |
| `get_spec` | `(spec_id, release, version?, clause?)` | Fetch d'une spec ou d'une clause précise |
| `get_changelog` | `(spec_id, from_release, to_release, clause?)` | Liste des CRs et leur impact |
| `list_releases` | `(spec_id)` | Toutes les `(release, version, freeze_date)` |
| `resolve_term` | `(term, release?)` | Définition + domaine + références |
| `trace_evolution` | `(entity, from_release, to_release)` | Sous-graphe d'évolution (V2) |
| `find_cross_references` | `(spec_id, clause?)` | Specs/clauses référencées |
| `list_specs` | `(release?, series?, working_group?)` | Catalogue filtré |

**Toute réponse contient un bloc `citations: [{spec_id, release, version, clause, url}]`.**

## 6. Pipeline d'ingestion

```
1. Scraper FTP   : net/http sur https://www.3gpp.org/ftp/Specs/archive/
                   Filtres : doc_type ∈ {TS,TR}, série ∈ {21..38}, release cible
2. Parser DOCX   : zip.NewReader → word/document.xml via encoding/xml
                   Extraction :
                     - heading hierarchy (w:pStyle Heading1-7)
                     - clauses (chunks par clause leaf)
                     - tables (w:tbl + gridSpan/vMerge)
                     - blocs ASN.1 (w:pStyle="PL")
                     - cross-refs (w:hyperlink)
                     - Annexe Change History (table en fin de spec)
3. Embeddings    : ONNX Runtime + BGE-M3 (CPU OK, GPU optionnel)
                   Batch de 32 chunks, dimensions 1024
4. Glossaire     : seed depuis TS 21.905 § 3.1 (définitions) et § 3.2 (acronymes)
                   + mining regex sur le corpus pour expansion contextuelle
5. CR pipeline   : (V2) scrape /ftp/tsg_*/ pour les CR documents
                   parsing du cover sheet (section 4 "Clauses affected")
6. Écriture DB   : COPY ... FROM ... (DuckDB bulk insert, columnar)
                   Création des index FTS + HNSW à la fin
```

## 7. Périmètre MVP (V1)

| Item | Inclus en V1 | Reporté |
|---|---|---|
| Releases | **Rel-17, Rel-18, Rel-19** (Phase 1 → Rel-16 en V2) | Phase 1 → Rel-16 |
| Séries | **23, 24, 29, 33, 38** (~150 specs) | 21, 22, 25, 26, 28, 31, 32, 35, 36, 37 |
| DOCX parsing | ✓ heading, tables, clauses, annexes | figures vision, formules OMML |
| FTS BM25 | ✓ DuckDB FTS | tuning de scoring custom |
| Vecteurs | ✓ HNSW DuckDB VSS | reranker |
| Glossaire | ✓ TS 21.905 seed + regex miner | extraction LLM offline |
| Changelog | ✓ Change History Annex | CR-pipeline complet |
| Graph (KuzuDB) | ❌ V2 | NE→NF evolutions complètes |
| Ollama batch | ❌ pas nécessaire en V1 | extraction d'entités assistée |

Objectif V1 : **~2-3 semaines solo dev**, livrable utilisable depuis Claude Code en local.

## 8. Pièges identifiés (à éviter dans le code)

1. **"R0" n'existe pas.** Lineage réel : Phase 1 (1992) → Phase 2 → Rel-96 → Rel-97 → Rel-98 → Rel-99 → Rel-4 → ... → Rel-19 (en cours) → Rel-20 (2027).
2. **DOCX, pas PDF.** Les PDF 3GPP sont des artefacts dérivés. On parse les `.docx` (et `.zip` qui les enveloppent). **Jamais d'OCR** sur ce corpus.
3. **Versions non monotones.** `(release, version, publication_date)` est nécessaire pour ordonner ; semver seul ne suffit pas. Exemple : `23.501 v16.15.0` peut paraître _après_ `v17.5.0`.
4. **CR multi-spec.** Une seule CR peut modifier `TS 23.501 + TS 23.502 + TS 29.501` simultanément. Table `changes` indexée par `cr_number`, pas par `spec_id`.
5. **Acronymes contextuels.** `AMF` = Access and Mobility Management Function (5GC) **ou** Application Management Function (IMS legacy). Toujours qualifier par release/domain.
6. **NE → NF n'est pas 1:1.** MME se sépare en AMF + SMF + (parties résiduelles). Modèle many-to-many avec `confidence` et `evolution_type`.
7. **TS ≠ TR.** TS = normatif (~700+ actives), TR = informatif/étude. Toujours filtrer par défaut sur TS.
8. **Frozen ≠ stable.** Rel-18 frozen en juin 2024 mais ASN.1 + corrections continuent ~12 mois. Distinguer drafts (`xx.0.0`) vs versions stables.

## 9. Structure de repo (cible)

```
3gpp-mcp/
├── cmd/
│   ├── ingest/        # CLI one-shot : scrape FTP, parse DOCX, write DuckDB
│   │   └── main.go
│   └── server/        # MCP server (stdio + SSE)
│       └── main.go
├── internal/
│   ├── docx/          # DOCX parser (zip+xml, heading hierarchy, tables, annexes)
│   ├── model/         # Spec, Clause, Change, Acronym, Evolution
│   ├── store/         # DuckDB (FTS + HNSW) + KuzuDB (V2) wrappers
│   ├── ingest/        # Pipeline d'ingestion
│   ├── search/        # Router de requêtes (BM25 / vector / graph / changelog)
│   ├── embed/         # ONNX Runtime + BGE-M3 wrapper
│   └── mcp/           # Tools MCP (search_spec, get_changelog, ...)
├── data/              # gitignored
│   ├── 3gpp.duckdb
│   ├── 3gpp.kuzu/
│   └── models/
│       └── bge-m3.onnx
├── docs/              # MkDocs si réactivé, architecture, ADRs
├── tests/
├── .devcontainer/     # template kodflow synchronisé via /update
├── .claude/           # commandes/agents/hooks via image template
├── .github/           # CI (workflows synchronisés)
├── .githooks/
├── .taskmaster/
├── .vscode/
├── AGENTS.md          # specs des agents IA utilisés
├── CLAUDE.md          # ce fichier
├── Makefile           # build, ingest, serve, test
├── go.mod
└── README.md
```

## 10. Workflow de dev assumé avec Claude Code

Le projet est conçu pour être développé **avec** Claude Code, pas malgré lui.

| Étape | Commande recommandée |
|---|---|
| Démarrer une session | `/warmup` |
| Nouvelle fonctionnalité | `/plan "..."` → review → `/do` |
| Doc / recherche normative | `/search "..."` (local-first sur `~/.claude/docs/`, fallback web) |
| Commits conventionnels | `/git --commit` |
| MR GitLab | `/git --pr` (auto-détection GitLab) |
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

- ❌ Pas de Python
- ❌ Pas d'Ollama / LLM local au moment du query
- ❌ Pas de KV store nu (Bolt, Badger, LMDB) — KV n'a ni FTS, ni vector, ni SQL
- ❌ Pas de Neo4j (JVM) — KuzuDB est préféré pour rester embedded
- ❌ Pas d'Elasticsearch (licence, opex)
- ❌ Pas de PDF parsing — DOCX uniquement
- ❌ Pas d'OCR
- ❌ Pas de chunking par token-window arbitraire — toujours clause-aware
- ❌ Pas de résumés côté serveur — Claude synthétise

## 14. Liens utiles

- Repo GitLab : https://github.com/kodflow/3gpp-mcp
- Template parent : https://github.com/kodflow/devcontainer-template
- DuckDB Go : https://github.com/marcboeker/go-duckdb
- KuzuDB Go : https://github.com/kuzudb/go-kuzu
- ONNX Runtime Go : https://github.com/yalue/onnxruntime_go
- MCP Go SDK : https://github.com/mark3labs/mcp-go
- BGE-M3 : https://huggingface.co/BAAI/bge-m3
- 3GPP portal : https://portal.3gpp.org/
