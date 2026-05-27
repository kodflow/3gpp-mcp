#!/usr/bin/env bash
# frontmatter.sh — Skills Architecture v1.3 (PR3)
#
# WHY: /refine plans are Markdown with YAML frontmatter, so calling
# `yq` on the entire .md file is wrong (yq would try to parse the body
# as YAML). These helpers extract the frontmatter block first, then
# pipe it through `yq` for actual key access.

# Print the YAML block between the leading --- and the next ---.
# Output is empty if the file has no frontmatter.
extract_frontmatter() {
  awk '
    BEGIN { in_fm = 0 }
    NR == 1 && $0 == "---" { in_fm = 1; next }
    in_fm && $0 == "---" { exit }
    in_fm { print }
  ' "$1"
}

# Evaluate a yq expression against the file's frontmatter.
# Returns empty string when frontmatter is absent or `yq` fails.
frontmatter_get() {
  local file="$1"
  local expr="$2"
  local fm
  fm="$(extract_frontmatter "$file")"
  [[ -z "$fm" ]] && { echo ""; return; }
  if command -v yq >/dev/null 2>&1; then
    echo "$fm" | yq "$expr" 2>/dev/null
  else
    # Fallback when yq is missing: best-effort grep for `key: value`.
    # Only supports `.key` style expressions.
    local key="${expr#.}"
    echo "$fm" | grep -E "^${key}:" | head -1 | sed -E "s/^${key}:[[:space:]]*//;s/[[:space:]]*$//"
  fi
}

# Get a frontmatter key or return a sentinel if the key is absent.
# WHY: both yq's and jq's `//` operator treat `false` as missing, which
# breaks the AUTO-mode lookup of `touches_*: false` (the value is
# coerced to the "missing" sentinel and triggers a false default-to-FULL).
# This helper bypasses `//` and uses `has()` for an unambiguous existence
# check, so a real `false` stays `false`.
#
# Usage: frontmatter_get_or <file> <key> <sentinel>
#   <file>     path to a Markdown file with YAML frontmatter
#   <key>      top-level frontmatter key (no leading dot, no jq syntax)
#   <sentinel> string to emit when the key is genuinely absent
frontmatter_get_or() {
  local file="$1" key="$2" default="$3"
  local fm
  fm="$(extract_frontmatter "$file")"
  [[ -z "$fm" ]] && { echo "$default"; return; }
  if command -v yq >/dev/null 2>&1 && command -v jq >/dev/null 2>&1; then
    local json
    json="$(echo "$fm" | yq -o=json 2>/dev/null)"
    if [[ -z "$json" ]]; then
      echo "$default"; return
    fi
    echo "$json" | jq -r --arg k "$key" --arg d "$default" \
      'if has($k) then .[$k] else $d end' 2>/dev/null \
      || echo "$default"
  else
    local line
    line=$(echo "$fm" | grep -E "^${key}:" | head -1)
    if [[ -z "$line" ]]; then
      echo "$default"
    else
      echo "$line" | sed -E "s/^${key}:[[:space:]]*//;s/[[:space:]]*$//"
    fi
  fi
}

# CLI entry point for shell scripts that prefer subprocess access.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-}" in
    extract) extract_frontmatter "$2" ;;
    get)     frontmatter_get "$2" "$3" ;;
    -h|--help|*)
      cat <<USAGE
frontmatter.sh extract <file>      # print YAML frontmatter block
frontmatter.sh get <file> <expr>   # evaluate yq expression on frontmatter
USAGE
      ;;
  esac
fi
