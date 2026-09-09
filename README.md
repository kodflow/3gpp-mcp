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

Chiffres **mesurés** dans les bases servies le 2026-09-09, pas des ordres de
grandeur. Ils portent sur un digest précis, pas sur le tag mouvant :

```text
ghcr.io/kodflow/3gpp-mcp@sha256:0349248311a48073f8eb4b2252914e326b67f9d27b9434926cb13751cb2e3ec6
```

`:latest` pointe sur ce digest à cette date ; épinglez le digest si vous voulez
que ces chiffres restent vrais. Pour les relire sur VOTRE copie, appelez l'outil
`help` : il compte dans la base servie au lieu de répéter ce tableau.

| | 3GPP | ETSI |
|---|---|---|
| Clauses indexées | **2 751 918** | **3 168 482** |
| Specs / deliverables | 3 568 | 5 142 |
| Versions | 20 163 | 11 822 |
| Vecteurs denses (1024d) | 821 387 | 902 159 |
| Postings sparse | 194 051 110 | 127 308 329 |
| Glossaire (acronymes) | 14 126 | 28 154 |
| Index HNSW cosinus | gelé | gelé |
| BM25 / FTS | oui | oui |
| Clause sans vecteur dû | **0** | **0** |
| Taille sur disque | 24,1 GiB | 18,4 GiB |
| Axe d'évolution | **release** (Rel-99 → dernière) | **version** (toutes les versions de chaque deliverable) |

Les vecteurs portent sur des **corps de paragraphe dédupliqués** (ADR 0004), pas
sur les clauses : 821 387 corps distincts couvrent les 2 751 918 occurrences de
clause côté 3GPP. Un paragraphe identique répété dans quarante versions est
vectorisé une fois — c'est ce qui rend le corpus complet tenable, et non un trou
de couverture (`missing_content=0`, `unaccounted=0`).

S'ajoutent au texte des clauses, côté 3GPP : **8 562 opérations** et **27 889
schémas** OpenAPI 5GC, **1 131 événements** d'interception légale, 18 releases.

**L'historique des change requests est incomplet, et le serveur le dit.** La
table `changes` porte 61 321 lignes mais n'a plus d'écrivain depuis que
l'ingest HTML est passé côté Rust : elle couvre **311 des 3 568 specs** et
s'arrête à ce que chacune tenait ce jour-là. `get_changelog` écarte désormais les
lignes incitables — l'en-tête de la table de change history était lu comme une
ligne de données et TS 23.501 répondait « une modification, intitulée *Date* » —
et il nomme la version où l'historique s'arrête quand le corpus en tient une plus
récente. Pour diffuser l'évolution d'une clause entre deux versions, utilisez
`trace_clause` : il compare le texte du corpus, pas cette table.

Plus : le modèle d'embedding **bi-tête** BGE-M3 (dense + lexical appris,
identité `38067f8c6efe`), le modèle sparse `b13103bce7ae`, le reranker
cross-encoder, ONNX Runtime et les extensions DuckDB `fts`/`vss`. Total ~50 GiB
de couches.

Les deux moitiés sont **fédérées, jamais fusionnées** : un `spec_id` commençant
par `ETSI ` part sur la base ETSI, le reste sur la base 3GPP, et une recherche
fédérée interroge les deux. C'est ce qui permet de garder deux axes d'évolution
distincts sans que l'un écrase l'autre.

### Ce que le corpus n'a pas, et pourquoi

Un index qui tait ses trous est un index qui ment. Les trois connus :

- **Aucun change request côté ETSI.** Un historique 3GPP survit à la conversion
  `.doc → HTML` sous forme de TABLE ; ETSI publie des PDF, et `pdftotext -layout`
  aplatit cette table en colonnes désalignées. Une ligne reconstruite de travers
  citerait la mauvaise transition de version, ce qui est pire que rien.
  `get_changelog` le dit et renvoie vers `trace_clause`.
- **4 VERSIONS sur les 11 826 de la liste de travail ne sont pas convertibles**
  (PDF sans couche texte) — quatre versions, pas quatre livrables ; deux
  livrables disparaissent avec elles parce qu'ils n'en avaient qu'une. D'où
  5 142 livrables / 11 822 versions tenus contre 5 144 / 11 826 listés. Les
  quatre sont **nommées** dans `.local/state/etsi-absences.tsv`, et
  `validate --require-worklist` réconcilie liste de travail / corpus / registre
  à chaque build : une version absente sans raison enregistrée fait échouer le
  build.
- **`evolutions` ne couvre que le 3GPP** (seed curaté NE→NF, EPC↔5GC).

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

Trois façons, de la plus simple à la plus impliquée. Dans tous les cas : Claude
Code redémarre, le serveur apparaît, **13 outils** sont disponibles.

### A. L'image (recommandé — rien à installer)

