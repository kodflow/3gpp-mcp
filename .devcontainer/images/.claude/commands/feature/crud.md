# Phase 1: Init + Migration

**Always runs first. Ensure `.claude/features.json` exists and is v2.**

```yaml
init:
  check: "Read .claude/features.json"
  if_missing:
    action: |
      Write .claude/features.json with:
      {
        "version": 2,
        "features": []
      }
    message: "Created .claude/features.json (schema v2)"

  if_exists:
    read_version: "Parse .version from JSON"

    if_version_1:
      action: |
        FOR each feature in .features[]:
          ADD fields: "level": 0, "workdirs": [], "audit_dirs": []
          APPEND to journal:
            { "ts": now, "action": "modified", "detail": "Migrated to schema v2: added level, workdirs, audit_dirs" }
        SET .version = 2
        Write updated .claude/features.json
      message: "Migrated features.json from v1 → v2 ({n} features updated)"

    if_version_2:
      action: "Load into working memory"
```

---

# Phase 2: CRUD Operations

## --add "title" --desc "description" [--level N] [--workdirs "..."] [--audit-dirs "..."]

```yaml
add_feature:
  1_generate_id:
    action: "Auto-increment: find max existing ID number, +1"
    format: "F001, F002, F003, ..."

  2_parse_args:
    level: "from --level (default 0, integer >= 0)"
    workdirs: "from --workdirs (comma-separated, normalize trailing /). Prompted if missing."
    audit_dirs: "from --audit-dirs (comma-separated, default = workdirs)"
    validation: |
      IF --workdirs missing: ask user
      Normalize: ensure each dir ends with /
      IF level > 5: ERROR "Level must be <= 5" → reject input
      IF workdirs empty after prompt: ERROR "workdirs required" → reject input

  3_create_entry:
    fields:
      id: "{generated_id}"
      title: "{from --add argument}"
      description: "{from --desc argument, or ask user}"
      status: "pending"
      tags: "[]  # Ask user for optional tags"
      level: "{parsed level}"
      workdirs: "[parsed workdirs array]"
      audit_dirs: "[parsed audit_dirs array]"
      created: "{ISO 8601 now}"
      updated: "{ISO 8601 now}"
      journal:
        - ts: "{ISO 8601 now}"
          action: "created"
          detail: "Initial feature definition"

  4_write:
    action: "Edit .claude/features.json, append to features array"

  5_infer_parent:
    action: "Run infer_hierarchy, find parent for this feature"
    output: "Parent info (if level > 0 and parent found)"

  6_output:
    format: |
      ═══════════════════════════════════════════════════════════════
        Feature Created
      ═══════════════════════════════════════════════════════════════
        ID       : {id}
        Title    : {title}
        Level    : {level}
        Workdirs : {workdirs}
        Parent   : {parent_id}: {parent_title} (or "none / root")
        Status   : pending
      ═══════════════════════════════════════════════════════════════
```

## --edit \<id\> [--title "..."] [--desc "..."] [--status ...] [--tags ...] [--level N] [--workdirs "..."] [--audit-dirs "..."]

```yaml
edit_feature:
  1_find: "Locate feature by ID in features array"
  2_update: |
    Update only specified fields.
    For --level: integer >= 0. Warn if > 5.
    For --workdirs/--audit-dirs: comma-separated, normalize trailing /.
  3_journal: |
    Append entry:
      { ts: now, action: "modified", detail: "Updated {changed_fields}" }
    If --status changed:
      { ts: now, action: "status_change", detail: "from → to" }
  4_compact: |
    If journal has > 50 entries:
      Summarize oldest entries into single "compacted" entry
  5_write: "Save updated features.json"
```

## --del \<id\>

