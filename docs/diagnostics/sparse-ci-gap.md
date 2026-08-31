# Diagnostic — pourquoi la CI n'a jamais produit le sparse

> **RÉSOLU (2026-08-31), et pas de la façon envisagée ici.** Ce document reste
> comme archéologie : il décrit correctement pourquoi le sparse n'existait pas,
> mais il propose de réparer la CI, et **il n'y a plus de CI de build/release** :
> ces workflows sont supprimés, seul `post-commit.yml` subsiste parce qu'il est
> le status requis par la branch ruleset. La passe sparse tourne en local
> (`make build/sparse`, étape `sparse` du DAG), le modèle bi-tête est cuit dans
> l'image parce qu'un modèle actif dense-only ferait tomber le bras en silence,
> et le contrat `DATA_CONTRACT=dense+sparse` refuse de publier un corpus dont
> `clause_sparse` est vide ou estampillé par un autre modèle.
> Voir [`../automation/data-image.md`](../automation/data-image.md).
>
> Date : 2026-06-15 · Branche : `feat/sparse-embed-smoke-proven`
> Question : « pourquoi l'embed sparse n'a pas marché via la CI alors qu'elle aurait dû
> détecter qu'il n'existe pas et le faire (uniquement le sparse, sans refaire le dense
> BGE-M3 déjà fait) ? »

## Verdict

**Rien n'a planté.** Le sparse n'a jamais été produit parce qu'**aucun maillon de la CI
ne le déclenche** — ni la détection, ni l'export du modèle, ni l'étape d'embed. Le code
applicatif sparse est complet et testé (store inverted-index, seam ONNX, bras RRF,
pastille dashboard, `cmd/embed --sparse-only`), mais il est **débranché de la chaîne CI**.
Il y a donc trois trous distincts, et c'est leur cumul qui explique l'absence de sparse.

Le `--sparse-only` est bien conçu pour **ne PAS refaire le dense** : c'est une passe
dédiée, résumable, qui remplit `clause_sparse` et sort, sans toucher la colonne `embedding`
(`cmd/embed/main.go:85-101`). Donc l'attente « ne pas refaire le BGE-M3 dense » est
réalisable — simplement, personne ne l'appelle en CI.

---

## Trou n°1 — la détection d'incrément ignore totalement le sparse

La décision « qu'est-ce qui doit être (ré)indexé » repose sur trois signaux, **aucun ne
regarde le sparse** :

- **Drift de version corpus** (`rust/discover` : delta site vs `corpus-index.json`).
- **Drift de footprint subject** (`subjectmeta.ChangedSeries` — code des verticales li/glossary).
- **Drift d'identité de build** : `SpecIngestIdentity`, `GlobalEnrichmentIdentity`, `EmbedIdentity`.

Or l'`EmbedIdentity` — la **seule** porte de re-embed — est calculée sans aucune
composante sparse (`internal/model/pipeline.go:109-128`) :

```go
type EmbedParts struct {
    ModelID, ModelRevision, TokenizerRevision string
    VectorDim, NormalizationMode, Precision   string
    // ❌ aucune composante sparse (sparse_revision / has_sparse)
}
func (p EmbedParts) Identity() string {
    return digest12("embed-v1", p.ModelID, p.ModelRevision, p.TokenizerRevision,
        p.VectorDim, p.NormalizationMode, p.Precision)
}
```

Conséquence : que le sparse soit présent, absent, ou que son modèle change,
`EmbedIdentity` **ne bouge pas** → `discover` ne voit aucun drift → aucun re-embed forcé.

De plus, `SparseAvailable()` est un flag **runtime** calculé à l'ouverture
(`SELECT EXISTS(... FROM clause_sparse)`, `internal/store/sparse.go:39-55`), **jamais
persisté en meta**. Il n'existe pas d'équivalent du `hnsw_state="frozen"` pour le sparse,
donc même un check côté pipeline n'aurait rien à interroger.

### Pire : la passe dense skip tout, donc n'atteint jamais le sparse

`cmd/embed` (mode dense par défaut) ne ré-embed que les clauses dont le hash a dérivé
(`internal/embed/apply.go` ~l.194) :

```go
want := ClauseHash(it.Heading, it.Text, modelID)
if it.StoredHash == want { continue } // déjà embedée avec ce texte+modèle → skip
```

Sur une DB dense déjà à jour, **toutes** les clauses sont skippées. Ce test ne regarde
jamais `clause_sparse`. Donc même si la CI relançait `cmd/embed` (dense), il ne ferait
rien — et n'écrirait surtout aucun sparse (le sparse n'est PAS dans cette passe).

---

## Trou n°2 — le modèle sparse n'est jamais exporté/fetché en CI

Le bras sparse exige un modèle BGE-M3 **exporté avec la tête sparse** : le backend ONNX
n'allume `EmbedSparse` que si `ModelSpec.SparseOutput != ""`
(`internal/embed/embed_onnx_sparse.go` : sinon `"model has no sparse head"`).

Or les deux modèles du registre embarqué (`internal/embed/models.yaml`) — `bge-m3` et
`bge-m3-fp16` — **ne déclarent pas** `sparse_output`. Et côté CI :

- `scripts/fetch-model.sh` télécharge le dense (+ reranker via `WITH_RERANKER`) — **pas de
  `WITH_SPARSE`, pas d'export sparse**.
