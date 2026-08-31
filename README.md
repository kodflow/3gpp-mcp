# 3gpp-mcp

> Serveur **MCP (Model Context Protocol)** exposant l'intégralité du corpus 3GPP (Phase 1 → dernière Release) à Claude Code, en local, sans hallucination.

Le serveur retourne des **fragments de spécification cités** (`spec_id`, `release`, `version`, `clause`, `url`) — jamais des résumés. Claude raisonne, l'index sert.

## Utilisation : une seule image, tout dedans

```jsonc
// .mcp.json (Claude Code)
{
  "mcpServers": {
    "3gpp": {
      "type": "stdio",
      "command": "docker",
      "args": ["run", "-i", "--rm", "ghcr.io/kodflow/3gpp-mcp:latest"]
    }
  }
}
```

Rien d'autre à installer, rien à télécharger au premier lancement, aucun accès
réseau nécessaire à l'exécution. L'image porte le corpus 3GPP **et** ETSI, le
modèle d'embedding **bi-tête** (dense + lexical appris), le reranker
cross-encoder, et les extensions DuckDB `fts`/`vss` — donc BM25, HNSW, sparse et
reranking fonctionnent hors ligne. Le paquet est **privé** (texte de spec
verbatim, cf. [`DATA_NOTICE.md`](./DATA_NOTICE.md)) : `docker login ghcr.io`
avec un token portant `read:packages` avant le premier `pull`.

L'image se construit **sur la machine qui a le corpus** — `make image` — et non
sur un runner : voir [`docs/automation/data-image.md`](./docs/automation/data-image.md).

---

## TL;DR architectural

> **Stack figée — voir [`CLAUDE.md`](./CLAUDE.md) pour le verdict détaillé et les verrouillages.**

| Couche | Choix |
|---|---|
| Langage | **Go** 1.23+, CGO autorisé |
| Stockage | **DuckDB** (FTS + HNSW VSS) |
| Graph (V2) | **KuzuDB** embedded |
| Embeddings | **BGE-M3** via ONNX Runtime |
| MCP SDK | `github.com/mark3labs/mcp-go` |
| Parsing | `archive/zip` + `encoding/xml` (DOCX natif) |
| Distribution | Binaire statique unique (`mcp-3gpp`) |

Routing par intention : la majorité des requêtes (TS, IE, NF nominaux) finissent en BM25 à ~10 ms. Hybrid (BM25 + vecteurs + RRF) en fallback. KuzuDB pour les relations NE↔NF.

---

## Prérequis

- **VS Code + DevContainer** (le projet est livré avec sa propre configuration DevContainer)
- Docker / Docker Desktop
- Optionnel : 1Password Service Account (pour `/secret` et VPN)

Le devcontainer apporte tout le reste : Go, Python (pour scripts batch), Claude Code, ktn-linter, RTK (token savings), hooks IA, etc. Voir [`.devcontainer/CLAUDE.md`](./.devcontainer/CLAUDE.md).

## Démarrage

```bash
# 1. Cloner
git clone https://github.com/kodflow/3gpp-mcp.git
cd 3gpp-mcp

# 2. Renseigner les secrets locaux (gitignorés)
$EDITOR .devcontainer/.env       # OP_SERVICE_ACCOUNT_TOKEN, GIT_USER, GIT_EMAIL

# 3. Ouvrir dans VS Code et rebuild devcontainer
code .
# Command Palette → "Dev Containers: Rebuild and Reopen in Container"
```

Une fois dans le container :

```bash
# Build
make build                    # → ./bin/mcp-3gpp

# Ingestion (one-shot, écrit data/3gpp.duckdb)
make ingest ARGS="--spec 33.128,33.127,21.905"       # sous-ensemble LI (rapide)
make ingest ARGS=""                                   # TOUT le corpus (5414 HTML)
EMBEDDER=local make ingest ARGS="--series 33"         # + vecteurs HNSW (sémantique)

# Lancer le MCP en stdio
make serve

# TESTER soi-même
make poc                      # test E2E : events LI X2→MDF2 par NE/NF (TS 33.128)
make demo                     # interroge les 8 tools en vrai (JSON-RPC) et affiche

# Backend sémantique ONNX réel (optionnel, ~2.3 Go)
make model                    # bootstrap BGE-M3 + ONNX Runtime, puis build -tags onnx
```

> **Comment c'est indexé et les relations entre éléments** : voir
> [`docs/INDEXING.md`](./docs/INDEXING.md) (tables, index FTS/HNSW/b-tree,
> hiérarchie `clause_path`, cross-refs, CR multi-spec, évolutions NE↔NF).

## Brancher sur Claude Code

`mcp.json` est **gitignoré** (peut contenir des PAT ou chemins sensibles).
Créer un fichier local — soit `~/.claude/mcp.json`, soit `mcp.json` à la
racine du repo si tu utilises le devcontainer — avec :

