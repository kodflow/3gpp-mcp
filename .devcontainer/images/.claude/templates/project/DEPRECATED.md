# Deprecated: Project Templates

The `.tpl` files in this directory are **deprecated** as of the `/init` conversational discovery redesign.

## What Changed

The `/init` command previously used these mustache-style templates (`{{VARIABLE}}`) to generate project files from discrete multiple-choice answers. The new `/init` uses open-ended conversation to build rich context, then synthesizes documents directly — producing higher quality, project-specific output.

## Affected Files

| Template | Replaced By |
|----------|-------------|
| `CLAUDE.md.tpl` | Direct generation in Phase 3 |
| `vision.md.tpl` | Direct generation in Phase 3 |
| `architecture.md.tpl` | Direct generation in Phase 3 |
| `workflows.md.tpl` | Direct generation in Phase 3 |
| `env.example.tpl` | Conditional direct generation |
| `Makefile.tpl` | Conditional direct generation |

## Why Not Deleted

Per project safeguards, template files are preserved for rollback if needed. They are no longer referenced by any command.
