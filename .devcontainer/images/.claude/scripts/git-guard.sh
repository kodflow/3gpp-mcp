#!/bin/bash
# ============================================================================
# git-guard.sh - Combined git commit/push guard (PreToolUse Bash)
# Merges commit-validate.sh + security.sh into a single script.
#
# Only activates on git commit/push commands. All other Bash commands
# pass through instantly (no JSON parsing overhead).
#
# Functions:
#   1. Block AI/Claude mentions in commit messages
#   2. Auto-correct --force → --force-with-lease
#   3. Scan staged files for secrets before commit
#
# Exit 0 = allow, Exit 2 = block
# ============================================================================

set +e  # Fail-open

# === Read hook input ===
INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0
fi

TOOL=$(printf '%s' "$INPUT" | jq -r '.tool_name // ""' 2>/dev/null || echo "")
COMMAND=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // ""' 2>/dev/null || echo "")

# Only handle Bash tool
if [ "$TOOL" != "Bash" ]; then
    exit 0
fi

# Normalize RTK-prefixed commands
NORMALIZED_CMD="$COMMAND"
if [[ "$NORMALIZED_CMD" =~ ^rtk[[:space:]]+proxy[[:space:]]+ ]]; then
    NORMALIZED_CMD="${NORMALIZED_CMD#rtk proxy }"
elif [[ "$NORMALIZED_CMD" =~ ^rtk[[:space:]]+ ]]; then
    NORMALIZED_CMD="${NORMALIZED_CMD#rtk }"
fi

# === FAST EXIT: not a git commit/push/rebase/cherry-pick? Skip entirely ===
#
# rebase and cherry-pick are included because they replay upstream commits
# whose messages may carry tainted AI attribution (see #358 D2 + #359 CR-11).
# Letting them through the fast-exit means the --no-verify guard, the
# AI-mention scan, and the secret scan all get a chance to evaluate them
# (each scoped to its own subcommand check below).
if [[ ! "$NORMALIZED_CMD" =~ ^git[[:space:]]+(commit|push|rebase|cherry-pick) ]]; then
    exit 0
fi

# ============================================================
# From here: git commit or git push only
# ============================================================

