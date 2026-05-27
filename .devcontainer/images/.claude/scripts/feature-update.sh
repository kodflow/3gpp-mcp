#!/bin/bash
# feature-update.sh — Auto-learn: associate edits with features
# Trigger: PostToolUse (Write|Edit), async
# Output: additionalContext suggesting feature journal update

set -euo pipefail

INPUT=$(cat)
FILE=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // ""')
FEATURES_DB="/workspace/.claude/features.json"

# Skip if no file edited or no features.json
[ -z "$FILE" ] && exit 0
[ -f "$FEATURES_DB" ] || exit 0

# Find features whose journal mentions this file
MATCHING_JOURNAL=$(jq -r --arg f "$FILE" '
  .features[] |
  select(.status != "archived") |
  select(.journal[]? | .files[]? == $f) |
  "\(.id): \(.title)"
' "$FEATURES_DB" 2>/dev/null) || true

# Find features whose workdirs match this file (v2 schema)
MATCHING_WORKDIRS=$(jq -r --arg f "$FILE" '
  .features[] | select(.status != "archived") |
  select(.workdirs[]? as $wd | ($f | startswith($wd))) |
  "\(.id): \(.title)"
' "$FEATURES_DB" 2>/dev/null) || true

# Combine and deduplicate matches
MATCHING=$(printf '%s\n%s' "$MATCHING_JOURNAL" "$MATCHING_WORKDIRS" | sort -u | sed '/^$/d')

if [ -n "$MATCHING" ]; then
    CONTEXT="File $FILE is linked to feature(s): $MATCHING. Consider updating the feature journal with /feature --edit after completing this change."
    jq -n -c --arg ctx "$CONTEXT" \
        '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":$ctx}}'
fi
