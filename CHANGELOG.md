# Changelog

All notable changes to this project are documented here.

## [Unreleased] — Skills Architecture v1.5 (2026-05-20)

### Changed — v1.5 patch on top of v1.4

- `/refine` directive char-cap is now **uniformly 4000 chars** (the
  actual `/goal` tool limit, not 4096 — corrected from v1.4).
- The cap is a **target**, not a floor: natural output may be shorter
  when content warrants it; the skill never pads to hit 4000.
- Dual budget removed (no more LIGHT 2000 / FULL 4096 split). LIGHT vs
  FULL now affects only **lens depth** (4 critical vs all 10), never
  char-cap.
- `/refine` now **auto-detects mode** from argument shape + disk state.
  Explicit `--bare` / `--from-contract` flags become **overrides** for
  edge cases, not the primary entry point:
  - `/refine "free-form text"` → auto BARE (arg has spaces)
  - `/refine my-slug` (with plan+context on disk) → auto FULL
  - `/refine my-slug` (only goal on disk) → auto FROM-CONTRACT
  - `/refine inexistant-slug` → BARE (slug treated as description)
- `--lenses light|full` replaces `--light` / `--full` for FULL mode
  lens-depth override.

## [Unreleased] — Skills Architecture v1.4 (2026-05-20) — superseded by v1.5

### Changed — v1.4 patch on top of v1.3

- `/refine` gains three input modes (initial design used explicit
  `--bare` / `--from-contract` flags; v1.5 supersedes with auto-detection).
- Budget logic moves to single source of truth in
  `refine/synthesis.md`; BARE and FROM-CONTRACT reuse the same
  compact step as FULL.
- The "standalone /goal" use case ships without bringing back the
  deprecated `/prompt` skill — the migration doc remains valid.

## [Unreleased] — Skills Architecture v1.3 (2026-05-20)

### Added

- `/refine` skill: 10-lens goal-contract generator with AUTO mode,
  static lens fallback (router-independent for critical lenses), and
  Markdown-frontmatter-aware metadata parser.
- `route-agent.sh`: router that resolves `(skill, phase, profile)` to a
  concrete `(subagent_type, model, effort)` dispatch via
  `routing-table.jsonl`. Supports `agent_template` + `expand_from` for
  per-language fanout.
- `goal-state.sh`: lifecycle CRUD on `.claude/state/goals/<slug>.json`
  (create/read/update/mark-stale/gc) enabling `/do --goal-turn`.
- `probe-primitives.sh`: emits `.claude/state/primitives.json` with
  presence and `ExitPlanMode` schema for the 16 primitives the
  initiative depends on.
- `frontmatter.sh`: helper extracting YAML frontmatter from `.md` files
  before `yq` evaluation (fixes the v1.2 bug where `yq` was invoked on
  the full Markdown body).
- 5 new specialist agents: `developer-specialist-react`,
  `data-specialist-postgres`, `developer-specialist-playwright`,
  `devops-specialist-cloudflare`, `tooling-specialist-github-actions`
  (86 agents total, up from 81).
- 6 new facets in `detect-project.sh`: `cloud[]`, `container[]`, `k8s`,
  `os`, `ci`, `test_frameworks[]`.
- `agent-drift-patterns.md` + `migrated_skills.txt` + `routing-table.jsonl`.
- `primitives-compat.md`: documented fallback policy per primitive.

### Changed

- `/plan` Phase 6.0 now invokes `ExitPlanMode(plan=<full md>)` with
  schema validation against `.claude/state/primitives.json`.
- `/plan` gains `--goal` flag — chains into `/refine` via `Skill`.
- `/git --merge` uses `mcp__github__merge_pull_request` (no
  `gh pr merge` fallback).
- `/git --watch` prefers `Monitor` over `sleep(60)` polling.
- `/do` adds `--goal-turn <slug>` flag and `Skill(*)` allowed-tool.
- `/do` loop emits `PushNotification` on terminal state.
- `/ktn` dispatches `devops-executor-linux` instead of `general-purpose`.
- `/search` parallel mode routes per-language specialists via
  `agent_template` instead of generic `Explore`.
- `/warmup` scan/read use `docs-analyzer-*` specialists.
- `/review --loop`, `/git --commit`, `/init`, `/search` now use real
  `Skill(...)` recursive calls instead of magic-string `/X` mentions
  (cycle detection capped at depth 5).
- `registry.json` counts: 79 → 86 agents, distribution opus 3→4,
  sonnet 32→38, haiku 46→39.
- `AGENTS.md` header: 79 → 86.

### Removed

- `/prompt` skill removed (`.devcontainer/images/.claude/commands/prompt.md`).
  Use `/refine` instead — see
  `.devcontainer/images/.claude/docs/migrations/prompt-to-refine.md`.