```json
{
  "mcpServers": {
    "3gpp": {
      "command": "/workspace/bin/mcp-3gpp",
      "args": ["serve"]
    }
  }
}
```

Claude Code redémarre, le serveur apparaît, les 8 tools sont disponibles.

## Surface MCP

| Tool | Rôle |
|---|---|
| `search_spec` | Retrieval hybride (BM25 + vecteurs + RRF) avec citations |
| `get_spec` | Fetch d'une spec ou d'une clause précise |
| `get_changelog` | CRs entre deux releases (depuis Change History Annex) |
| `list_releases` | Versions, freeze dates |
| `resolve_term` | Glossaire 3GPP (seed TS 21.905) |
| `trace_evolution` | Évolutions NE↔NF (V2, via KuzuDB) |
| `find_cross_references` | Specs/clauses référencées |
| `list_specs` | Catalogue filtré par release/série/WG |

Chaque réponse contient un bloc `citations: [{spec_id, release, version, clause, url}]`. Pas de citation possible = pas de réponse.

## État d'implémentation (V1)

Les 8 phases du moteur de retrieval sont implémentées et le service build/serve.

| Phase | État | Paquet |
|---|---|---|
| 1 — Modèle + schéma + store DuckDB | ✅ | `internal/model`, `internal/store` |
| 2 — Parsing HTML → clauses | ✅ | `internal/htmlparse` |
| 3 — Indexation FTS BM25 + filtres | ✅ (HNSW câblé, attend les vecteurs) | `internal/store` |
| 4 — Embeddings BGE-M3 | ⚙️ interface + dégradable ; backend ONNX derrière `-tags onnx` | `internal/embed` |
| 5 — Glossaire (21.905) | ✅ seeder câblé (best-effort) | `internal/ingest` |
| 6 — Changelog (Change History) | ✅ | `internal/htmlparse` |
| 7 — Router + RRF + ordre versions | ✅ | `internal/search` |
| 8 — Serveur MCP + 8 tools | ✅ | `internal/mcp`, `cmd/server` |

**Deux écarts assumés avec l'archi figée (à régulariser en MR `arch-change`) :**

1. **Parsing HTML, pas DOCX natif** — ~55 % du corpus est du `.doc` binaire ; `scripts/corpus.sh` convertit tout en HTML via LibreOffice et l'ingestion parse ce HTML (couvre 100 % du corpus). Contredit CLAUDE.md §13.
2. **Embeddings désactivés par défaut** — le mono-binaire build sans le runtime ONNX ni le modèle ~2 Go ; la recherche dégrade en lexical (BM25/LIKE), visible jamais bloquant. Backend réel à brancher avec `-tags onnx`.

### POC — question cible (Lawful Interception)

> *« Combien d'events chaque NE/NF remonte-t-il en LI_X2 vers le MDF2 ? »*

Prouvé de bout en bout par un test E2E (ingest → DuckDB → serveur MCP → client → `get_spec` → recomposition citée) :

```bash
go test ./tests/e2e -run LIEvents -v     # ou: make test
```

Réponse sur **TS 33.128** (Rel-19, 19.6.0), extraite des sous-clauses « Generation of xIRI over LI_X2 » :

| NE/NF | Events X2→MDF2 | Clause |
|---|---|---|
| AMF | 13 | 6.2.2.2 |
| SMF | 9 | 6.2.3.2 |
| UPF | 4 | 6.2.3.5 |
| MME | 10 | 6.3.2.2 |
| SGW/PGW+ePDG | 8 | 6.3.3.2 |

Chaque ligne est citée (`TS 33.128 v19.6.0 §<clause>` + URL d'archive).

## Périmètre MVP

- **V1** : Rel-17 / 18 / 19, séries 23, 24, 29, 33, 38 (~150 specs)
- **V2** : KuzuDB pour les relations NE↔NF, CR pipeline complet, ingestion historique → Rel-15
- **V3** : reranker, ingestion Phase 1 → Rel-16, multi-utilisateurs Halys

## Workflow dev (assumé AI-first)

Le projet est développé **avec** Claude Code. Les skills `/plan`, `/do`, `/review`, `/lint`, `/git`, `/secret`, `/update` sont les commandes principales.

| Commande | Usage |
|---|---|
| `/warmup` | Charge le contexte projet |
| `/plan "..."` | Planifie une feature (RLM decomposition) |
| `/do` | Exécute le plan, itère jusqu'à succès |
| `/review` | Revue de code par 5 agents specialists en parallèle |
| `/lint` | Linting Go via ktn-linter |
| `/git --commit` | Commit conventionnel |
| `/git --pr` | Pull request GitHub (auto-détecté depuis l'origine) |
| `/git --commit` | Commit conventionnel + push |
| `/update` | Sync devcontainer-template (`devcontainer.local.json` préservé) |

Voir [`CLAUDE.md`](./CLAUDE.md) pour les verrouillages architecturaux et les pièges identifiés.

## Licence

Propriétaire — Halys.
