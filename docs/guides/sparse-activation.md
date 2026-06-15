# Activer le bras sparse (BGE-M3 learned-lexical) — sans refaire le dense

> Ce guide complète le diagnostic `docs/diagnostics/sparse-ci-gap.md`. Il décrit la
> chaîne d'activation **additive** : produire le sparse **sans jamais recalculer les
> vecteurs denses déjà bakés**, et le rendre **auto-détectable** par la CI.

## Principe : le sparse est une couche additive, identité séparée

Le sparse a sa **propre identité** (`model.SparseIdentity`, `internal/model/pipeline.go`),
**distincte** de `EmbedIdentity`. C'est volontaire : un changement de modèle sparse doit
déclencher une **passe sparse-only** sur les mêmes clauses, **sans** invalider les vecteurs
denses (longs à produire, déjà faits). `embed --sparse-only` ne touche jamais la colonne
`embedding` (`cmd/embed/main.go`).

À la fin d'une passe `--sparse-only`, le DB est estampillé `schema_meta.sparse_model = <SparseModelID>`.
C'est ce marqueur qui rend l'absence/obsolescence **détectable** (avant, rien ne l'était).

## Détection (désormais câblée)

| Endroit | Comportement |
|---|---|
| **serve** | `warnIfSparseMissing` (cmd/server/main.go) : si le binaire est sparse-capable mais `clause_sparse` vide → **log d'alerte** avec la commande exacte ; si `sparse_model` ≠ attendu → **alerte « stale »**. |
| **dashboard** | pastille « Sparse » + cause/fix (déjà présent). |
| **validate** | `--require-sparse` est **identity-aware** : échoue si `clause_sparse` vide **ou** si `sparse_model` ≠ identité attendue du build. Gate de promotion du bake. |
| **bake (self-heal)** | comparer `cmd/embedid --sparse` (attendu) au `sparse_model` du DB fusionné ; s'ils diffèrent → lancer la passe sparse-only GPU avant de baker. |

## Procédure d'activation (one-time, puis idempotente)

```bash
# 1. Exporter le modèle BGE-M3 AVEC la tête sparse (torch + FlagEmbedding).
WITH_SPARSE=1 scripts/fetch-model.sh
#    → data/models/bge-m3-sparse/model.onnx exposant [sentence_embedding, sparse_weights]

# 2. Rendre ce modèle actif (registre embed) : models.yaml
#    active: bge-m3-sparse  + entrée avec `sparse_output: sparse_weights`
#    (tokenizer_dir → data/models/bge-m3 ; fp16 possible si export fp16).

# 3. Vérifier que l'identité sparse s'allume (CGO-free) :
EMBED_MODELS_CONFIG=data/models/models.yaml go run ./cmd/embedid --sparse   # non vide

# 4. Peupler clause_sparse ADDITIVEMENT sur le DB dense fusionné (GPU = Kaggle ;
#    CPU = semaines pour 2,85 M clauses). NE recalcule PAS le dense.
#    Build onnx requis :
go build -tags onnx -o bin/embed ./cmd/embed
bin/embed --db data/3gpp.duckdb --sparse-only   # resumable, idempotent, stampe sparse_model

# 5. Garde de promotion :
go run -tags onnx ./cmd/validate --db data/3gpp.duckdb --require-sparse

# 6. Re-baker l'image data + redéployer (le serve allume alors la pastille « Sparse: on »).
```

> ⚠️ Pré-requis serve : le **modèle servi** doit lui-même porter la tête sparse
> (model.onnx avec `sparse_weights`). Sinon le binaire reste dense-only et
> `warnIfSparseMissing` ne se déclenche pas (rien n'est attendu). L'activation = baker
> l'image avec le modèle sparse-exporté **et** `clause_sparse` peuplé.

## Production GPU (Kaggle)

La passe `--sparse-only` corpus-scale tourne sur GPU comme le dense. Le chemin a été
**prouvé sur GPU** (#165, `scripts/kaggle/kernel-sparse-embed-smoke.py` : export inline +
`--sparse-only` sur ORT-CUDA, 2ᵉ passe = 0). Pour le corpus complet : un kernel qui (1)
récupère le DB dense fusionné, (2) build `cmd/embed -tags onnx`, (3) `--sparse-only` par
lots résumables, (4) pousse `clause_sparse` (overlay), que le bake fusionne.

## Pourquoi ce n'était pas déjà le cas (résumé)

Trois trous (cf. diagnostic) : (1) aucune identité/ marqueur sparse → indétectable ;
(2) pas d'export du modèle sparse en CI ; (3) aucun job n'appelait `--sparse-only`. Les
points (1) et la détection sont désormais corrigés côté code ; le point (2) a un gate
`WITH_SPARSE` ; il reste à câbler le job GPU corpus-scale + le self-heal du bake et à
exécuter la production (compute GPU multi-sessions).
