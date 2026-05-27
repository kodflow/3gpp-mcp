#!/usr/bin/env bash
# Shared setup for all bats tests

# Path to scripts under test
SCRIPTS_DIR="${BATS_TEST_DIRNAME}/../../.devcontainer/images/.claude/scripts"

# Call from each test file's setup()
common_setup() {
    TEST_TMPDIR="$(mktemp -d)"
    export TEST_TMPDIR
}

# Call from each test file's teardown()
common_teardown() {
    [ -d "${TEST_TMPDIR:-}" ] && rm -rf "$TEST_TMPDIR"
}

# Source common.sh for tests that need it
load_common() {
    # shellcheck source=/dev/null
    source "$SCRIPTS_DIR/common.sh"
}

# Create a temporary .env file scoped to TEST_TMPDIR and export ENV_FILE
# pointing at it. Used by postCreate-* bats to isolate workspace state.
# Usage: set_env_file 'GIT_USER="kodflow"' 'GIT_EMAIL="x@y.com"'
set_env_file() {
    ENV_FILE="$TEST_TMPDIR/test.env"
    : > "$ENV_FILE"
    local line
    for line in "$@"; do
        printf '%s\n' "$line" >> "$ENV_FILE"
    done
    export ENV_FILE
}

# Source a single function definition from a shell file without executing
# the rest of the file. Useful when the file has top-level state mutations
# (init_steps, set -u, …) that bats setup() cannot tolerate.
# Usage: source_function_from "$path_to_sh" "function_name"
source_function_from() {
    local file="$1" func="$2" definition
    definition=$(awk -v f="$func" '
        $0 ~ "^"f"\\(\\) \\{" {p=1}
        p {print}
        p && /^\}$/ {exit}
    ' "$file") || return 1
    # Fail loudly on a missing helper import — silent eval "" turns a config
    # mistake into a hard-to-diagnose test failure later. CodeRabbit #368.
    if [ -z "$definition" ]; then
        echo "source_function_from: function '$func' not found in $file" >&2
        return 1
    fi
    eval "$definition"
}

# Build a TEST_TMPDIR-scoped GPG keyring.
# - "with-secret"/"both": generates an ed25519 secret-bearing key at runtime
#   (no committed PGP private key in source control, no CI entropy issues —
#   ed25519 is generated in <1s on any modern runner).
# - "pub-only": imports the committed pubkey-only.asc (a static pub key whose
#   secret half is never generated; the absence-of-secret IS the test scenario).
# Usage: make_test_gpg_keyring with-secret | pub-only | both
make_test_gpg_keyring() {
    local mode="${1:-with-secret}"
    export GNUPGHOME="$TEST_TMPDIR/.gnupg"
    mkdir -p "$GNUPGHOME"
    chmod 700 "$GNUPGHOME"
    local fixtures
    fixtures="${BATS_TEST_DIRNAME}/../fixtures/gpg"
    case "$mode" in
        with-secret)
            gpg --batch --pinentry-mode loopback --passphrase '' \
                --quick-generate-key "test-with-secret@example.com" ed25519 default never \
                >/dev/null 2>&1
            ;;
        pub-only)
            gpg --batch --import "$fixtures/pubkey-only.asc" >/dev/null 2>&1
            ;;
        both)
            gpg --batch --pinentry-mode loopback --passphrase '' \
                --quick-generate-key "test-with-secret@example.com" ed25519 default never \
                >/dev/null 2>&1
            gpg --batch --import "$fixtures/pubkey-only.asc" >/dev/null 2>&1
            ;;
        *)
            # Fail fast on typo'd modes — silent empty-keyring runs make
            # the resulting test failures hard to attribute. CodeRabbit #368.
            echo "make_test_gpg_keyring: unknown mode '$mode' (expected with-secret|pub-only|both)" >&2
            return 1
            ;;
    esac
}

# Print the long key ID of a key whose UID matches the given email pattern.
# Returns empty string if no match. Usage: gpg_key_id_for "test@example.com"
gpg_key_id_for() {
    local uid_pattern="$1"
    gpg --list-keys --keyid-format LONG 2>/dev/null \
        | awk -v p="$uid_pattern" '
            /^pub/ { split($2, a, "/"); kid = a[2] }
            /^uid/ { if (index($0, p) > 0) { print kid; exit } }
        '
}
