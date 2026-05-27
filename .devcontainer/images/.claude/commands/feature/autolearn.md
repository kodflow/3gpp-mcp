# Journal Actions Reference

| Action | Trigger | Fields |
|--------|---------|--------|
| `created` | --add | detail |
| `modified` | --edit | detail, files? |
| `status_change` | --edit --status | detail (from → to) |
| `checkup_pass` | --checkup | detail |
| `checkup_fail` | --checkup | detail |
| `auto_corrected` | --checkup wave correction | detail (parent ID) |
| `plan_generated` | --checkup auto-plan | detail |
| `compacted` | Journal > 50 entries | detail (N events) |

---

## Journal Compaction

```yaml
compaction:
  trigger: "journal.length > 50 for any feature"
  action: |
    Keep last 50 entries.
    Summarize removed entries into:
      { ts: oldest_ts, action: "compacted", detail: "{N} events compacted" }
    Insert compacted entry at index 0.
  result: "Journal always has <= 51 entries (1 compacted + 50 recent)"
```

---

## Schema Reference (v2)

```json
{
  "version": 2,
  "features": [
    {
      "id": "F001",
      "title": "Short title (< 80 chars)",
      "description": "Detailed description of the feature",
      "status": "pending|in_progress|completed|blocked|archived",
      "tags": ["tag1", "tag2"],
      "level": 0,
      "workdirs": ["src/domain/", "src/infrastructure/"],
      "audit_dirs": ["src/"],
      "created": "ISO 8601",
      "updated": "ISO 8601",
      "journal": [
        {
          "ts": "ISO 8601",
          "action": "created|modified|status_change|checkup_pass|checkup_fail|auto_corrected|plan_generated|compacted",
          "detail": "Description of what happened",
          "files": ["optional/array/of/paths.ext"]
        }
      ]
    }
  ]
}
```

**v2 fields:**
- `level` (int, default 0): Hierarchy depth. 0 = root, 1 = subsystem, 2+ = component.
- `workdirs` (string[]): Directories this feature owns. Trailing `/` required.
- `audit_dirs` (string[]): Directories this feature can audit. Used for parent inference.

---

## Integration

| Skill | Integration |
|-------|-------------|
| `/init` | Propose --add for discovered features |
| `/warmup` | Load features.json into context |
| `/plan` | Reference features in plan context |
