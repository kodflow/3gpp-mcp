#!/usr/bin/env bats
# Tests for build-features-manifest.py (schema v2, --prev-manifest support).

setup() {
    load '../helpers/setup'
    common_setup
    BUILDER="${BATS_TEST_DIRNAME}/../../.devcontainer/scripts/build-features-manifest.py"
    SRC="$TEST_TMPDIR/features"
    mkdir -p "$SRC"
}

teardown() {
    common_teardown
}

@test "v2: schema version is 2 and files map populated" {
    echo a > "$SRC/a.md"
    run python3 "$BUILDER" "$SRC" "sha-1" "2026-05-20T00:00:00Z"
    [ "$status" -eq 0 ]
    echo "$output" | jq -e '.version == 2'
    echo "$output" | jq -e '.files["a.md"] | startswith("sha256:")'
    echo "$output" | jq -e '.previous_hashes == {}'
}

@test "v2: no --prev-manifest → previous_hashes empty" {
    echo a > "$SRC/a.md"
    run python3 "$BUILDER" "$SRC" "sha-1" "2026-05-20T00:00:00Z"
    [ "$status" -eq 0 ]
    echo "$output" | jq -e '.previous_hashes == {}'
}

@test "v2: --prev-manifest from v1-style input → previous_hashes carries prev hash" {
    echo a-v1 > "$SRC/a.md"
    python3 "$BUILDER" "$SRC" "sha-v1" "2026-05-01T00:00:00Z" > "$TEST_TMPDIR/prev.json"
    echo a-v2 > "$SRC/a.md"
    run python3 "$BUILDER" "$SRC" "sha-v2" "2026-05-20T00:00:00Z" \
        --prev-manifest "$TEST_TMPDIR/prev.json"
    [ "$status" -eq 0 ]
    echo "$output" | jq -e '.previous_hashes["a.md"] | length == 1'
}

@test "v2: 9-generation chain caps at PREV_HISTORY_CAP=8" {
    local prev=""
    for i in $(seq 1 9); do
        echo "v$i" > "$SRC/a.md"
        local out="$TEST_TMPDIR/m$i.json"
        if [ -n "$prev" ]; then
            python3 "$BUILDER" "$SRC" "sha-$i" "2026-05-${i}T00:00:00Z" \
                --prev-manifest "$prev" > "$out"
        else
            python3 "$BUILDER" "$SRC" "sha-$i" "2026-05-${i}T00:00:00Z" > "$out"
        fi
        prev="$out"
    done
    jq -e '.previous_hashes["a.md"] | length == 8' "$prev"
}

@test "v2: malformed --prev-manifest → warning, empty history" {
    echo a > "$SRC/a.md"
    echo "not json" > "$TEST_TMPDIR/bad.json"
    run bash -c "python3 '$BUILDER' '$SRC' 'sha-1' '2026-05-20T00:00:00Z' --prev-manifest '$TEST_TMPDIR/bad.json' 2>'$TEST_TMPDIR/err.log'"
    [ "$status" -eq 0 ]
    echo "$output" | jq -e '.previous_hashes == {}'
    grep -q "failed to parse" "$TEST_TMPDIR/err.log"
}

@test "v2: unchanged file does NOT accumulate in previous_hashes" {
    echo same > "$SRC/a.md"
    python3 "$BUILDER" "$SRC" "sha-1" "2026-05-01T00:00:00Z" > "$TEST_TMPDIR/prev.json"
    run python3 "$BUILDER" "$SRC" "sha-2" "2026-05-20T00:00:00Z" \
        --prev-manifest "$TEST_TMPDIR/prev.json"
    [ "$status" -eq 0 ]
    echo "$output" | jq -e '.previous_hashes == {}'
}

@test "v2: missing --prev-manifest file → warning, empty history" {
    echo a > "$SRC/a.md"
    run bash -c "python3 '$BUILDER' '$SRC' 'sha-1' '2026-05-20T00:00:00Z' --prev-manifest '$TEST_TMPDIR/no-such-file.json' 2>'$TEST_TMPDIR/err.log'"
    [ "$status" -eq 0 ]
    echo "$output" | jq -e '.previous_hashes == {}'
    grep -q "not found" "$TEST_TMPDIR/err.log"
}