# === 0. Reject --no-verify ===
#
# `--no-verify` bypasses every local git hook — pre-commit, commit-msg,
# pre-push — which makes the layer-2 .githooks/commit-msg guard pointless if
# Claude can route around it. We refuse it outright. The only valid escape
# hatch is a deliberate human terminal action outside Claude's Bash tool.
#
# `-n` after `git commit` is git's documented alias for `--no-verify`
# (see `man git-commit`). For `git push`, `-n` means `--dry-run` and is
# harmless, so we only treat the bare `-n` as a bypass when preceded by
# `commit`. Flag order doesn't matter to git, so `-n` anywhere after
# `git commit` (e.g. `git commit -m msg -n`) is treated as --no-verify —
# this is intentional (CR-2 PR #359 review).
#
# Combined short flags: git accepts `-nm`, `-anm`, `-anqv`, … as the same
# thing as `-n -m`, `-a -n -m`, etc. Our regex catches `n` anywhere inside
# the combined short-flag cluster (`-[a-zA-Z]*n[a-zA-Z]*`). Long options
# like `--no-verify` are NOT matched because the second `-` is not in
# `[a-zA-Z]`. (CR-9 PR #359 round 2.)
#
# To avoid false positives where the message itself contains the literal
# text "-n" (e.g. `git commit -m "docs: explain -n flag"`), we strip every
# "…" / '…' substring BEFORE running the regex on the command. That leaves
# only the flag positions of the command for the scan to consider.
# (CR-10 PR #359 round 3.)
FLAGS_ONLY=$(printf '%s' "$NORMALIZED_CMD" | sed -E 's/"[^"]*"//g; s/'\''[^'\'']*'\''//g')
if [[ "$FLAGS_ONLY" =~ --no-verify ]] \
   || { [[ "$NORMALIZED_CMD" =~ ^git[[:space:]]+commit([[:space:]]|$) ]] \
        && [[ "$FLAGS_ONLY" =~ [[:space:]]-[a-zA-Z]*n[a-zA-Z]*([[:space:]]|$) ]]; }; then
    echo "═══════════════════════════════════════════════" >&2
    echo "  ❌ COMMAND BLOCKED — --no-verify is forbidden" >&2
    echo "═══════════════════════════════════════════════" >&2
    echo "" >&2
    echo "  Bypassing local git hooks defeats the AI-attribution and" >&2
    echo "  secret-scan guards (defence-in-depth, issue #358)." >&2
    echo "  If a hook is wrong, fix the hook — don't skip it." >&2
    echo "" >&2
    echo "═══════════════════════════════════════════════" >&2
    exit 2
fi

# === 1. Auto-correct --force → --force-with-lease (push) ===
if [[ "$NORMALIZED_CMD" =~ ^git[[:space:]]+push ]] && \
   [[ "$NORMALIZED_CMD" =~ --force ]] && \
   [[ ! "$NORMALIZED_CMD" =~ --force-with-lease ]]; then
    # Replace standalone --force with --force-with-lease (preserves quoting)
    CORRECTED="${COMMAND/--force/--force-with-lease}"
    echo "⚠️  Auto-corrected: --force → --force-with-lease" >&2
    jq -n --arg cmd "$CORRECTED" \
        --arg reason "Auto-corrected: --force → --force-with-lease" \
        '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":$reason,"updatedInput":{"command":$cmd}}}'
    exit 0
fi

# === 2. Block AI mentions in commit messages ===
#
# Build a HAYSTACK string covering every shape `git commit` accepts.
#
# Lesson from #359 review (CR-1, CR-3, Q-1): trying to *extract* individual
# `-m` / `--message` values with greedy `sed` patterns is brittle — each
# `.*-m` pattern anchors to the LAST occurrence, so earlier `-m` blocks
# are silently dropped. Same problem with heredoc delimiters: hard-coding
# `EOF` misses `<<END`, `<<COMMIT_MSG`, etc.
#
# Solution: don't extract — SCAN the raw $COMMAND string. Every `-m`/`-F`/
# heredoc value is already a literal substring of $COMMAND; pattern matching
# on the whole string catches them all regardless of count, quoting, or
# heredoc delimiter. We only need to *augment* the haystack with content
# that lives outside $COMMAND: file content from `-F file`, HEAD message
# from `--amend`, replayed messages from `rebase`/`cherry-pick`.
#
# Catches every leak path in #358 D2:
#   - git commit -m "..." | -m '...' | -m bare | -m "X" -m "Y"       ← in $COMMAND
#   - git commit --message=... | --message X                          ← in $COMMAND
#   - Heredoc bodies (any delimiter, single-line invocation)          ← in $COMMAND
#   - git commit -F file                                              ← we read file
#   - git commit -F - (stdin)                                         ← unreachable, documented
#   - git commit --amend (no -m)                                      ← we read git log -1
#   - git rebase --continue / git cherry-pick                         ← we scan recent log
#   - git commit (editor mode) — caught by layer 2 (.githooks/commit-msg)
if [[ "$NORMALIZED_CMD" =~ ^git[[:space:]]+(commit|rebase|cherry-pick) ]]; then
    # Start with the raw command string — covers every -m/--message/-F path/
    # heredoc-body substring without any extraction step. (CR-1/CR-3/Q-1.)
    HAYSTACK="$COMMAND"

    # -F file content (skip "-F -" which means stdin — unreachable from here).
    # Handle the three quoting shapes git accepts so paths with spaces work:
    #   -F "path with spaces"   (double-quoted, CR #359 round 4 finding)
    #   -F 'path with spaces'   (single-quoted)
    #   -F bare/path            (unquoted, no spaces)
    F_ARG=""
    if [[ "$NORMALIZED_CMD" =~ -F[[:space:]]+\"([^\"]+)\" ]]; then
        F_ARG="${BASH_REMATCH[1]}"
    elif [[ "$NORMALIZED_CMD" =~ -F[[:space:]]+\'([^\']+)\' ]]; then
        F_ARG="${BASH_REMATCH[1]}"
    elif [[ "$NORMALIZED_CMD" =~ -F[[:space:]]+([^[:space:]]+) ]]; then
        F_ARG="${BASH_REMATCH[1]}"
    fi
    if [ -n "$F_ARG" ] && [ "$F_ARG" != "-" ] && [ -r "$F_ARG" ]; then
        HAYSTACK="$HAYSTACK"$'\n'"$(cat -- "$F_ARG" 2>/dev/null || true)"
    fi

    # --amend without -m reuses HEAD's commit message — fetch it.
    if [[ "$NORMALIZED_CMD" =~ --amend ]] && [[ ! "$NORMALIZED_CMD" =~ -m[[:space:]]|--message ]]; then
        HAYSTACK="$HAYSTACK"$'\n'"$(git log -1 --pretty=%B 2>/dev/null || true)"
    fi

    # rebase --continue / cherry-pick: defensive scan of recent commits.
    # Note: scans the last 5 commits from HEAD, which may include commits
    # not actually being replayed; that's a coverage/perf trade-off we
    # accept (CR-5). Parsing .git/rebase-merge/git-rebase-todo to scope
    # exactly to the operation's todo list would be more precise but
    # significantly more complex for marginal gain — the cost of one
    # extra `git log` call is negligible vs. the risk of a false negative.
    if [[ "$NORMALIZED_CMD" =~ ^git[[:space:]]+(rebase|cherry-pick) ]]; then
        HAYSTACK="$HAYSTACK"$'\n'"$(git log -5 --pretty=%B 2>/dev/null || true)"
    fi

    # Editor mode (no -m, no -F, no --amend): the message is going to be typed
    # in COMMIT_EDITMSG, which doesn't exist yet at PreToolUse time. We cannot
    # block it here — the `.githooks/commit-msg` hook (layer 2, defence-in-depth)
    # catches that path at git's `commit-msg` event. Documented in #358 D3.

    if [ -n "$HAYSTACK" ]; then
        HAYSTACK_LOWER=$(printf '%s' "$HAYSTACK" | tr '[:upper:]' '[:lower:]')

        FORBIDDEN_PATTERNS=(
            "co-authored-by:.*(claude|anthropic|openai|gpt|copilot|gemini|llm|ai)"
            "generated[[:space:]]+(by|with)[[:space:]]+(claude|ai|gpt|anthropic|copilot)"
            "ai[-.]assisted"
            "🤖"
            "claude[-[:space:]]code"
            # Issue #364: workflow-internal leaks (RLM-plan-driven AI assistance)
            "\\.claude/"
            "^[[:space:]]*plan:"
            "(see|ref|tracked-in)[[:space:]]+\\.claude"
            "\\bplan[[:space:]]+\\.claude"
        )

        for pattern in "${FORBIDDEN_PATTERNS[@]}"; do
            if echo "$HAYSTACK_LOWER" | grep -qiE "$pattern"; then
                echo "═══════════════════════════════════════════════" >&2
                echo "  ❌ COMMIT BLOCKED - AI reference detected" >&2
                echo "═══════════════════════════════════════════════" >&2
                echo "" >&2
                echo "  Forbidden pattern: $pattern" >&2
                echo "  Remove AI / workflow-internal references from the commit message." >&2
                echo "  Blocked categories:" >&2
                echo "    - AI attribution (co-authored-by, generated by, 🤖, ai-assisted)" >&2
                echo "    - Workflow leaks (#364): .claude/ paths, 'Plan:' footers" >&2
                echo "  Defence-in-depth: layer 2 (.githooks/commit-msg) will" >&2
                echo "  also block this on the git side if you bypass me." >&2
                echo "" >&2
                echo "═══════════════════════════════════════════════" >&2
                exit 2
            fi
        done
    fi
fi

if [[ "$NORMALIZED_CMD" =~ ^git[[:space:]]+commit ]]; then

    # === 3. Scan staged files for secrets (reads staged blobs, not working tree) ===
    ISSUES_FOUND=0

    # Inline lightweight secret scan (avoid calling security.sh subprocess)
    # Use process substitution to avoid subshell (preserves ISSUES_FOUND)
    while IFS= read -r -d '' f; do
        [ -z "$f" ] && continue

        # Skip hook/tooling scripts (contain regex patterns that match themselves)
        # Skip agents (contain security detection patterns), templates (.tpl),
        # lifecycle hooks (contain token variable references), and test fixtures
        case "$f" in
            */.claude/scripts/*|*/.claude/agents/*|*/.claude/commands/*) continue ;;
            */.githooks/*|*.bats|*.tpl) continue ;;
            */hooks/lifecycle/*) continue ;;
        esac

        # Read staged blob content (not working tree)
        STAGED_CONTENT=$(git show ":$f" 2>/dev/null) || continue

        # Skip binary files (check staged content)
        if printf '%s' "$STAGED_CONTENT" | file -- - 2>/dev/null | grep -q "binary"; then
            continue
        fi

        # Pattern-based secret detection on staged content
        if printf '%s' "$STAGED_CONTENT" | grep -iEq \
            'password\s*=\s*["\047][^"\047]+|api[_-]?key\s*=\s*["\047][^"\047]+|secret[_-]?key\s*=\s*["\047][^"\047]+|aws[_-]?access[_-]?key|BEGIN RSA PRIVATE KEY|BEGIN OPENSSH PRIVATE KEY|ghp_[a-zA-Z0-9]{36}|gho_[a-zA-Z0-9]{36}|github_pat_[a-zA-Z0-9_]+|sk-[a-zA-Z0-9]{48}|AKIA[0-9A-Z]{16}'; then
            echo "⚠️  Potential secret in: $f" >&2
            ISSUES_FOUND=1
        fi

        # detect-secrets (if available, more thorough)
        if [ $ISSUES_FOUND -eq 0 ] && command -v detect-secrets &>/dev/null; then
            if printf '%s' "$STAGED_CONTENT" | detect-secrets scan 2>/dev/null | python3 -c 'import sys,json; d=json.load(sys.stdin); exit(0 if any(d.get("results",{}).values()) else 1)'; then
                echo "⚠️  detect-secrets found issue in: $f" >&2
                ISSUES_FOUND=1
            fi
        fi
    done < <(git diff --cached --name-only -z 2>/dev/null)

    if [ $ISSUES_FOUND -eq 1 ]; then
        echo "═══════════════════════════════════════════════" >&2
        echo "  ⚠️  COMMIT BLOCKED - Secrets detected" >&2
        echo "═══════════════════════════════════════════════" >&2
        echo "  Remove secrets before committing." >&2
        echo "═══════════════════════════════════════════════" >&2
        exit 2
    fi
fi

exit 0
