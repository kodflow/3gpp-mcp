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

Rien d'autre à installer, rien a telecharger au premier lancement, aucun accès
réseau nécessaire à l'exécution. Le paquet est **privé** (texte de spec verbatim,
cf. [`DATA_NOTICE.md`](./DATA_NOTICE.md)) : `docker login ghcr.io` avec un token
portant `read:packages` avant le premier `pull`.

### Ce que l'image contient

Chiffres **mesurés** dans les bases servies le 2026-09-04, pas des ordres de
grandeur. Ils portent sur un digest précis, pas sur le tag mouvant :

```text
ghcr.io/kodflow/3gpp-mcp@sha256:f2aa17e695871ddf33acb2e419f1250b602ac8b720cea9c329add374ce796642
```

`:latest` pointe sur ce digest à cette date ; épinglez le digest si vous voulez
que ces chiffres restent vrais. Pour les relire sur VOTRE copie, appelez l'outil
`help` : il compte dans la base servie au lieu de répéter ce tableau.

| | 3GPP | ETSI |
|---|---|---|
| Clauses indexées | **2 752 688** | **3 169 614** |
| Specs / deliverables | 3 568 | 5 142 |
| Versions | 20 163 | 11 822 |
| Vecteurs denses (1024d) | 821 387 | 902 159 |
| Postings sparse | 194 111 501 | 127 375 760 |
| Index HNSW cosinus | gelé | gelé |
| BM25 / FTS | oui | oui |
| Clause sans vecteur dû | **0** | **0** |
| Taille sur disque | 21,4 GiB | 18,4 GiB |
| Axe d'évolution | **release** (Rel-99 → dernière) | **version** (toutes les versions de chaque deliverable) |

Les vecteurs portent sur des **corps de paragraphe dédupliqués** (ADR 0004), pas
sur les clauses : 821 387 corps distincts couvrent les 2 752 688 occurrences de
clause côté 3GPP. Un paragraphe identique répété dans quarante versions est
vectorisé une fois — c'est ce qui rend le corpus complet tenable, et non un trou
de couverture (`missing_content=0`, `unaccounted=0`).

S'ajoutent au texte des clauses, côté 3GPP : **61 321 CR**, **8 562 opérations**
et **27 889 schémas** OpenAPI 5GC, **1 131 événements** d'interception légale,
1 300 acronymes, 18 releases.

Plus : le modèle d'embedding **bi-tête** BGE-M3 (dense + lexical appris,
identité `38067f8c6efe`), le modèle sparse `b13103bce7ae`, le reranker
cross-encoder, ONNX Runtime et les extensions DuckDB `fts`/`vss`. Total ~48 GiB
de couches.

Les deux moitiés sont **fédérées, jamais fusionnées** : un `spec_id` commençant
par `ETSI ` part sur la base ETSI, le reste sur la base 3GPP, et une recherche
fédérée interroge les deux. C'est ce qui permet de garder deux axes d'évolution
distincts sans que l'un écrase l'autre.

### Les quatre armes de recherche

| Arme | Ce qu'elle fait | Quand elle sert |
|---|---|---|
| BM25 / FTS | correspondance lexicale exacte | noms d'IE, de NF, références de clause |
| HNSW dense | proximité sémantique | question formulée autrement que la spec |
| Sparse (lexical appris) | termes pondérés par le modèle | vocabulaire technique rare |
| Cross-encoder | réordonne les candidats | précision sur le haut du classement |

`search_spec` les combine (RRF) par défaut. `server_info` dit lesquelles sont
**actives à cet instant** et, quand l'une est éteinte, **pourquoi** — à lire
avant de conclure qu'une arme manque.

### Vérifier que tout répond

```bash
docker run -i --rm ghcr.io/kodflow/3gpp-mcp:latest   # puis, en JSON-RPC : help, puis server_info
```

Le dépôt embarque la preuve utilisée en interne : `scripts/local/prove-serving.sh`
démarre le vrai serveur sur stdio et vérifie les sept armes sur les deux moitiés
en JSON-RPC réel. Elle sort `PROVE OK` ou échoue.

