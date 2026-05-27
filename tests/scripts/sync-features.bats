#!/usr/bin/env bats
# Tests for images/hooks/shared/sync-features.sh — the 3-way safe sync helper
# that fixes issue #334 (silent overwrite of consumer-modified feature files).

setup() {
    load '../helpers/setup'
    common_setup

    HELPER="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/shared/sync-features.sh"
    UTILS="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/shared/utils.sh"
    BUILDER="${BATS_TEST_DIRNAME}/../../.devcontainer/scripts/build-features-manifest.py"

    SRC="$TEST_TMPDIR/src"
    WS="$TEST_TMPDIR/ws"
    DST="$WS/.devcontainer/features"
    MAN="$TEST_TMPDIR/manifest.json"
    mkdir -p "$SRC" "$DST" "$WS/.devcontainer"

    git -C "$WS" init -q
    git -C "$WS" config user.email "test@test"
    git -C "$WS" config user.name "test"

    # shellcheck source=/dev/null
    source "$UTILS"
    export FEATURES_MANIFEST="$MAN"
    # shellcheck source=/dev/null
    source "$HELPER"
}

teardown() { common_teardown; }

# Build a manifest from the current SRC tree and commit DST as the
# baseline "previously-shipped" state.
seed_baseline() {
    cp -r "$SRC"/* "$DST/" 2>/dev/null || true
    python3 "$BUILDER" "$SRC" "test" "2026-04-28T00:00:00Z" > "$MAN"
    git -C "$WS" add . && git -C "$WS" commit -qm "baseline" 2>/dev/null || true
}

# --- T1: happy path ---
@test "T1 happy path: clean consumer, no upstream change → noop" {
    echo "alpha" > "$SRC/alpha.md"
    seed_baseline

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    [[ "$output" == *"0 copied, 1 unchanged, 0 removed, 0 preserved"* ]]
}

# --- T2: reproducer of issue #334 (uncommitted edit) ---
@test "T2 issue #334 reproducer: tracked + git-dirty file → preserved" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline

    # Consumer makes an uncommitted edit
    echo "consumer-edit" >> "$DST/alpha.md"
    # Upstream pushes a different version
    echo "alpha-v2" > "$SRC/alpha.md"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    grep -q "consumer-edit" "$DST/alpha.md"
    [[ "$output" == *"1 preserved"* ]]
    [[ "$output" == *"uncommitted changes"* ]]
}

# --- T3: committed consumer edit (manifest catches) ---
@test "T3 committed consumer edit + upstream change → manifest preserves" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline

    # Consumer commits an edit
    echo "consumer-committed" > "$DST/alpha.md"
    git -C "$WS" add . && git -C "$WS" commit -qm "consumer edit"

    # Upstream pushes a different version
    echo "alpha-v2" > "$SRC/alpha.md"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    grep -q "consumer-committed" "$DST/alpha.md"
    [[ "$output" == *"1 preserved"* ]]
    [[ "$output" == *"divergent from upstream"* ]]
}

# --- T4: consumer-added file is left alone ---
@test "T4 consumer-added file → preserved (not in manifest, not deleted)" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline

    echo "consumer-only" > "$DST/extra.md"
    git -C "$WS" add . && git -C "$WS" commit -qm "extra"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ -f "$DST/extra.md" ]
    grep -q "consumer-only" "$DST/extra.md"
}

# --- T5: upstream-removed file (consumer untouched) → safe delete ---
@test "T5 upstream removes pristine file → deleted via manifest" {
    echo "alpha" > "$SRC/alpha.md"
    echo "beta" > "$SRC/beta.md"
    seed_baseline

    rm "$SRC/beta.md"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    [ ! -e "$DST/beta.md" ]
    [[ "$output" == *"1 removed"* ]]
}

# --- T6: upstream-removed file (consumer modified) → keep + warn ---
@test "T6 upstream removes consumer-modified file → kept" {
    echo "alpha" > "$SRC/alpha.md"
    echo "beta" > "$SRC/beta.md"
    seed_baseline

    rm "$SRC/beta.md"
    echo "consumer-modified" > "$DST/beta.md"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    [ -e "$DST/beta.md" ]
    grep -q "consumer-modified" "$DST/beta.md"
    [[ "$output" == *"Keeping features/beta.md"* ]]
}

# --- T7: new upstream file colliding with consumer-added path ---
@test "T7 new upstream path collides with consumer file → preserved" {
    echo "alpha" > "$SRC/alpha.md"
    seed_baseline

    # Consumer creates a file at a path NOT in the manifest
    echo "consumer-x" > "$DST/x.md"
    # Upstream now ships a file at the same path (not regenerating manifest →
    # the helper sees a path absent from FEATURES_MANIFEST)
    echo "upstream-x" > "$SRC/x.md"

    run sync_features_tree "$SRC" "$DST" "$WS"
    grep -q "consumer-x" "$DST/x.md"
    [[ "$output" == *"new upstream path collides"* ]]
}

# --- T8: missing manifest → falls back to Phase 1 (git-dirty) ---
@test "T8 manifest absent → falls back to Phase 1 (overwrites committed edit)" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline
    echo "consumer-committed" > "$DST/alpha.md"
    git -C "$WS" add . && git -C "$WS" commit -qm "edit"
    echo "alpha-v2" > "$SRC/alpha.md"

    export FEATURES_MANIFEST="/no/such/manifest.json"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    grep -q "alpha-v2" "$DST/alpha.md"
}

# --- T9: missing manifest + dirty tracked file → still preserved ---
@test "T9 manifest absent but git-dirty → Phase 1 preserves" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline
    echo "consumer-edit" >> "$DST/alpha.md"   # uncommitted
    echo "alpha-v2" > "$SRC/alpha.md"

    export FEATURES_MANIFEST="/no/such/manifest.json"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    grep -q "consumer-edit" "$DST/alpha.md"
    [[ "$output" == *"1 preserved"* ]]
}

# --- T10: byte-identical file → noop, no copy or skip ---
@test "T10 byte-identical src/dst → noop counter" {
    echo "alpha" > "$SRC/alpha.md"
    seed_baseline

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    [[ "$output" == *"0 copied"* ]]
    [[ "$output" == *"1 unchanged"* ]]
    [[ "$output" == *"0 preserved"* ]]
}

# --- T11: stale-but-clean (#367): previous_hashes match → fast-forward, silent ---
@test "T11 stale-but-clean: previous_hashes match → fast-forward, silent" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline                       # baseline: SRC=alpha-v1, manifest v2 (empty previous_hashes)

    # Consumer commits the v1 content (no edit, just lagging behind)
    git -C "$WS" add . && git -C "$WS" commit -qm "consumer baseline" 2>/dev/null || true

    # Upstream pushes a different version; rebuild manifest with --prev-manifest
    echo "alpha-v2" > "$SRC/alpha.md"
    python3 "$BUILDER" "$SRC" "test-v2" "2026-05-20T00:00:00Z" \
        --prev-manifest "$MAN" > "${MAN}.new"
    mv "${MAN}.new" "$MAN"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    grep -q "alpha-v2" "$DST/alpha.md"               # fast-forwarded
    [[ "$output" != *"consumer-modified"* ]]         # legacy wording absent
    [[ "$output" != *"divergent"* ]]                 # new warning absent (it was clean)
    [[ "$output" == *"1 fast-forwarded"* ]]          # summary counter
}

# --- T13: true committed fork (no prior shipped match) → preserved with improved warning ---
@test "T13 committed fork → preserved with improved warning, no fast-forward" {
    echo "alpha-v1" > "$SRC/alpha.md"
    seed_baseline

    # Consumer commits a TRUE fork (content never shipped)
    echo "consumer-true-fork" > "$DST/alpha.md"
    git -C "$WS" add . && git -C "$WS" commit -qm "consumer fork" 2>/dev/null || true

    # Upstream pushes again; manifest knows alpha-v1 as previous, NOT the fork
    echo "alpha-v2" > "$SRC/alpha.md"
    python3 "$BUILDER" "$SRC" "test-v2" "2026-05-20T00:00:00Z" \
        --prev-manifest "$MAN" > "${MAN}.new"
    mv "${MAN}.new" "$MAN"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    grep -q "consumer-true-fork" "$DST/alpha.md"     # preserved
    [[ "$output" == *"divergent from upstream"* ]]   # new wording
    [[ "$output" == *"git diff"* ]]                  # actionable hint present
    [[ "$output" == *"1 preserved"* ]]               # counter
    [[ "$output" == *"0 fast-forwarded"* ]]          # NOT fast-forwarded
}

# --- T12: template-repo self-exclusion via origin URL (#367) ---
@test "T12 origin=kodflow/devcontainer-template → step_sync_features short-circuits" {
    local postStart="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/lifecycle/postStart.sh"
    source_function_from "$postStart" step_sync_features

    echo "alpha" > "$SRC/alpha.md"
    seed_baseline
    git -C "$WS" remote add origin "https://github.com/kodflow/devcontainer-template.git" 2>/dev/null || true

    # Even with upstream change, no copy should happen — function early-exits.
    echo "alpha-upstream-changed" > "$SRC/alpha.md"

    export WORKSPACE_FOLDER="$WS"

    run step_sync_features
    [ "$status" -eq 0 ]
    [[ "$output" == *"Skipping features sync: template repo"* ]]
    grep -q "^alpha$" "$DST/alpha.md"  # dst untouched (the early-exit fires before any I/O)
}

# --- T14: v2-only deletion (file in previous_hashes only, dropped from files) ---
@test "T14 v2 deletion: file removed upstream → previous_hashes drives deletion" {
    echo "alpha-v1" > "$SRC/alpha.md"
    echo "beta-v1" > "$SRC/beta.md"
    seed_baseline                       # v1 manifest: files={alpha,beta}, previous_hashes={}
    git -C "$WS" add . && git -C "$WS" commit -qm "baseline" 2>/dev/null || true

    # Upstream removes beta; rebuild manifest v2 (--prev-manifest moves beta into previous_hashes)
    rm "$SRC/beta.md"
    python3 "$BUILDER" "$SRC" "test-v2" "2026-05-20T00:00:00Z" \
        --prev-manifest "$MAN" > "${MAN}.new"
    mv "${MAN}.new" "$MAN"

    # Sanity: beta is gone from .files but lives in .previous_hashes
    # (use bracket-form for keys containing dots — `.foo.bar` would walk into a nested object)
    jq -e '.files["beta.md"] == null' "$MAN"
    jq -e '.previous_hashes["beta.md"] | length > 0' "$MAN"

    run sync_features_tree "$SRC" "$DST" "$WS"
    [ "$status" -eq 0 ]
    [ ! -e "$DST/beta.md" ]                          # deleted via previous_hashes path
    [[ "$output" == *"1 removed"* ]]
}