```yaml
delete_feature:
  1_find: "Locate feature by ID"
  2_check_children:
    action: "Run infer_hierarchy, find children of this feature"
    if_has_children:
      warn: "Feature {id} has {n} inferred children: {child_ids}"
      extra_option: "Cascade archive children"
  3_confirm:
    tool: AskUserQuestion
    question: "Delete feature {id}: {title}? This cannot be undone."
    options:
      - label: "Yes, delete"
        description: "Permanently remove this feature"
      - label: "Archive instead"
        description: "Set status to archived (recoverable)"
      - label: "Cascade archive (with children)"
        description: "Archive this feature and all inferred children"
        condition: "Only shown if has_children"
  4_execute:
    if_delete: "Remove from features array"
    if_archive: "Set status to archived, add journal entry"
    if_cascade: |
      Set this feature + all inferred children to archived.
      Add journal entry to each: { action: "status_change", detail: "Cascade archived via parent {id}" }
  5_write: "Save updated features.json"
```

## --list

```yaml
list_features:
  1_load: "Read features.json"
  2_infer: "Run infer_hierarchy to build parent-child tree"
  3_display:
    action: "Render indented hierarchy tree"
    format: |
      ═══════════════════════════════════════════════════════════════
        /feature --list ({n} features)
      ═══════════════════════════════════════════════════════════════

        F001  [L0] DDD Architecture       | completed   | src/
        ├─ F002  [L1] HTTP Server         | in_progress | src/api/
        │  └─ F004  [L2] Auth middleware  | pending     | src/api/auth/
        └─ F003  [L1] Database layer      | completed   | src/models/
        F005  [L0] CI/CD Pipeline         | in_progress | .github/

        Orphans (no parent found):
          ⚠ F006  [L1] Logging            | pending     | lib/log/

      ═══════════════════════════════════════════════════════════════

    tree_algorithm: |
      1. Group features by level (0, 1, 2, ...)
      2. For each root (level 0): render, then recurse children
      3. Use tree connectors: ├─ (mid), └─ (last), │ (vertical)
      4. Show [LN] tag for level
      5. Show first workdir as path indicator
      6. Orphans (level > 0 with no parent) listed separately with ⚠
```

## --show \<id\>

```yaml
show_feature:
  action: "Display full feature detail + hierarchy + journal"
  steps:
    1_load: "Read feature by ID"
    2_infer: "Run infer_hierarchy for parent/children context"
  format: |
    ═══════════════════════════════════════════════════════════════
      Feature {id}: {title}
    ═══════════════════════════════════════════════════════════════

      Status      : {status}
      Level       : {level}
      Tags        : {tags}
      Workdirs    : {workdirs}
      Audit dirs  : {audit_dirs}
      Created     : {created}
      Updated     : {updated}

      Description:
        {description}

      Hierarchy:
        Parent   : {parent_id}: {parent_title} (or "none / root")
        Children : {child_id}: {child_title}, ... (or "none")

      Journal ({n} entries):
        {ts} | {action} | {detail}
        {ts} | {action} | {detail} | files: {files}
        ...

    ═══════════════════════════════════════════════════════════════
```

---

## Hierarchy Inference (Runtime, No Storage)

**Reusable algorithm referenced by `--list`, `--show`, `--checkup`, `--add`, `--del`.**

```yaml
infer_hierarchy:
  input: "features[] from features.json (status != archived)"
  output: "{ parent_map: {child_id → parent_id}, children_map: {parent_id → [child_ids]} }"

  algorithm: |
    1. Sort features by level ascending
    2. FOR each feature F at level N > 0:
       a. Candidates = features at level N-1 whose audit_dirs overlap F.workdirs
          Overlap test: any audit_dir of candidate is a prefix of any workdir of F
       b. IF multiple candidates: pick longest prefix match
       c. IF no candidate: F is orphan (emit warning)
       d. Record parent_map[F.id] = candidate.id
    3. Invert parent_map → children_map

  examples:
    - F001 (L0, audit_dirs=["src/"]) + F002 (L1, workdirs=["src/api/"])
      → F002 is child of F001 ("src/" is prefix of "src/api/")
    - F001 (L0, audit_dirs=["src/"]) + F003 (L0, audit_dirs=["tests/"])
      → No relationship (same level)
    - F004 (L2, workdirs=["lib/log/"]) with no L1 whose audit_dirs cover "lib/log/"
      → F004 is orphan

  constraints:
    - Level 0 features are always roots (never have parents)
    - Relationships are inferred, NEVER stored in features.json
    - Corrections flow DOWNWARD only (parent → child, never child → parent)
```