L'image se construit **sur la machine qui a le corpus** — `make publish` — et non
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
# L'orchestrateur de corpus — il ne refait que ce qui a réellement changé
make plan                     # ce que `make build` ferait, et POURQUOI. Ne change rien.
make build                    # tout : fetch → ingest → merge → embed → sparse → index → smoke → publish
make build/<étape>            # une seule étape ; `make steps` liste les noms
make status                   # l'état persisté, étape par étape

# Prouver que le serveur sert vraiment les quatre armes, sur les DEUX moitiés
make prove                    # JSON-RPC réel contre le vrai binaire → `PROVE OK`

# Construire l'image depuis le corpus local et la pousser sur GHCR.
# `publish` est la DERNIÈRE ÉTAPE du pipeline, pas un point d'entrée séparé :
# elle a une empreinte comme les autres, donc `make plan` dit si l'image publiée
# est en retard sur le corpus, et elle ne repousse rien quand elle est à jour.
make publish
```

`make build` est **l'orchestrateur du corpus**, pas un `go build` : chaque étape
déclare ses sources, et une étape qui n'a rien à faire **décline** au lieu de
reprogrammer tout l'aval. Sur un corpus déjà complet, un build converge vers
« rien à faire » — un `fetch` qui ne reçoit aucune nouvelle version décline, et
ingest, merge, embed et index sautent derrière lui.

Pour les binaires seuls, sans toucher au corpus : `make build-bin` bâtit le
serveur dans `bin/`, `make goal-bin` l'orchestrateur, et `make build/build-go`
tous les binaires de lecture (serveur + outils hors-ligne).

**Lire un plan** : `[SKIP]` prouvé · `[RUN ]` va tourner, pour la raison
affichée · `[RUN?]` sera re-décidé contre l'état réel quand sa dépendance aura
fini, et sauté si celle-ci n'a rien changé.

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

Claude Code redémarre, le serveur apparaît, les 13 outils sont disponibles.

## Surface MCP

13 outils. En cas de doute sur lequel appeler, `help` rend la carte complète
depuis le serveur lui-même.

| Tool | À appeler quand |
|---|---|
| `help` | **récap** : ce que le corpus contient (compté réellement), la carte question → outil, les réglages |
| `server_info` | quelles armes sont actives, et **pourquoi** l'une est éteinte |
| `search_spec` | question en texte libre sur le corps des clauses — l'entrée principale |
| `get_spec` | vous savez déjà la spec et la clause, vous voulez le texte |
| `search_api` | vous cherchez une operation ou un schema OpenAPI 5GC, pas de la prose |
| `trace_clause` | comment le TEXTE d'une clause a évolué, **paragraphe par paragraphe** |
| `trace_evolution` | comment un élément 4G se projette sur ses NF 5GC |
| `get_changelog` | les CRs entre deux releases d'une spec |
| `list_releases` | quelles releases/versions d'une spec sont dans le corpus |
| `list_specs` | parcourir le catalogue par release, série ou WG |
| `find_cross_references` | quelles specs une spec ou une clause référence |
| `resolve_term` | développer un acronyme, trouver où un terme est défini |
| `li_events` | définitions d'événements d'interception légale (TS 33.128) |

Chaque réponse contient un bloc `citations: [{spec_id, release, version, clause, url}]`.
Pas de citation possible = pas de réponse.

### Suivre une évolution entre versions

`trace_clause` est l'outil qui répond à « qu'est-ce qui a changé ». Il travaille
au **paragraphe**, pas à la clause : une clause dont une seule phrase a bougé
paraît entièrement neuve à un suivi clause-à-clause.

```jsonc
{"spec_id": "23.501", "clause": "5.4.4a",
 "from_release": "Rel-17", "to_release": "Rel-18"}