- Le job model de `corpus-matrix.yml` exporte fp32 + fp16 dense uniquement.

Donc même si on appelait `cmd/embed --sparse-only` en CI, il sortirait en erreur
« the active embedder has no sparse head » (`cmd/embed/main.go:90-92`).

---

## Trou n°3 — aucun workflow n'appelle `--sparse-only`

`grep -rin "sparse" .github/workflows/` ⇒ **0 résultat**. La chaîne
discover → matrix → embed → merge → bake est 100 % dense :

- `corpus-matrix.yml:867` : `./bin/embed --db "$OUT" --embed-floor … --require-semantic`
  (jamais `--sparse-only`).
- `scripts/kaggle/kernel-embed.py` (kernel GPU de prod) : build `cmd/embed`, passe dense.
- `corpus-data-image.yml` : bake HNSW dense + reranker, **aucun bake d'index sparse**.

Les seules références sparse vivantes sont **manuelles / recherche**, non câblées à un
workflow :
- `scripts/export-bge-m3-sparse.py` — recette d'export du modèle sparse.
- `scripts/kaggle/kernel-export-sparse.py` — kernel d'export one-off (modifs locales non
  commitées).
- `scripts/kaggle/kernel-sparse-embed-smoke.py` — smoke `--sparse-only` (dispatch manuel).

---

## Tableau récapitulatif

| Étape CI | Chemin dense | Chemin sparse | État |
|---|---|---|---|
| Détection (discover/EmbedIdentity) | version + EmbedIdentity | aucune composante sparse | ❌ jamais déclenché |
| Fetch/export modèle | fp32 + fp16 dense (+reranker) | pas de `WITH_SPARSE` | ❌ absent |
| Embed (shard/Kaggle) | `cmd/embed` (dense, skip-by-hash) | `--sparse-only` jamais appelé | ❌ absent |
| Merge | overlay vecteurs denses | pas de rekey `clause_sparse` | ❌ absent |
| Bake image | freeze HNSW dense | pas d'index/colonne sparse | ❌ absent |
| Serve/dashboard | colonne `embedding` | `clause_sparse` vide → « absent » | ❌ runtime-only |

---

## Ce qu'il faudrait pour que la CI « détecte et fasse uniquement le sparse »

Sans jamais retoucher le dense :

1. **Rendre le sparse détectable.** Persister un marqueur DB (ex. `sparse_state` / `sparse_model_id`
   via `SetMeta`, comme `hnsw_state`) écrit à la fin de `runSparse`, **et/ou** ajouter une
   composante sparse à `EmbedParts` (`sparse_output` + revision) pour que `EmbedIdentity`
   flippe quand le sparse manque/change.
2. **Câbler la détection.** Dans `rust/discover` (ou un check de bake), comparer ce marqueur
   à l'attendu et émettre un signal « sparse-needed » quand `clause_sparse` est vide alors
   que le modèle a une tête sparse.
3. **Exporter le modèle sparse en CI.** Ajouter un gate `WITH_SPARSE` à `fetch-model.sh` /
   au job model (réutiliser `scripts/export-bge-m3-sparse.py`) et une entrée registre avec
   `sparse_output: sparse_weights`.
4. **Ajouter l'étape sparse-only.** Un job (ou branche du kernel Kaggle) qui lance
   `cmd/embed --db … --sparse-only` sur la DB dense existante (additif, résumable,
   idempotent — `internal/store/sparse.go:60-90`), puis re-bake.
5. **Garde de promotion.** `cmd/validate --require-sparse` (déjà existant,
   `cmd/validate/main.go:42,192-195`) sur les bakes sparse-enabled.

Coût : l'export sparse est rapide ; la passe `--sparse-only` est un **balayage GPU additif
sur les clauses existantes** (pas un re-embed dense), donc bien moins lourd que le dense
déjà fait — exactement le « uniquement le sparse » demandé.

## Références (file:line)

- `cmd/embed/main.go:85-101` — `--sparse-only` séparé, ne touche pas le dense.
- `cmd/embed/main.go:341-406` — `runSparse` : balayage `ClausesMissingSparse` → `SetSparse`.
- `internal/model/pipeline.go:109-128` — `EmbedParts`/`EmbedIdentity` sans sparse.
- `internal/embed/embed_onnx_sparse.go` — exige `ModelSpec.SparseOutput`.
- `internal/embed/models.yaml` — `bge-m3` / `bge-m3-fp16` sans `sparse_output`.
- `internal/store/sparse.go:39-90` — `SparseAvailable` runtime-only ; `SetSparse` additif/idempotent.
- `internal/embed/apply.go:~194` — skip dense par hash (ignore le sparse).
- `cmd/validate/main.go:42,192-195` — `--require-sparse` (off par défaut).
- `.github/workflows/corpus-matrix.yml:561,867` — build+embed dense, pas de `--sparse-only`.
- `scripts/fetch-model.sh` — dense (+`WITH_RERANKER`), pas de `WITH_SPARSE`.
- `.github/workflows/*` — `grep sparse` = 0 résultat.
- Scripts sparse non câblés : `scripts/export-bge-m3-sparse.py`, `scripts/kaggle/kernel-export-sparse.py`, `scripts/kaggle/kernel-sparse-embed-smoke.py`.
