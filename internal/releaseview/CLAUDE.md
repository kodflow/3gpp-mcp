<!-- updated: 2026-05-30T08:38:41Z -->
# internal/releaseview — Release-Scoped Clause Views

## Purpose

Computes release-scoped views over clause availability: what a spec/clause
contains in a baseline release, what that release introduced vs the previous one,
and — as an annex the caller surfaces — what LATER releases add or what is
already obsolete. Generic (any spec), used by the core `get_spec` tool and reused
by domain subjects.

## Structure

```text
releaseview/
└── releaseview.go   # ReleaseView: baseline + introduced/obsolete/later annex
```

## Conventions

- Lives **outside** any vertical so the core never depends on a subject package
  (import-rule discipline, see `internal/CLAUDE.md`).
- Read-only over `internal/store`; pure computation on clause sets.
- "Frozen ≠ stable" (CLAUDE.md §8 #8): a release keeps gaining corrections after
  freeze — views distinguish drafts (`xx.0.0`) from stable versions.