```

La réponse nomme l'**axe** qu'elle a suivi (`release` côté 3GPP, `version` côté
ETSI) et liste `axis_values`. Un champ nommé pour des releases qui porterait des
versions serait le genre d'erreur silencieuse que ce projet traque.

### Réglages

Variables lues au démarrage du serveur ; `help` en rend la liste à jour.

| Variable | Effet |
|---|---|
| `RT_DB` | chemin de la base 3GPP servie (défaut `data/3gpp.duckdb`) |
| `RT_DB_FULL` | chemin de la base ETSI attachée ; non défini = 3GPP seul |
| `EMBEDDER=off` | coupe l'embedder de requête : sémantique et sparse s'éteignent, BM25 reste |
| `RERANKER=off` | coupe le cross-encoder |
| `RERANK_WINDOW` | nombre de candidats rescorés (défaut 12) |
| `SEARCH_BUDGET` | budget par recherche avant résultats partiels (défaut 20s) |
| `EMBED_QUERY_CACHE` | entrées du cache d'embeddings de requête (défaut 512) |
| `ORT_EP` | execution provider ONNX (`cpu`, `cuda`) |
| `MCP3GPP_ALLOW_LEXICAL_FALLBACK` | `true` autorise un démarrage lexical si les vecteurs sont inutilisables ; **par défaut le serveur refuse de démarrer**, pour qu'un service silencieusement dégradé ne soit jamais servi |

## État d'implémentation

Le corpus complet est construit, indexé, embarqué et **prouvé en JSON-RPC réel**
(`make prove` → `PROVE OK — every arm live on both halves`).

| Phase | État | Paquet |
|---|---|---|
| 1 — Modèle + schéma + store DuckDB | ✅ | `internal/model`, `internal/store` |
| 2 — Parsing HTML → clauses | ✅ | `internal/htmlparse` |
| 3 — Indexation FTS BM25 + filtres | ✅ | `internal/store` |
| 4 — Embeddings BGE-M3 dense + sparse appris | ✅ gelés sur les deux moitiés | `internal/embed`, `rust/embedcore` |
| 5 — Glossaire (21.905) | ✅ 1 300 acronymes | `internal/ingest` |
| 6 — Changelog (Change History) | ✅ 61 321 CR | `internal/htmlparse` |
| 7 — Router + RRF + ordre versions | ✅ | `internal/search` |
| 8 — Serveur MCP + 13 outils | ✅ | `internal/mcp`, `cmd/server` |
| 9 — Reranker cross-encoder | ✅ actif par défaut | `internal/rerank` |
| 10 — Moitié ETSI fédérée | ✅ 3 169 614 clauses | `cmd/goal` (`corpus-etsi`) |

**Un écart assumé avec l'archi figée (à régulariser en MR `arch-change`) :**

1. **Parsing HTML, pas DOCX natif** — ~55 % du corpus est du `.doc` binaire ; `scripts/corpus.sh` convertit tout en HTML via LibreOffice et l'ingestion parse ce HTML (couvre 100 % du corpus). Contredit CLAUDE.md §13.

Le second écart historique — « embeddings désactivés par défaut » — **n'existe
plus** : ONNX Runtime et les deux modèles voyagent dans l'image, et le serveur
**refuse de démarrer** si les vecteurs sont inutilisables, plutôt que de servir
en silence une recherche dégradée en mots-clés. `MCP3GPP_ALLOW_LEXICAL_FALLBACK`
lève ce refus quand on le veut explicitement.

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

## Périmètre

- **Atteint** — tout le corpus 3GPP (Phase 1 → dernière release : 18 releases,
  3 568 specs, 20 163 versions) **et** toute la moitié ETSI (5 142 deliverables,
  11 822 versions) ; FTS + HNSW dense + sparse appris + reranker ; le tout dans
  **une seule image** qui sert les 13 outils. Couverture vérifiée par contrat :
  `over_claim=0 missing_content=0 unaccounted=0`.
- **Reste** — KuzuDB pour le graphe NE↔NF (la table `evolutions` en tient lieu
  en V1), multi-utilisateurs Halys.

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
