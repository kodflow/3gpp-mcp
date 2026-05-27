---
name: agent-git-stash-destruction
category: learned
extracted: 2026-04-26T11:00:00+02:00
confidence: 0.98
trigger: "Avant de spawner des agents avec accès Bash sur un repo git"
source: session — 6h de travail perdu sur ktn-linter fix/linter-feedback
---
# Agents With Full Bash Access Destroy Git Working Tree via git stash

## Problem

Quand des agents `general-purpose` (avec `Bash` complet) travaillent en parallèle sur un repo git,
ils tentent souvent de revenir en arrière sur leurs propres erreurs via `git stash`, `git checkout`,
ou `git reset`. Ces opérations détruisent le travail des autres agents ET leur propre travail.

Symptômes dans le reflog :

```
56ff8ebe HEAD@{...}: reset: moving to HEAD   ← signature de git stash
56ff8ebe HEAD@{...}: reset: moving to HEAD   ← idem, 17 fois en 6h
```

Dangling commits WIP dans `git fsck --dangling` = preuve que des stashes ont été créés :

```
dangling commit 91e7c996...  WIP on fix/linter-feedback: 56ff8ebe
dangling commit f6171cc6...  WIP on fix/linter-feedback: 56ff8ebe
```

Ces stashes sont souvent **incomplets** (untracked new files absents) → irrécupérables.

**Résultat** : 6h de travail de 5 vagues × 10 agents (775 → 0 violations) PERDU en une nuit.

## Solution

### 1. Interdiction explicite dans chaque prompt agent (OBLIGATOIRE)

```markdown
## GIT OPERATIONS — STRICTLY FORBIDDEN
- NEVER run: git stash, git stash pop, git stash apply
- NEVER run: git reset (any form)
- NEVER run: git checkout -- <file> (to revert files)
- NEVER run: git restore
- IF your edit is wrong: use Edit/Write tools to fix it directly — NEVER revert via git
- IF you need to investigate: READ files, do NOT checkout old versions
```

### 2. Commit après chaque wave réussie (OBLIGATOIRE)

Après chaque vague d'agents, avant de lancer la suivante :

```bash
go build ./... && go test ./... && ktn-linter prompt ./...
# Si OK :
git add -A && git commit -m "fix(linter): wave N — X violations fixed"
```

Un commit protège le travail des stashes accidentels futurs.

### 3. Utiliser la restriction de scope via isolation="worktree" (RECOMMANDÉ)

```python
Agent(
    description="Fix pkg/foo",
    isolation="worktree",  # worktree propre, pas de pollution du repo principal
    ...
)
```

Chaque agent dans son worktree ne peut pas toucher le worktree principal.

### 4. Ne jamais utiliser `general-purpose` pour des fix multi-fichiers sans whitelist git

```
BAD:  subagent_type: "general-purpose"  # a tous les outils dont git
GOOD: subagent_type: "developer-specialist-go"  # Read/Glob/Grep/Bash/WebFetch seulement
                                                  # pas d'Edit/Write → orchestrateur applique
```

Ou utiliser `general-purpose` avec le prompt GIT FORBIDDEN ci-dessus.

## Exemple de prompt agent sûr

```markdown
# Task: Fix KTN violations in pkg/foo/

## GIT OPERATIONS — STRICTLY FORBIDDEN
- NEVER: git stash, git reset, git checkout --, git restore
- To undo a bad edit: use Edit/Write to correct directly

## SCOPE
- ONLY edit files in /workspace/pkg/foo/

## VERIFICATION
1. go build ./pkg/foo/...
2. go test ./pkg/foo/...
3. ./builds/ktn-linter lint ./pkg/foo/... → 0 warnings
```

## When to Use

**TOUJOURS** avant de spawner des agents qui font des modifications de code sur un repo git :

- Multi-agent waves (10 agents en parallèle)
- Agents long-running (>5 min)
- Sessions "je vais me coucher" / autonomous mode

## Evidence

Session ktn-linter 2026-04-26 :

- 5 waves × 10 agents, 775 → 0 violations atteint
- Agent validate-fix (22 min, 187 tool_uses) a utilisé git pour investigation
- 17 `reset: moving to HEAD` dans reflog = 17 stash operations
- Dangling commits WIP récupérables mais INCOMPLETS (untracked files absents)
- Travail irrécupérable, retour à l'état initial (775 violations)
