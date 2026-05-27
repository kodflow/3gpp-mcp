# Axe #6 — HNSW « build-then-freeze » : index vectoriel déterministe et résistant à la corruption

> **But.** Garantir que l'index HNSW (DuckDB VSS) du fichier `data/3gpp.duckdb`
> est construit **une seule fois** à l'ingestion, **vérifié**, puis **figé en
> lecture seule** pour le serveur MCP — tout en restant **reconstructible à
> l'identique** depuis la colonne `clauses.embedding`, qui reste la **source de
> vérité**. Le serveur ne reconstruit jamais l'index dans le chemin de query.

Ce document est *implementation-ready* : séquence SQL exacte, pragmas, points de
défaillance, détection de corruption + repli auto, et le verdict « shipper
l'index vs. reconstruire au premier `serve` ».

---

## 0. Pourquoi cet axe existe (contraintes DuckDB VSS)

L'extension VSS de DuckDB fournit un **vrai** index HNSW (≈3  ms sur le corpus
projeté, cf. CLAUDE.md §2.2), mais sa persistance est **expérimentale** et porte
plusieurs limites *dures* qu'on doit absorber par conception, pas espérer :

| Limite VSS (documentée) | Conséquence pour nous |
|---|---|
| Index **uniquement en RAM par défaut** ; persistance derrière `SET hnsw_enable_experimental_persistence = true`. | Toute session (ingest **et** serve) qui touche l'index doit poser ce flag **avant** `CREATE INDEX` / avant tout accès à la table indexée. |
| **WAL recovery non implémenté** pour les index custom : crash / arrêt brutal avec changements non-committés ⇒ **perte de données ou corruption de l'index**. | L'ingestion doit **committer + CHECKPOINT** proprement et fermer net. Le serveur doit ouvrir **read-only** (aucune écriture ⇒ aucun WAL ⇒ rien à rejouer). |
| Sérialisation **complète** au checkpoint (pas d'updates incrémentaux du fichier) ; rechargement *lazy* au premier accès à la table. | On construit l'index **après** le bulk-load, en un coup. On ne fait aucun DML sur `clauses` après `CREATE INDEX` côté serveur. |
| « Faster to create the index **after** the table has been populated » (parallélisme du bulk-load). | Ordre figé : *populate embeddings → CHECKPOINT → CREATE HNSW*. |
| Deletes = **tombstones** ; l'index devient *stale* ; remède `PRAGMA hnsw_compact_index('idx')` ou recréation. | On ne supprime jamais de lignes sur le DB figé. Un re-ingest = DB neuf (rebuild total), pas un patch. |
| Mémoire HNSW allouée **hors** `memory_limit` DuckDB ; index doit **tenir en RAM** ; vecteurs **FLOAT 32 bits** uniquement. | Dimensionner la RAM du poste serve sur la taille de l'index, pas sur `memory_limit`. `FLOAT[1024]` est déjà conforme. |
| Recommandation officielle : « **Do not use this feature in production environments.** » | On ne *contourne* pas l'avertissement, on le **neutralise** : build offline reproductible + ouverture read-only + détection de corruption + repli rebuild. C'est exactement l'objet de cet axe. |

**Procédure de récupération officielle** (à connaître, mais qu'on cherche à ne
jamais déclencher) : lancer DuckDB séparément, `LOAD vss` **puis** `ATTACH` le
fichier, afin que la fonctionnalité HNSW soit disponible **pendant le replay du
WAL**. Notre design rend ce cas quasi-impossible (read-only au serve, fermeture
propre à l'ingest), mais on l'implémente comme **filet** (cf. §6).

> Aligné avec `docs/INDEXING.md` §3 : l'index HNSW est `CREATE INDEX … USING
> HNSW (embedding) WITH (metric='cosine')`, best-effort, construit post-ingestion
> si `EMBEDDER` est actif ; la colonne `embedding` (FLOAT[1024]) est la source.

---

## 1. Invariant central : l'embedding est la source de vérité, l'index est un cache

```
clauses.embedding  (FLOAT[1024], persisté, déterministe)   ← SOURCE DE VÉRITÉ
        │  CREATE INDEX … USING HNSW (embedding)
        ▼
clauses_hnsw       (structure HNSW en RAM, sérialisée au checkpoint) ← CACHE jetable
```

Conséquences directes :

1. **Reconstructibilité garantie.** Tant que `embedding` est intacte, l'index est
   reconstructible par un seul `CREATE INDEX`. La corruption de l'index n'est
   **jamais** une perte de données — c'est une perte de *cache*.
2. **Déterminisme du hash d'ingestion.** Le hash de reproductibilité (CLAUDE.md
   §1 « Reproductibilité d'ingestion ») se calcule sur les **données** (specs,
   versions, clauses + leurs embeddings), **jamais** sur les octets de l'index
   HNSW — la sérialisation HNSW n'est pas garantie *byte-stable* d'une version de
   VSS à l'autre, et la construction parallèle peut différer dans la disposition
   interne sans changer les résultats k-NN. **L'index est exclu du hash.**
3. **Le repli est toujours sûr.** Si l'index manque/est corrompu au serve, on
   peut soit reconstruire (si writable), soit dégrader en *scan exact*
   (`array_cosine_distance` sans index, cf. `SearchVectors` actuel qui n'exige
   pas l'index — il fait un full-scan ORDER BY). Visible, jamais bloquant.

---

## 2. Séquence de build à l'ingestion (figée)

Ordre **non négociable** — chaque étape protège la suivante.

```
(1) bulk-load   : InsertClauses (embedding NULL) + tables catalogue
(2) embed       : SetEmbedding(...) en batch (UPDATE clauses SET embedding=…)
(3) FTS         : PRAGMA create_fts_index(...)            ← (axe BM25, indépendant)
(4) CHECKPOINT  : flush WAL → fichier, état propre AVANT l'index custom
(5) VSS on      : INSTALL vss; LOAD vss; SET hnsw_enable_experimental_persistence=true
(6) CREATE HNSW : CREATE INDEX clauses_hnsw … USING HNSW (embedding) WITH (metric='cosine')
(7) CHECKPOINT  : sérialise l'index HNSW dans le fichier
(8) VERIFY      : sanity k-NN + comptage + plan EXPLAIN (index utilisé)
(9) FREEZE      : SetMeta(hnsw_state, 'frozen'), SetMeta(embedding_count, N), hash data
(10) CLOSE      : fermeture propre (pas de WAL pendant)
```

### SQL / pragmas exacts

```sql
-- (4) état propre avant de créer un index custom
CHECKPOINT;

-- (5) extension + persistance expérimentale (obligatoire AVANT CREATE INDEX)
INSTALL vss;
LOAD vss;
SET hnsw_enable_experimental_persistence = true;

-- (6) index en un coup, APRÈS que la colonne embedding est entièrement peuplée
--     (bulk-load parallèle > insertion incrémentale, cf. README VSS)
CREATE INDEX IF NOT EXISTS clauses_hnsw
  ON clauses USING HNSW (embedding) WITH (metric = 'cosine');

-- (7) forcer la sérialisation de l'index dans le fichier .duckdb
CHECKPOINT;
```

> `internal/store` a déjà `EnableVSS` (= INSTALL/LOAD/SET persistence) et
> `BuildHNSW` (= EnableVSS + CREATE INDEX IF NOT EXISTS … cosine). Cet axe ajoute
> autour : les **deux CHECKPOINT** (avant/après), la **vérification**, et les
> **marqueurs de gel** dans `schema_meta`. Aucune signature existante n'est cassée.

### Pourquoi CHECKPOINT avant *et* après

- **Avant (étape 4)** : on entre dans `CREATE INDEX` avec un WAL vide. Si le build
  plante, le fichier reste un DB de données valide *sans* index → repli trivial.
- **Après (étape 7)** : l'index HNSW n'est écrit dans le fichier **qu'au
  checkpoint**. Sans ce `CHECKPOINT`, fermer le process laisserait l'index dans
  le WAL → exactement le scénario « custom index + WAL » non supporté. On
  **élimine** le WAL avant de fermer.

### (8) Vérification post-build (gate de l'ingest)

Avant de marquer `frozen`, l'ingest **doit** valider, sinon il échoue (exit ≠ 0) :

```sql
-- a. l'index existe
SELECT count(*) FROM duckdb_indexes()
 WHERE index_name = 'clauses_hnsw';                       -- attendu : 1

-- b. toutes les clauses destinées à l'embedding en ont un (pas de trou)
SELECT count(*) FILTER (WHERE embedding IS NULL),
       count(*) FILTER (WHERE embedding IS NOT NULL)
  FROM clauses;                                           -- non-NULL == N attendu

-- c. l'index est réellement emprunté par une requête k-NN (plan)
EXPLAIN
SELECT chunk_id
  FROM clauses
 ORDER BY array_cosine_distance(embedding, CAST([…] AS FLOAT[1024]))
 LIMIT 5;                            -- le plan doit montrer HNSW_INDEX_SCAN

-- d. sanity sémantique : un vecteur d'une clause connue se retrouve top-1
--    (auto-recherche : la clause est sa propre meilleure voisine, score ≈ 1.0)
```

Règles de la requête accélérée (sinon **full-scan silencieux**, l'index est
ignoré) :
- `ORDER BY array_cosine_distance(embedding, <const>)` **ASC** + `LIMIT k` ;
- la fonction de distance **doit matcher le `metric` de l'index** (`cosine` ⇒
  `array_cosine_distance`) ;
- le 2ᵉ argument doit être une **constante** `FLOAT[1024]`.
  ⚠️ `SearchVectors` actuel écrit `1.0 - array_cosine_distance(...) AS score …
  ORDER BY score DESC` : trier sur l'alias `score DESC` ≡ `array_cosine_distance
  ASC`, mais selon la version de VSS le *push-down* HNSW peut exiger la forme
  canonique `ORDER BY array_cosine_distance(embedding, c) LIMIT k`. **À vérifier
  par `EXPLAIN`** (étape c) ; si l'index n'est pas pris, réécrire la requête sous
  forme canonique et calculer `score = 1 - distance` en projection externe.

### (9) Marqueurs de gel (`schema_meta`)

On réutilise `SetMeta` (déjà présent) pour rendre l'état **auto-descriptif** et
permettre au serve de décider sans deviner :

| key | value | usage |
|---|---|---|
| `hnsw_state` | `frozen` \| `none` \| `building` | gate serve-time |
| `hnsw_metric` | `cosine` | garde-fou (cohérence requête/­index) |
| `embedding_dim` | `1024` | garde-fou |
| `embedding_count` | `N` (clauses non-NULL au build) | **détection de corruption** (§6) |
| `embedding_model` | `bge-m3` | provenance / reproductibilité |
| `ingest_data_hash` | `sha256(…données…)` | hash déterministe, **index exclu** |

`hnsw_state='building'` est posé **avant** l'étape 6 et basculé à `frozen` après
l'étape 8 réussie : si l'ingest meurt entre les deux, le serve voit `building`
(≠ `frozen`) et traite l'index comme absent (repli, §6).

---

## 3. Garantir la reconstructibilité depuis `embedding`

La rebuild-abilité est une **propriété**, pas un espoir. On la verrouille ainsi :

1. **La colonne `embedding` est toujours persistée et committée** (étape 2) et
   **CHECKPOINT-ée avant** la création d'index (étape 4). Elle ne dépend
   d'aucune extension : un DB sans `vss` la lit toujours.
2. **Le build d'index est idempotent et pur** : `CREATE INDEX IF NOT EXISTS …
   USING HNSW (embedding) WITH (metric='cosine')` ne dépend que de la colonne +
   des hyperparamètres par défaut (`ef_construction=128, M=16, M0=2M`). Mêmes
   embeddings + mêmes params ⇒ même comportement k-NN. *(Les octets sérialisés
   peuvent varier — sans importance, l'index est hors-hash.)*
3. **`RebuildHNSW` = DROP + CHECKPOINT + CREATE + CHECKPOINT** (un seul chemin de
   code, partagé entre l'ingest et le repli serve) :

```sql
SET hnsw_enable_experimental_persistence = true;   -- idempotent
DROP INDEX IF EXISTS clauses_hnsw;                 -- purge un index stale/corrompu
CHECKPOINT;                                        -- état propre
CREATE INDEX clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric='cosine');
CHECKPOINT;                                        -- re-sérialise
```

4. **Test de non-régression** (tests/) : ingest sur un mini-corpus → snapshot des
   top-k pour un jeu de requêtes → `DROP INDEX` → rebuild → **les top-k doivent
   être identiques** (à l'ordre près des ex æquo). Verrouille « l'index est un
   cache reconstructible » comme invariant testé.

---

## 4. Chargement au serve (read-only, zéro rebuild)

Le serveur MCP est **lecteur pur**. Objectif : ouvrir, charger `vss`, **ne rien
construire**, répondre.

```
serve:
  open(path, read_only)         ← ACCESS_MODE = READ_ONLY  (pas de WAL, pas de replay risqué)
  INSTALL vss; LOAD vss
  SET hnsw_enable_experimental_persistence = true   ← requis pour ACCÉDER à un index persisté
  -- AUCUN CREATE INDEX ici
  check schema_meta.hnsw_state == 'frozen'  &&  index présent  &&  count cohérent
        ├─ OK            → HNSW actif (k-NN ~3 ms)
        └─ KO/corrompu   → repli (§6) : scan exact OU rebuild si writable
```

Points clés :

- **Ouvrir en read-only** est la meilleure protection anti-corruption : pas
  d'écriture ⇒ pas de WAL ⇒ le scénario « crash avec changements non-committés »
  ne peut pas se produire au serve. Le seul moment d'écriture du fichier est
  l'ingest, hors ligne, contrôlé.
- `LOAD vss` + `SET hnsw_enable_experimental_persistence=true` restent
  **nécessaires** côté serve : sans le flag, DuckDB peut refuser/ignorer un index
  HNSW persisté. Le flag est *best-effort* (déjà le cas dans `EnableVSS`) — s'il
  échoue (offline, extension absente), on dégrade (recherche lexicale + scan
  exact), visible via un équivalent `VSSAvailable()`.
- **Chargement lazy** : l'index se charge en RAM au **premier accès à la table**.
  Conséquence : prévoir un **warm-up** optionnel (une requête k-NN bidon au
  démarrage) pour payer le coût de désérialisation hors du premier appel client.
- **Implémentation `store`** : ajouter un `OpenReadOnly(path)` (DSN
  `?access_mode=read_only`) et un `LoadVSS(ctx)` symétrique à `LoadFTS` : il
  charge l'extension, **ne crée rien**, et marque `vssAvailable=true` seulement si
  `hnsw_state=frozen` + index présent. `BuildHNSW`/`EnableVSS`/`SetEmbedding`
  restent réservés à l'ingest.

### read-only + persistance expérimentale : risque résiduel

`hnsw_enable_experimental_persistence` est un `SET` de session ; sur une connexion
read-only il configure l'accès, pas une écriture. Si une version de VSS refusait
de servir un index persisté sans capacité d'écriture, le repli **scan exact**
(§6) couvre le cas sans dégrader la justesse (seulement la latence). À valider sur
la version de VSS pinnée ; documenter le résultat ici.

---

## 5. Shipper l'index *ou* le reconstruire au premier serve ? — **verdict**

> **Décision : SHIP l'index (DB figé, index inclus), avec rebuild automatique en
> repli.** C'est le « build-then-freeze » du titre. Le rebuild-on-first-serve est
> le **filet**, pas le mode nominal.

### Comparatif

| Critère | **Ship l'index (figé)** ✅ | Rebuild au 1ᵉʳ serve |
|---|---|---|
| Latence 1ᵉʳ query | ~0 (lazy load + warm-up optionnel) | **build complet** (minutes sur gros corpus) bloquant |
| Serve read-only | **Oui** (rien à écrire) | **Non** : build ⇒ writable ⇒ WAL ⇒ réintroduit le risque de corruption |
| Reproductibilité du livrable | DB **autoportant**, distribuable par `scp` (CLAUDE.md §1 mono-binaire/local-first) | dépend de l'env d'exécution au démarrage |
| Taille du livrable | + taille index (acceptable, FLOAT32) | DB plus petit |
| Surface de corruption | confinée à l'ingest (offline, contrôlé) | déplacée vers chaque démarrage serveur |
| Cohérence avec la doctrine | « build une fois, fige, expédie » | « calcule à chaud » (anti-pattern ici) |

Le seul avantage du rebuild-on-serve (livrable plus léger) ne compense pas la
perte du **read-only** : reconstruire au démarrage force un mode writable, donc un
WAL, donc rouvre précisément la fenêtre de corruption que cet axe veut fermer.

### Conséquence opérationnelle

- **Mode nominal** : `ingest` produit `data/3gpp.duckdb` **avec** l'index figé +
  `hnsw_state=frozen`. `serve` l'ouvre **read-only**.
- **Filet** : si `serve` détecte un index manquant/corrompu/incohérent et qu'on
  l'autorise explicitement (flag `--allow-rebuild`), il rouvre en **read-write**,
  exécute `RebuildHNSW` (§3), re-CHECKPOINT, re-`frozen`, puis **re-ouvre
  read-only**. Sinon il sert en **scan exact** dégradé (visible).

---

## 6. Détection de corruption + repli automatique

### Signaux de corruption / d'incohérence (au serve, avant de servir)

1. **Marqueur absent/incohérent** : `schema_meta.hnsw_state != 'frozen'`
   (ingest interrompu, état `building`/`none`).
2. **Index manquant** : `duckdb_indexes()` ne liste pas `clauses_hnsw`.
3. **Dérive de comptage** : `count(embedding NOT NULL) != embedding_count`
   mémorisé → la colonne et l'index ne sont plus en phase.
4. **Garde-fous** : `hnsw_metric != 'cosine'` ou `embedding_dim != 1024`.
5. **Échec à l'exécution** : une requête k-NN sentinelle au démarrage renvoie une
   erreur DuckDB (désérialisation HNSW échouée) ou un top-1 absurde (la clause
   sentinelle ne se retrouve pas elle-même avec score ≈ 1.0).

### Arbre de décision (warm-up déterministe au démarrage)

```
LoadVSS():
  if !vss_extension_ok            → vssAvailable=false ; lexical seul + scan exact si embeddings
  elif hnsw_state != 'frozen'     → CORRUPTION_SUSPECTED
  elif index absent               → CORRUPTION_SUSPECTED
  elif count != embedding_count   → CORRUPTION_SUSPECTED
  elif sentinel k-NN error/absurd → CORRUPTION_SUSPECTED
  else                            → HNSW actif ✅

CORRUPTION_SUSPECTED:
  if --allow-rebuild && writable  → RebuildHNSW (§3) → re-verify → frozen → reopen read-only
  else                            → DÉGRADÉ : scan exact (array_cosine_distance full-scan),
                                     log "[hnsw] degraded reason=…", vssAvailable=false
```

### Pourquoi le repli « scan exact » est toujours acceptable

`SearchVectors` (déjà implémenté) fait `… ORDER BY 1.0 -
array_cosine_distance(embedding, ?) DESC LIMIT k` sur `embedding IS NOT NULL`.
**Sans index, ça reste exact** — juste O(N) au lieu de O(log N). Sur le corpus V1
(séries 23/24/29/33/38, Rel-17→19, ~10⁵–10⁶ clauses) c'est plus lent (dizaines de
ms à ~1 s) mais **correct et cité**. La doctrine « dégrade visiblement, ne bloque
jamais » (CLAUDE.md, INDEXING.md §3) est respectée : on ne renvoie jamais de
résultat faux, au pire un résultat plus lent.

### Filet ultime : la procédure officielle ATTACH

Si un `data/3gpp.duckdb` *writable* a été corrompu par un arrêt brutal (ne devrait
pas arriver au serve read-only), la récupération est : process neuf →
`LOAD vss;` → **puis** `ATTACH 'data/3gpp.duckdb';` (l'ordre compte : VSS doit
être chargé avant l'attach pour que le replay WAL voie la fonctionnalité HNSW).
En pratique, on préfère **jeter l'index et le reconstruire** depuis `embedding`
(§3) : plus simple, déterministe, et la donnée n'est jamais en jeu.

---

## 7. Plan d'implémentation pas-à-pas (sans casser l'existant)

> Périmètre **hors de cet axe** (à coder dans `internal/store`, `cmd/ingest`,
> `cmd/server`) — listé ici comme feuille de route, **non implémenté dans ce
> document** (cet axe ne touche qu'un fichier markdown).

1. **Ingest — `cmd/ingest`** : après la phase embed, appeler en séquence
   `CHECKPOINT` → `BuildHNSW` (existant) → `CHECKPOINT` → vérif (§2.8) → poser les
   `SetMeta` de gel (§2.9). Échec dur si la vérif ne passe pas.
2. **`store.RebuildHNSW(ctx)`** : DROP + CHECKPOINT + CREATE + CHECKPOINT (§3),
   réutilisé par l'ingest et le repli serve.
3. **`store.OpenReadOnly(path)`** : `sql.Open("duckdb", path+"?access_mode=read_only")`,
   `SetMaxOpenConns(1)`. Le serve l'utilise par défaut.
4. **`store.LoadVSS(ctx)`** : symétrique de `LoadFTS` — `INSTALL/LOAD vss`,
   `SET hnsw_enable_experimental_persistence=true`, vérif `hnsw_state=frozen` +
   index présent + count cohérent ; `vssAvailable=true` seulement alors.
5. **`store.VSSAvailable()`** + warm-up k-NN sentinelle au démarrage serve.
6. **Routing search** : si `VSSAvailable()` → branche HNSW ; sinon → scan exact
   (même fonction `SearchVectors`, l'index est transparent) ; fusion RRF inchangée.
7. **Tests** (§3.4) : rebuild-determinism (top-k stables après DROP+rebuild) ;
   corruption simulée (effacer le marqueur / fausser `embedding_count`) → repli
   scan exact ; vérif que `serve` n'émet **jamais** de `CREATE INDEX`.
8. **Makefile** : `make ingest` produit le DB figé ; `make serve` ouvre read-only ;
   un `make verify-index` qui rejoue les gates §2.8 sur un DB livré.

---

## 8. Risques & mitigations

| Risque | Probabilité | Impact | Mitigation |
|---|---|---|---|
| Index HNSW non emprunté (requête non canonique) → full-scan silencieux, « lent sans raison » | Moyenne | Latence ×100 | Gate `EXPLAIN` (§2.8c) ; réécrire `SearchVectors` en forme canonique si besoin |
| Arrêt brutal de l'ingest pendant `CREATE INDEX`/avant le 2ᵉ CHECKPOINT | Faible | Index absent/partiel | `hnsw_state` reste `building` → serve traite comme absent → repli ; rebuild trivial |
| Drift de version VSS change le format sérialisé | Faible | Index illisible au serve | Index **hors-hash** + rebuild depuis `embedding` ; pin VSS dans le build |
| RAM serveur insuffisante (index hors `memory_limit`) | Moyenne | OOM au load | Dimensionner la RAM sur la taille d'index ; mesurer à l'ingest, stocker dans `schema_meta` |
| `SET persistence=true` refusé sur connexion read-only (version VSS) | Faible | k-NN indispo | Repli scan exact (exact, plus lent) ; valider sur la version pinnée |
| Corruption silencieuse (résultats subtilement faux) | Très faible | Justesse | Sentinelle self-match (top-1 ≈ 1.0) au warm-up ; sinon CORRUPTION_SUSPECTED |
| Double-writer (ingest pendant un serve) | Faible | WAL/corruption | Serve **read-only** (n'acquiert pas le lock writer) ; ingest exclusif hors ligne |

---

## 9. TL;DR (verdict figé)

1. **Ingest** : `populate embeddings → CHECKPOINT → CREATE HNSW (cosine) →
   CHECKPOINT → VERIFY (EXPLAIN+self-match) → SetMeta(hnsw_state=frozen) → CLOSE`.
2. **`embedding` = source de vérité**, l'index = **cache reconstructible** ;
   l'index est **exclu du hash** de reproductibilité.
3. **Serve** : ouvrir **read-only**, `LOAD vss` + flag persistence, **jamais de
   `CREATE INDEX`** ; warm-up lazy-load ; vérifier les marqueurs.
4. **Corruption** détectée par marqueur/comptage/sentinelle → repli **scan exact**
   (exact, plus lent) ou **rebuild** depuis `embedding` si `--allow-rebuild`.
5. **On ship l'index figé** (pas de rebuild-on-serve nominal) : préserve le
   read-only, le livrable autoportant `scp`-able, et confine la corruption à
   l'ingest offline. C'est la neutralisation concrète de l'avertissement
   « experimental / not for production » de DuckDB VSS.

---

## Sources

- DuckDB — Vector Similarity Search (VSS) extension : <https://duckdb.org/docs/stable/core_extensions/vss>
- DuckDB blog — *Vector Similarity Search in DuckDB* (persistance, sérialisation au checkpoint, chargement lazy, mémoire hors `memory_limit`) : <https://duckdb.org/2024/05/03/vector-similarity-search-vss>
- GitHub — `duckdb/duckdb-vss` (création post-bulk-load, `hnsw_enable_experimental_persistence`, tombstones + `PRAGMA hnsw_compact_index`, récupération `LOAD vss` puis `ATTACH`) : <https://github.com/duckdb/duckdb-vss>
- Projet — `docs/INDEXING.md` §3 (les 3 index sur `clauses`, best-effort/dégradation) et `internal/store/store.go` (`BuildHNSW`, `EnableVSS`, `LoadFTS`, `SetEmbedding`, `SearchVectors`).
