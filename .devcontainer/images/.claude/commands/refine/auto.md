# refine/auto.md — Skills Architecture v1.5

> **Scope note**: AUTO mode controls **lens depth only** (4 critical vs
> all 10). It does NOT control the char-cap — that's always 4000 in
> every mode. `--bare` and `--from-contract` skip lens analysis
> entirely, so this phase doesn't apply to them.

## Decision tree

```bash
# Source the frontmatter helper (handles .md-with-YAML correctly)
source ~/.claude/scripts/frontmatter.sh

auto_select_lens_depth() {
  local plan_path="$1"

  # WHY frontmatter_get_or and not the bare frontmatter_get: yq/jq's
  # `// "missing"` operator treats `false` as missing, which would
  # coerce a real touches_*: false value into the sentinel and force
  # FULL mode on every plan. The _or helper uses has() so a
  # present-but-false value stays false.
  local risk loc pub_api sec devinf
  risk=$(frontmatter_get_or    "$plan_path" risk                      missing)
  loc=$(frontmatter_get_or     "$plan_path" loc_estimate_max          -1)
  pub_api=$(frontmatter_get_or "$plan_path" touches_public_api        missing)
  sec=$(frontmatter_get_or     "$plan_path" touches_security_surface  missing)
  devinf=$(frontmatter_get_or  "$plan_path" touches_dev_infra         missing)

  # Missing / non-numeric metadata → full lens depth (safe default)
  for field in "$risk" "$pub_api" "$sec" "$devinf"; do
    [ "$field" = "missing" ] && { echo "full"; return; }
  done
  case "$loc" in
    ''|*[!0-9]*) echo "full"; return ;;
  esac

  # Light criteria (4 critical lenses): ALL must hold
  #   loc_estimate_max ≤ 500
  #   risk ∈ {low, medium}
  #   touches_public_api == false
  #   touches_security_surface == false
  #   touches_dev_infra == false
  if [ "$loc" -le 500 ] \
     && { [ "$risk" = "low" ] || [ "$risk" = "medium" ]; } \
     && [ "$pub_api" = "false" ] \
     && [ "$sec" = "false" ] \
     && [ "$devinf" = "false" ]; then
    echo "light"
  else
    echo "full"
  fi
}
```

## Boundaries (locked by tests)

| `loc_estimate_max` | `risk` | `touches_*` | Lens depth |
|---|---|---|---|
| `≤ 500` | `low`/`medium` | all `false` | **light** (4 critical lenses) |
| `501+` | any | any | **full** (all 10 lenses) |
| any | `high`/`critical` | any | **full** |
| any | any | any `true` | **full** |
| missing | any | any | **full** |
| non-numeric | any | any | **full** |

The directive char-cap is **4000 in every row** — see `synthesis.md`.