```jsonc
// .mcp.json à la racine de VOTRE projet
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

Le paquet est **privé** : `docker login ghcr.io` avec un token `read:packages`
avant le premier `pull`. L'image porte les deux corpus, les modèles et les
runtimes — aucun accès réseau à l'exécution, rien à télécharger au démarrage.

### B. Depuis ce dépôt, avec le corpus local

Le `.mcp.json` du dépôt est **déjà écrit et suivi dans git** : ouvrir ce dossier
avec Claude Code suffit. Ce qu'il contient, et pourquoi :

```jsonc
{
  "mcpServers": {
    "3gpp": {
      "type": "stdio",
      // Windows. Sous Linux/macOS : ".local/bin/server-full" et les .so/.dylib
      // correspondants dans les deux chemins ONNX ci-dessous.
      "command": ".local/bin/server-full.exe",
      "args": ["serve", "--db", "data/3gpp.duckdb", "--etsi-db", "data/etsi.duckdb"],
      "env": {
        "EMBED_MODEL": "bge-m3-sparse",
        "EMBED_MODEL_DIR": "data/models/bge-m3-sparse",

        // DEUX BINDINGS, DEUX RUNTIMES — ne pas en omettre un.
        // ORT_DYLIB_PATH : le crate RUST (rust/embed-core), qui fait l'embedding
        // de la requête. Épinglé sur la version contre laquelle il a été compilé.
        "ORT_DYLIB_PATH": ".local/toolchain/ort/onnxruntime-win-x64-gpu-1.20.1/lib/onnxruntime.dll",
        // ONNXRUNTIME_SHARED_LIBRARY_PATH : le binding GO, qui fait le reranker
        // cross-encoder. Épingle DIFFÉRENTE. Un seul fichier ne peut pas servir
        // les deux.
        "ONNXRUNTIME_SHARED_LIBRARY_PATH": "data/models/onnxruntime/lib/onnxruntime.dll"
      }
    }
  }
}
```

**Omettre la seconde variable ne fait pas échouer le démarrage** — le serveur
sert quand même, avec le reranker éteint :

```text
The requested API version [25] is not available, only API versions [1, 20]
are supported in this build. Current ORT Version is: 1.20.1
… embedder=true reranker=false
```

C'est exactement le genre de panne que ce projet traque : ça marche, ça répond,
et une arme sur quatre manque. `server_info` est le seul endroit qui le dit — et
il le dit précisément :

```json
{"reranker": false,
 "reranker_reason": "the ONNX runtime would not initialise: Platform-specific
                     initialization failed: Error setting ORT API base: 2"}
```

`TestMCPJsonWiresBothONNXRuntimes` lit ce fichier et échoue si une variable
manque ou si les deux pointent le même runtime.

### C. Le binaire seul

Voir [`docs/install.md`](./docs/install.md) : token, cache, et quelles variantes
de build savent faire du sémantique.

## Une fois lancé : les deux premiers appels

**`help` d'abord.** Il compte dans la base réellement servie plutôt que de
répéter une doc, et rend la carte question → outil :

```text
> utilise l'outil help du serveur 3gpp
```

**`server_info` ensuite**, pour savoir quelles armes tournent *à cet instant* —
et, quand l'une est éteinte, **pourquoi**. Une installation **sémantique
complète, avec les deux corpus** (A ou B ci-dessus) répond :

```json
{"lexical": true, "semantic": true, "sparse": true, "reranker": true,
 "hnsw": true, "fts": true,
 "etsi": {"attached": true, "embedding_model_ok": true}}
```

Tous les `false` ne sont pas des pannes. Une build lexicale seule répond
légitimement `semantic: false` et `reranker: false` ; sans `--etsi-db`,
`etsi.attached` est `false` et c'est normal. Ce qui compte est le motif : chaque
capacité éteinte porte un `reason` / `reranker_reason` / `sparse_reason` qui dit
si c'est un choix de build ou une installation à réparer.

Ensuite, posez vos questions en français ou en anglais : `search_spec` est
l'entrée principale et Claude choisit les autres outils tout seul.

**Les outils qui rendent du contenu de spécification refusent de répondre sans
citation** — `search_spec`, `get_spec`, `search_api`, `trace_clause`,
`find_cross_references`, `resolve_term`, `li_events`, `trace_evolution` portent
tous `citations: [{spec_id, release, version, clause, url}]`. `help`,
`server_info`, `list_specs` et `list_releases` décrivent le serveur ou son
catalogue, pas le corpus : ils n'ont rien à citer et ne prétendent pas le faire.

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
| 5 — Glossaire | ✅ 14 126 acronymes (3GPP) + 28 154 (ETSI) | `internal/abbrev`, `cmd/seed-glossary`, `rust/ingest` |
| 6 — Changelog (Change History) | ⚠️ **figé** : 311 specs sur 3 568, plus d'écrivain depuis le passage de l'ingest en Rust — `get_changelog` le dit et renvoie vers `trace_clause` | `internal/store`, `internal/mcp` |
| 7 — Router + RRF + ordre versions | ✅ | `internal/search` |
| 8 — Serveur MCP + 13 outils | ✅ | `internal/mcp`, `cmd/server` |
| 9 — Reranker cross-encoder | ✅ actif par défaut | `internal/rerank` |
| 10 — Moitié ETSI fédérée | ✅ 3 168 482 clauses, 11 822 versions tenues (11 826 listées, 4 PDF sans couche texte) | `cmd/goal` (`ingest-etsi`) |
| 11 — Contrat de complétude sur les DEUX moitiés | ✅ 8 vérifications, dont réconciliation liste de travail et non-réingestion | `cmd/validate`, `scripts/data-contract.sh` |

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
