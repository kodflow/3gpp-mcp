#!/bin/bash
# Tests for .devcontainer/images/scripts/merge-devcontainer-json.mjs

set +e

SCRIPT="/workspace/.devcontainer/images/scripts/merge-devcontainer-json.mjs"
if [ ! -f "$SCRIPT" ]; then
    echo "FATAL: merge script not found at $SCRIPT"
    exit 1
fi
if ! command -v node >/dev/null 2>&1; then
    echo "SKIP: node not available"
    exit 0
fi

PASS=0
FAIL=0
FAILED_TESTS=()

pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); FAILED_TESTS+=("$1"); }

TMPDIR_T=$(mktemp -d)
trap 'rm -rf "$TMPDIR_T"' EXIT

run_merge() {
    local tmpl="$TMPDIR_T/template.json"
    local over="$TMPDIR_T/override.json"
    local out="$TMPDIR_T/out.json"
    printf '%s' "$1" > "$tmpl"
    printf '%s' "$2" > "$over"
    node "$SCRIPT" "$tmpl" "$over" "$out" 2>&1
    cat "$out" 2>/dev/null
}

test_jsonc_comments_stripped() {
    echo "=== jsonc-comments-stripped ==="
    local tmpl='{
  // line comment
  /* block
     comment */
  "name": "base",
  "features": {
    // "ghcr.io/x/go:1": {}
  }
}'
    local over='{}'
    local out
    out=$(run_merge "$tmpl" "$over")
    if echo "$out" | jq -e '.name == "base"' >/dev/null 2>&1; then
        pass "comments stripped, JSON parseable"
    else
        fail "comments stripped (got: $out)"
    fi
}

test_url_double_slash_preserved() {
    echo "=== url-double-slash-preserved ==="
    local tmpl='{"url": "https://example.com/path"}'
    local over='{}'
    local out
    out=$(run_merge "$tmpl" "$over")
    if echo "$out" | jq -e '.url == "https://example.com/path"' >/dev/null 2>&1; then
        pass "URL with // preserved inside string"
    else
        fail "URL mangled (got: $out)"
    fi
}

test_trailing_commas_tolerated() {
    echo "=== trailing-commas-tolerated ==="
    local tmpl='{"a": 1, "b": [1, 2,], "c": {"x": 1,},}'
    local over='{}'
    local out
    out=$(run_merge "$tmpl" "$over")
    if echo "$out" | jq -e '.a == 1 and .b[1] == 2 and .c.x == 1' >/dev/null 2>&1; then
        pass "trailing commas accepted"
    else
        fail "trailing commas (got: $out)"
    fi
}

test_deep_merge_objects() {
    echo "=== deep-merge-objects ==="
    local tmpl='{"features": {"ghcr.io/x/node:1": {}, "ghcr.io/x/k8s:1": {}}}'
    local over='{"features": {"ghcr.io/x/go:1": {}, "ghcr.io/x/python:1": {"version": "3.12"}}}'
    local out
    out=$(run_merge "$tmpl" "$over")
    local node go py_ver
    node=$(echo "$out" | jq -r '.features."ghcr.io/x/node:1"')
    go=$(echo "$out" | jq -r '.features."ghcr.io/x/go:1"')
    py_ver=$(echo "$out" | jq -r '.features."ghcr.io/x/python:1".version')
    if [ "$node" = "{}" ] && [ "$go" = "{}" ] && [ "$py_ver" = "3.12" ]; then
        pass "deep merge: template feature kept + override features added with options"
    else
        fail "deep merge (node=$node go=$go py=$py_ver)"
    fi
}

test_arrays_replaced_not_concatenated() {
    echo "=== arrays-replaced ==="
    local tmpl='{"forwardPorts": [80, 443]}'
    local over='{"forwardPorts": [3000]}'
    local out
    out=$(run_merge "$tmpl" "$over")
    local len first
    len=$(echo "$out" | jq '.forwardPorts | length')
    first=$(echo "$out" | jq '.forwardPorts[0]')
    if [ "$len" = "1" ] && [ "$first" = "3000" ]; then
        pass "arrays replaced (override wins)"
    else
        fail "arrays should replace (len=$len first=$first)"
    fi
}

test_override_wins_on_scalars() {
    echo "=== override-wins-scalars ==="
    local tmpl='{"remoteUser": "vscode", "shutdownAction": "none"}'
    local over='{"remoteUser": "root"}'
    local out
    out=$(run_merge "$tmpl" "$over")
    local user shut
    user=$(echo "$out" | jq -r .remoteUser)
    shut=$(echo "$out" | jq -r .shutdownAction)
    if [ "$user" = "root" ] && [ "$shut" = "none" ]; then
        pass "override wins on scalars, template kept where absent"
    else
        fail "scalar merge (user=$user shut=$shut)"
    fi
}

test_missing_args_fails() {
    echo "=== missing-args-fails ==="
    if node "$SCRIPT" 2>/dev/null; then
        fail "should fail without args"
    else
        pass "exits non-zero without args"
    fi
}

test_nested_vscode_settings() {
    echo "=== nested-vscode-settings ==="
    local tmpl='{"customizations": {"vscode": {"settings": {"editor.formatOnSave": true}, "extensions": ["a"]}}}'
    local over='{"customizations": {"vscode": {"settings": {"editor.tabSize": 2}}}}'
    local out
    out=$(run_merge "$tmpl" "$over")
    local fos ts ext
    fos=$(echo "$out" | jq -r '.customizations.vscode.settings."editor.formatOnSave"')
    ts=$(echo "$out" | jq -r '.customizations.vscode.settings."editor.tabSize"')
    ext=$(echo "$out" | jq -r '.customizations.vscode.extensions[0]')
    if [ "$fos" = "true" ] && [ "$ts" = "2" ] && [ "$ext" = "a" ]; then
        pass "nested merge: template settings kept, override settings added, extensions array preserved when not overridden"
    else
        fail "nested merge (fos=$fos ts=$ts ext=$ext)"
    fi
}

echo "═══════════════════════════════════════════════"
echo "  merge-devcontainer-json — Unit Tests"
echo "═══════════════════════════════════════════════"

test_jsonc_comments_stripped
test_url_double_slash_preserved
test_trailing_commas_tolerated
test_deep_merge_objects
test_arrays_replaced_not_concatenated
test_override_wins_on_scalars
test_missing_args_fails
test_nested_vscode_settings

echo ""
echo "═══════════════════════════════════════════════"
TOTAL=$((PASS + FAIL))
echo "  Results: $PASS/$TOTAL passed ($FAIL failed)"
if [ "$FAIL" -gt 0 ]; then
    echo "  Failed tests:"
    for t in "${FAILED_TESTS[@]}"; do
        echo "    - $t"
    done
fi
echo "═══════════════════════════════════════════════"

exit $FAIL
