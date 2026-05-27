# Agents IA utilisés par 3gpp-mcp

> Ce fichier liste **uniquement** les agents et skills réellement employés
> par ce projet. Le catalogue exhaustif (79 agents) vit dans
> `~/.claude/agents/` et est documenté à part — ne pas le redupliquer ici.

## Workflow par défaut

| Étape | Skill / agent | Quand l'invoquer |
|---|---|---|
| Charger le contexte | `/warmup` | Début de session |
| Planifier une feature | `/plan` | Avant tout changement non trivial |
| Exécuter le plan | `/do` | Après revue du plan |
| Spécialiste Go | `developer-specialist-go` | Implémentation, refacto, idiomatique |
| Code review | `/review` (T1, 5 sub-agents) | Avant merge request |
| Linting bloquant | `/lint` (ktn-linter) | Hooks PostToolUse, CI |
| Commits + MR | `/git --commit` puis `/git --pr` | Sortie de feature |
| Recherche docs | `/search` | Standards 3GPP, libs Go |
| Secrets | `/secret` | Lecture du vault `halys/3gpp-mcp` |

## Règles propres au projet

1. **Go uniquement** — toute proposition de code Python dans `cmd/` ou
   `internal/` doit être rejetée (cf. `CLAUDE.md` §13).
2. Le specialist `developer-specialist-go` consulte `context7` pour la
   doc à jour (mark3labs/mcp-go, marcboeker/go-duckdb, kuzudb/go-kuzu,
   yalue/onnxruntime_go) avant d'écrire du code non trivial.
3. Aucun `//nolint` sans commentaire `nolint:rule // reason`.
4. Les agents `devops-*` ne sont pas mobilisés en V1 (pas d'infra cloud).
5. `developer-executor-security` a la priorité sur tout PR touchant le
   parsing DOCX / scraping FTP (vecteur d'attaque externe).

## Modèles attendus

| Tâche | Modèle |
|---|---|
| Plan / orchestration | Opus |
| Implémentation Go, review | Sonnet |
| OS / shell / DOCX leaf tasks | Haiku |

## Routing pertinent

```text
/review → developer-specialist-review (sonnet)
            ├─ developer-executor-correctness (sonnet)
            ├─ developer-executor-security    (opus)   ← critique pour ingest
            ├─ developer-executor-design      (sonnet)
            ├─ developer-executor-quality     (haiku)
            └─ developer-executor-shell       (haiku)

/plan, /do → developer-orchestrator (opus)
              └─ developer-specialist-go (sonnet)
```

Pour la liste complète des agents disponibles dans l'environnement
devcontainer, voir `~/.claude/agents/registry.json`.
