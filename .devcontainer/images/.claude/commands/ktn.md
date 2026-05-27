---
name: ktn
description: |
  Autonomous health-and-heal for the ktn-linter MCP stack.
  Dispatches 5 specialist agents in parallel: each verifies + fixes ONE concern
  (binary version, mcp.json entry, .claude/settings.json hooks, daemon on :7717,
  phase config). Idempotent: does nothing when the stack is already healthy,
  prompts a session restart only when settings.json was actually modified.
  Use when: a fresh container starts, a Claude session can't reach ktn-linter,
  hooks misbehave, or you just want a one-command sanity check.
allowed-tools:
  - "Read(**/*)"
  - "Write(.claude/settings.json)"
  - "Write(mcp.json)"
  - "Write(.ktn-linter.yaml)"
  - "Edit(.claude/settings.json)"
  - "Edit(mcp.json)"
  - "Edit(.ktn-linter.yaml)"
  - "Bash(curl:*)"
  - "Bash(jq:*)"
  - "Bash(command:*)"
  - "Bash(which:*)"
  - "Bash(ktn-linter:*)"
  - "Bash(pkill:*)"
  - "Bash(pgrep:*)"
  - "Bash(kill:*)"
  - "Bash(ss:*)"
  - "Bash(lsof:*)"
  - "Bash(readlink:*)"
  - "Bash(stat:*)"
  - "Bash(cut:*)"
  - "Bash(nohup:*)"
  - "Bash(uname:*)"
  - "Bash(chmod:*)"
  - "Bash(mv:*)"
  - "Bash(mkdir:*)"
  - "Bash(sleep:*)"
  - "Bash(sort:*)"
  - "Bash(test:*)"
  - "Bash([:*)"
  - "Bash(echo:*)"
  - "Bash(cat:*)"
  - "Bash(head:*)"
  - "Bash(sed:*)"
  - "Bash(make:*)"
  - "Bash(rtk:*)"
  - "Bash(grep:*)"
  - "Bash(rg:*)"
  - "Bash(find:*)"
  - "Bash(ls:*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "WebFetch(api.github.com/*)"
  - "WebFetch(github.com/*)"
  - "mcp__github__get_latest_release"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
---

# /ktn — Autonomous ktn-linter MCP Lifecycle

$ARGUMENTS

> One command. Multiple parallel agents. Zero ceremony when everything is OK.
> Designed to be invoked blindly: `/ktn` is safe to run any time — it reads the
> live state, fixes drift only when it finds drift, and tells you to restart the
> session only if it had to touch `.claude/settings.json`.

---

## Arguments

| Pattern | Action |
|---------|--------|
| _(none)_ | Full parallel reconcile (default) |
| `--check` | Read-only diagnostic — no writes, no daemon spawn |
| `--phases <spec>` | Configure `.ktn-linter.yaml` then reconcile (spec syntax in `--help`) |
| `--restart` | Force daemon respawn (skip health probe) |
| `--uninstall` | Remove `mcp.json` ktn-linter entry + settings.json hook entries (binary kept) |
| `--help` | Show this help and STOP |

**IF `$ARGUMENTS` contains `--help`**: Print [Help](#help) verbatim and **STOP**. Do NOT spawn agents.

---

## Help

```text
═══════════════════════════════════════════════════════════════
  /ktn — ktn-linter MCP autonomous health + heal
═══════════════════════════════════════════════════════════════

  DEFAULT (no args)
    Dispatches 5 agents in parallel. Each verifies ONE concern
    and fixes it iff drifted. Idempotent — second run is no-op.

  FLAGS
    (none)                Full reconcile (default)
    --check               Read-only — never writes
    --phases <spec>       Apply phase config then reconcile
    --restart             Force daemon respawn
    --uninstall           Remove mcp.json + settings hook entries (binary kept)
    --help                Show this help

  PHASES SPEC (--phases)
    default               Reset to {1..7} (phase 8 opt-in)
    all                   Universe {1..8}
    1-7                   Range
    1,3,6                 Subset
    structural,logic      Aliases (canonical or variants)
    +tests                Add phase 8 to current set
    -comment              Remove phase 7 from current set
    show                  Print resolved active set + source

  PROJECT GATE (always first)
    Detects Go via rtk grep (go.mod, *.go, BUILD.bazel rules, etc).
    If no signal: pauses with a Yes/No AskUserQuestion prompt.
    Cancel = abort, no writes. Confirm = continue.

  PARALLEL AGENTS (after the gate)
    1. binary       Install / upgrade ktn-linter from GitHub releases
    2. mcp          Ensure mcp.json registers ktn-linter
    3. settings     Wire PreToolUse + PostToolUse HTTP hooks
    4. daemon       Health-check :7717 + respawn if dead
    5. phases       Validate .ktn-linter.yaml (or write if --phases)

  EXAMPLES
    /ktn                  First-run / fresh container reconcile
    /ktn --check          CI-style read-only audit
    /ktn --phases 1,3,6   Restrict to structural+logic+style
    /ktn --phases +tests  Opt into phase 8 (KTN-TEST-*)
    /ktn --restart        After kernel-suspend / OOM kill

  EXIT BEHAVIOR
    All ✓ + no writes        → silent OK ("nothing to do")
    Any agent wrote a file   → consolidated report + restart prompt
                                iff .claude/settings.json was touched
    --check finds drift      → non-zero report, no writes

═══════════════════════════════════════════════════════════════
```

---

## Overview

`/ktn` is a **read–decide–heal** loop, not a configuration UI. It owns the
MCP-server lifecycle around `ktn-linter` and complements `/lint` (which runs
scans). 5 specialist agents work in parallel because the 5 concerns are
file-disjoint:

| Agent | Reads | Writes | Network |
|-------|-------|--------|---------|
| `binary` | `which ktn-linter`, `ktn-linter version` | `/usr/local/bin/ktn-linter` (or `~/.local/bin/`) | GitHub releases API |
| `mcp` | `mcp.json` | `mcp.json` | — |
| `settings` | `.claude/settings.json` | `.claude/settings.json` | — |
| `daemon` | `curl :7717/health` | (none — process op only) | localhost:7717 |
| `phases` | `.ktn-linter.yaml` | `.ktn-linter.yaml` (only with `--phases`) | — |

No two agents touch the same path → safe to dispatch as **one parallel wave**.

### `--uninstall` mode

Reverses the lifecycle in a single parallel wave:

| Agent | Action |
|-------|--------|
| `mcp` | Delete `ktn-linter` from both `.servers` and `.mcpServers` |
| `settings` | Strip every hook whose URL begins with `http://localhost:7717/` from PreToolUse + PostToolUse; drop matchers whose hooks array becomes empty |
| `daemon` | `pkill ktn-linter serve` (best-effort, no respawn) |
| `binary` | Skipped — the binary is intentionally kept |
| `phases` | Skipped — `.ktn-linter.yaml` belongs to the user |

Restart prompt fires iff `.claude/settings.json` was modified.

---

## Phase 0 — Project gating (Go detection, MANDATORY first step)

> ktn-linter only lints Go code. Running `/ktn` on a non-Go project would
> install a binary, wire hooks, and spawn a daemon that will never be used.
> Before doing anything irreversible (binary download, settings.json merge,
> daemon respawn) the skill MUST confirm a Go project is present — or get
> explicit user opt-in to proceed anyway.

### Step 0.1 — Scan

Run the cheapest possible probe (no recursion into `vendor/`, `node_modules/`,
`.git/`). Use **`rtk grep`** so the output is token-compressed automatically;
the agent never sees the raw listing — just a count.

```bash
# Detect Go signals. Stop at first hit; 50ms typical on a clean repo.
go_signals=0

# 1. go.mod / go.work at root or one level deep (very fast Glob).
if ls "$WORKSPACE"/go.mod "$WORKSPACE"/go.work \
        "$WORKSPACE"/*/go.mod 2>/dev/null | head -1 | grep -q .; then
    go_signals=1
fi

# 2. Otherwise widen to *.go anywhere outside the usual junk dirs.
#    Limit to 1 hit — we only care "any Go file exists?".
if [ "$go_signals" -eq 0 ]; then
    if rtk grep -r -l --include='*.go' \
            --exclude-dir=vendor \
            --exclude-dir=node_modules \
            --exclude-dir=.git \
            --exclude-dir=dist \
            --exclude-dir=build \
            -m 1 . "$WORKSPACE" 2>/dev/null | head -1 | grep -q .; then
        go_signals=1
    fi
fi

# 3. Last-ditch: ancillary Go markers (legacy or Bazel-only repos).
if [ "$go_signals" -eq 0 ]; then
    for marker in Gopkg.toml .golangci.yml .golangci.yaml BUILD.bazel WORKSPACE.bazel; do
        if [ -e "$WORKSPACE/$marker" ]; then
            # BUILD.bazel exists in many non-Go repos — confirm Go rules in it.
            if [ "$marker" = "BUILD.bazel" ] || [ "$marker" = "WORKSPACE.bazel" ]; then
                if rtk grep -l -E 'go_(library|binary|test|module)' \
                        "$WORKSPACE/$marker" 2>/dev/null | head -1 | grep -q .; then
                    go_signals=1; break
                fi
                continue
            fi
            go_signals=1; break
        fi
    done
fi
```

The `Glob` and `Grep` tools are equivalent fast paths if shell is awkward in
the host context — same semantics, same gate.

### Step 0.2 — Branch

| `go_signals` | Action |
|--------------|--------|
| `1` | Print one line `[ktn] go-project=yes` and **continue to Phase 1 silently**. |
| `0` | **PAUSE**. Call `AskUserQuestion` with the prompt below and block until the user picks an option. |

### Step 0.3 — The question (only when `go_signals == 0`)

Use **`AskUserQuestion`** with **exactly** this shape — a single-select Yes/No
that surfaces a Submit / Cancel UI:

```json
{
  "questions": [{
    "question": "No Go files detected in this project (no go.mod, no *.go, no BUILD.bazel with Go rules). ktn-linter only lints Go code — running /ktn here will install a binary, wire hooks in .claude/settings.json, and spawn an HTTP daemon on :7717 that nothing will ever call. Proceed anyway?",
    "header": "No Go found",
    "multiSelect": false,
    "options": [
      {
        "label": "No, cancel",
        "description": "Stop /ktn now. No files written, no daemon spawned. (Recommended — re-run /ktn from a Go project root.)"
      },
      {
        "label": "Yes, proceed anyway",
        "description": "Continue with the full reconcile. Use this only if you intend to add Go code later or are bootstrapping a fresh template."
      }
    ]
  }]
}
```

Notes on the wording:

- **Default focus is "No, cancel"** (listed first) — the safer option is
  always the default when no Go is detected.
- The question is **one sentence** with explicit consequences spelled out so
  the user can decide without rereading the help.
- `multiSelect: false` → the UI shows radio buttons + Submit / Cancel actions
  (Cancel maps to the user dismissing the question; treat dismissal as
  "No, cancel").
- DO NOT add a third "Other" option — `AskUserQuestion` injects the free-text
  fallback automatically; we do not want to encourage prose answers here.

### Step 0.4 — Resolve the answer

| User picked | Action |
|-------------|--------|
| `No, cancel` (or dismissal / free-text rejecting) | Print the abort banner below and **STOP**. Do not enter Phase 1. |
| `Yes, proceed anyway` | Print `[ktn] go-project=no, user-confirmed=yes` and continue to Phase 1. |

Abort banner:

```text
═══════════════════════════════════════════════════════════════
  /ktn — cancelled
═══════════════════════════════════════════════════════════════

  Reason : no Go signals in $WORKSPACE
  Probes : go.mod  go.work  **/*.go  Gopkg.toml
           .golangci.yml  BUILD.bazel(with go_*)

  No files were written. No daemon was spawned.
  Re-run /ktn from a directory that contains Go code, or pass
  --check to inspect the current ktn-linter state without
  any writes.
═══════════════════════════════════════════════════════════════
```

### Step 0.5 — Read-only short-circuit

`--check` still requires a Go project gate — but the question becomes
informational rather than blocking: print the abort banner and **STOP**
without prompting (a read-only audit of a stack you don't use is noise).
Honoring `--check` with a forced prompt would defeat its automation use case
(CI, scripted audits).

---

## Phase 1 — Pre-flight (host, not an agent)

Execute these checks before spawning any agent. They define the per-agent
inputs:

```bash
# Target dirs
WORKSPACE="${WORKSPACE_FOLDER:-/workspace}"
SETTINGS_FILE="$WORKSPACE/.claude/settings.json"
MCP_FILE="$WORKSPACE/mcp.json"
PHASES_FILE="$WORKSPACE/.ktn-linter.yaml"

# OS/arch for binary fetch. Devcontainer is Linux by construction, but derive
# from `uname` so the skill stays correct if a user runs it on a host shell
# outside the container (issue #356 mentions `go env GOOS/GOARCH`; we avoid
# the `go` dependency since `uname` is universal).
case "$(uname -s)" in
    Linux)   GOOS="linux"  ;;
    Darwin)  GOOS="darwin" ;;
    *)       echo "✗ Unsupported OS: $(uname -s)"; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64)         GOARCH="amd64" ;;
    aarch64|arm64)  GOARCH="arm64" ;;
    armv7l)         GOARCH="armv6l" ;;
    *)              echo "✗ Unsupported arch: $(uname -m)"; exit 1 ;;
esac

# Track if --check (read-only)
READ_ONLY=0
[[ "$ARGUMENTS" == *"--check"* ]] && READ_ONLY=1

# Track if --phases <spec> was passed (extract the spec)
PHASES_SPEC=""
case "$ARGUMENTS" in
    *"--phases "*)
        PHASES_SPEC="$(echo "$ARGUMENTS" | sed -n 's/.*--phases \([^ ]*\).*/\1/p')"
        ;;
esac

FORCE_RESTART=0
[[ "$ARGUMENTS" == *"--restart"* ]] && FORCE_RESTART=1

# Track --uninstall (mutually exclusive with reconcile)
UNINSTALL=0
[[ "$ARGUMENTS" == *"--uninstall"* ]] && UNINSTALL=1
```

Surface a single banner line:

```text
[ktn] os=<linux|darwin>/<arch> mode={reconcile|check|uninstall} phases-spec={none|<spec>} restart={0|1}
```

---

## Phase 2 — Parallel agent dispatch (single message, 5 Task calls)

**MANDATORY: spawn all 5 agents in ONE assistant message** so they execute
concurrently. Each agent receives a self-contained prompt + permission to
write its own file only.

### Agent A — `binary`

```yaml
# PR4 — Skills Architecture v1.3: routed via route-agent.sh to
# devops-executor-linux instead of general-purpose. The route-agent
# call returns {subagent_type, resolved_model, effort}; pass them
# through to the Task primitive.
subagent_type: devops-executor-linux
description: "ktn-linter binary health + upgrade"
prompt: |
  You manage the ktn-linter binary lifecycle. Goal: make sure the local
  ktn-linter binary is present and matches the latest GitHub release.

  Mode: {{READ_ONLY ? "read-only — REPORT ONLY, do not write" : "reconcile"}}

  STEP 1 — Detect current state
    a. resolved_path = `command -v ktn-linter || echo absent`
    b. local_version = `ktn-linter version 2>/dev/null | head -1` (parse
       `ktn-linter version X.Y.Z`). If output contains "dev" → dev_build=true.
    c. latest_tag = WebFetch
       https://api.github.com/repos/kodflow/ktn-linter/releases/latest →
       extract `.tag_name` (strip leading `v`).

  STEP 2 — Decide
    - absent → INSTALL
    - dev_build=true → SKIP (refuse upgrade over dev build, surface warning)
    - local_version < latest_tag (semver) → UPGRADE
    - else → NO-OP

  STEP 3 — Act (skip entirely in --check mode)
    INSTALL path:
      target_dir = first writable of:
        /usr/local/bin (try sudo -n mv)
        ~/.local/bin
        ~/bin
      mkdir -p $target_dir && ensure on $PATH
      asset_url = https://github.com/kodflow/ktn-linter/releases/download/v{{latest_tag}}/ktn-linter-{{GOOS}}-{{GOARCH}}
      curl -fsSL "$asset_url" -o /tmp/ktn-linter.new
      chmod +x /tmp/ktn-linter.new
      mv /tmp/ktn-linter.new $target_dir/ktn-linter
    UPGRADE path:
      Prefer `ktn-linter upgrade` (atomic rename in same dir, ErrDevBuild aware).
      If that fails OR dev_build=true, fall back to INSTALL path with --force semantics.

  STEP 4 — Verify
    `ktn-linter version` reports the expected version.

  RETURN exactly this JSON on the last line of your reply:
  {
    "agent": "binary",
    "status": "ok|fixed|skipped|error",
    "action": "noop|installed|upgraded|refused-dev-build",
    "before": "absent|X.Y.Z|dev",
    "after":  "X.Y.Z",
    "path":   "/usr/local/bin/ktn-linter|...",
    "notes":  "..."
  }
```

### Agent B — `mcp`

```yaml
# PR4 — Skills Architecture v1.3: routed via route-agent.sh to
# devops-executor-linux instead of general-purpose. The route-agent
# call returns {subagent_type, resolved_model, effort}; pass them
# through to the Task primitive.
subagent_type: devops-executor-linux
description: "mcp.json ktn-linter registration"
prompt: |
  You own the ktn-linter entry inside /workspace/mcp.json.

  Mode: {{UNINSTALL ? "uninstall" : (READ_ONLY ? "read-only" : "reconcile")}}

  Canonical schema for this repo: the postStart merger writes the merged
  config under the top-level `.servers` key (see
  /etc/mcp/mcp.json.tpl — `{"servers": {...}}`), with the Go feature
  fragment defining ktn-linter as `command: "ktn-linter", args: ["serve"]`
  (port 7717 is the upstream default in cmd/ktn-linter/cmd/serve.go).
  Some setups use the legacy `.mcpServers` key — accept either on read,
  but write to `.servers` to match this repo's template.

  STEP 1 — Read current mcp.json
    If file absent → drift=missing-file; the postStart MCP merger will
    regenerate it from /etc/mcp/mcp.json.tpl on next container start, so in
    that case we only write a minimal stub IFF reconcile mode AND the
    template merger is unavailable. Otherwise report drift and exit.

  STEP 2 — Detect drift
    Required shape (under `.servers` for this repo, but accept `.mcpServers`
    on read):
      "ktn-linter": {
        "command": "ktn-linter",
        "args": ["serve"]          (canonical; "--port" "7717" also valid)
      }
    drift = none if either args==["serve"] OR args==["serve","--port","7717"];
            missing-entry | mismatched-args | missing-file otherwise.

  STEP 3 — Act (skip in --check mode)
    Prefer the canonical upstream entrypoint:
      ktn-linter mcp install --port=7717
    which writes mcp.json + merges .claude/settings.json in one idempotent
    call (see upstream cmd/ktn-linter/cmd/mcp_install.go).
    If `ktn-linter mcp install` is unavailable (older binary, missing
    subcommand), fall back to a `jq` merge that writes ONLY the
    ktn-linter entry under `.servers`, preserving every other server:
      jq '.servers["ktn-linter"] = {command:"ktn-linter",args:["serve"]}' \
        mcp.json > mcp.json.new && mv mcp.json.new mcp.json
    NEVER rewrite the whole file by hand.

    UNINSTALL mode: remove ktn-linter from .servers AND .mcpServers
    (delete both keys; idempotent if absent):
      jq 'del(.servers["ktn-linter"], .mcpServers["ktn-linter"])' \
        mcp.json > mcp.json.new && mv mcp.json.new mcp.json

  STEP 4 — Verify
    `jq -e '(.servers["ktn-linter"]? // .mcpServers["ktn-linter"]?) | (.command == "ktn-linter" and (.args[0]? == "serve"))' mcp.json`
    For uninstall: the inverse — both keys must be null.

  RETURN:
  {
    "agent": "mcp",
    "status": "ok|fixed|error",
    "drift":  "none|missing-entry|mismatched-args|missing-file",
    "wrote_file": true|false,
    "schema_key": "servers|mcpServers",
    "notes":  "..."
  }
```

### Agent C — `settings`

```yaml
# PR4 — Skills Architecture v1.3: routed via route-agent.sh to
# devops-executor-linux instead of general-purpose. The route-agent
# call returns {subagent_type, resolved_model, effort}; pass them
# through to the Task primitive.
subagent_type: devops-executor-linux
description: ".claude/settings.json hook wiring"
prompt: |
  You own the ktn-linter HTTP hook entries inside /workspace/.claude/settings.json.

  Mode: {{UNINSTALL ? "uninstall" : (READ_ONLY ? "read-only" : "reconcile")}}

  Required entries (idempotency key = URL prefix "http://localhost:7717"):

    hooks.PreToolUse[]:
      { "matcher": "Edit|Write|MultiEdit",
        "hooks": [{ "type": "http",
                    "url":  "http://localhost:7717/hooks/pre-tool-use",
                    "timeout": 5 }] }

    hooks.PostToolUse[]:
      { "matcher": "Edit|Write|MultiEdit",
        "hooks": [{ "type": "http",
                    "url":  "http://localhost:7717/hooks/post-tool-use",
                    "timeout": 15 }] }

  STEP 1 — Read settings.json (create empty {} if absent and reconcile mode).
  STEP 2 — Detect drift
    For each of {pre, post}: missing-entry if no hooks-array entry contains
    any inner hook with URL starting with "http://localhost:7717/hooks/".
    DO NOT touch unrelated existing hooks (e.g. command-type git-guard, rtk,
    post-edit). Merge as a NEW item appended to the existing PreToolUse /
    PostToolUse arrays.
  STEP 3 — Act (skip in --check mode)
    Use `jq` deep-merge. NEVER overwrite the whole settings.json.
    If PreToolUse[] / PostToolUse[] don't exist yet, create them as arrays.
    If the matcher "Edit|Write|MultiEdit" already exists but lacks the HTTP
    hook, append the HTTP hook into THAT entry's "hooks" array — do NOT
    create a duplicate matcher entry.

    UNINSTALL mode: delete every inner hook whose URL startswith
    "http://localhost:7717/hooks/" from both PreToolUse and PostToolUse,
    then drop any matcher whose hooks array becomes empty:
      jq '(.hooks.PreToolUse, .hooks.PostToolUse) |= (map(.hooks |= map(select((.url? // "") | startswith("http://localhost:7717/") | not)) | select(.hooks | length > 0)))' \
        settings.json > settings.json.new && mv settings.json.new settings.json
    DO NOT touch unrelated hooks (git-guard, rtk, post-edit, …).

  STEP 4 — Verify
    `jq -e '
      (.hooks.PreToolUse  // []) | any(.hooks[]?; .url? // "" | startswith("http://localhost:7717/")) and
      (.hooks.PostToolUse // []) | any(.hooks[]?; .url? // "" | startswith("http://localhost:7717/"))
    ' settings.json`

  CRITICAL: if you write this file, the host will print a session-restart
  prompt. Claude Code re-reads settings.json only at session start.

  RETURN:
  {
    "agent": "settings",
    "status": "ok|fixed|error",
    "drift":  "none|pre-missing|post-missing|both-missing|file-missing",
    "wrote_file": true|false,
    "added_entries": ["pre"|"post"|...],
    "notes":  "..."
  }
```

### Agent D — `daemon`

```yaml
# PR4 — Skills Architecture v1.3: routed via route-agent.sh to
# devops-executor-linux instead of general-purpose. The route-agent
# call returns {subagent_type, resolved_model, effort}; pass them
# through to the Task primitive.
subagent_type: devops-executor-linux
description: "ktn-linter daemon health + freshness on :7717"
prompt: |
  You verify and (if needed) respawn the ktn-linter MCP daemon on
  127.0.0.1:7717. You DO NOT write any file.

  Mode: {{UNINSTALL ? "uninstall" : (READ_ONLY ? "read-only" : (FORCE_RESTART ? "force-restart" : "reconcile"))}}

  Canonical daemon command (matches Go feature MCP fragment in this repo):
    ktn-linter serve --port=7717
  (NOT `ktn-linter mcp serve` — the upstream entrypoint is the top-level
  `serve` subcommand defined in cmd/ktn-linter/cmd/serve.go.)

  CRITICAL: "healthy" and "running the current binary" are two INDEPENDENT
  gates. A daemon whose binary was rebuilt or replaced keeps serving from
  its in-memory image — /health still returns 200, but the analyzer code is
  frozen at the pre-rebuild commit. Linux marks this with
  `/proc/<pid>/exe → <path> (deleted)`. Reusing such a daemon means phantom
  findings against on-disk source the user has already fixed (issue #361).

  STEP 1 — Preconditions
    If `command -v ktn-linter` fails → status=skipped, reason=binary-missing,
    return immediately (Agent A will install it; you respawn on next /ktn run
    after session restart).
    Resolve BIN_REAL once for later freshness checks:
      BIN=$(command -v ktn-linter)
      BIN_REAL=$(readlink -f "$BIN" 2>/dev/null || echo "$BIN")

  STEP 2 — Probe liveness
    health = curl -fsS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:7717/health
    ready  = same on /ready
    Decide alive = (health == 200).

  STEP 3 — Discover the port owner (PID by port, NOT by pgrep pattern)
    pgrep on a command-line pattern is fragile: this template's MCP fragment
    spawns `ktn-linter serve --port 7717` (space-separated), but upstream
    install-hooks.sh spawns `ktn-linter mcp serve --port=7717`. Port
    ownership is unambiguous and covers every invocation shape.

      DAEMON_PID=""
      if command -v ss >/dev/null 2>&1; then
          DAEMON_PID=$(ss -H -ltnp 'sport = :7717' 2>/dev/null \
              | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2 || true)
      fi
      if [ -z "$DAEMON_PID" ] && command -v lsof >/dev/null 2>&1; then
          DAEMON_PID=$(lsof -tiTCP:7717 -sTCP:LISTEN 2>/dev/null | head -1 || true)
      fi

  STEP 4 — Freshness gate (independent from liveness, FAIL-CLOSED)
    # Default to STALE when alive but unverifiable. The whole point of the
    # gate is to refuse reuse of a daemon we can't prove is running the
    # current binary — silently leaving STALE=false on "I don't know" is
    # exactly the fail-open hole that issue #361 exists to close.
    STALE=false
    STALE_REASON=""
    DAEMON_EXE_RAW=""

    if [ "$alive" = "true" ]; then
        if [ -z "$DAEMON_PID" ]; then
            # Daemon answers /health but ss/lsof couldn't identify the owner
            # (e.g. busybox container with no port-discovery tools, namespace
            # boundary, locked-down /proc). We cannot verify freshness →
            # treat as stale so the reconcile path kills + respawns.
            STALE=true
            STALE_REASON="missing-exe-path"
        elif [ ! -r "/proc/$DAEMON_PID/exe" ]; then
            # PID known but /proc/<pid>/exe unreadable (perm, hidepid=2,
            # PID raced and exited). Same conclusion: cannot verify →
            # stale.
            STALE=true
            STALE_REASON="missing-exe-path"
        else
            DAEMON_EXE_RAW=$(readlink "/proc/$DAEMON_PID/exe" 2>/dev/null || true)

            if [ -z "$DAEMON_EXE_RAW" ]; then
                STALE=true
                STALE_REASON="missing-exe-path"
            else
                # Kernel marker for an unlinked inode whose process still
                # holds the old image. Canonical "rebuilt while running"
                # signature on Linux.
                case "$DAEMON_EXE_RAW" in
                    *"(deleted)"*)
                        STALE=true
                        STALE_REASON="exe-deleted"
                        ;;
                esac

                # Different on-disk paths with the same inode (hardlink /
                # install copy) are acceptable; only a real mismatch
                # triggers restart.
                if [ "$STALE" = "false" ] \
                        && [ "$DAEMON_EXE_RAW" != "$BIN_REAL" ]; then
                    if [ ! -f "$DAEMON_EXE_RAW" ] || [ ! -f "$BIN_REAL" ]; then
                        STALE=true
                        STALE_REASON="inode-mismatch"
                    else
                        d_ino=$(stat -c %i "$DAEMON_EXE_RAW" 2>/dev/null || echo a)
                        b_ino=$(stat -c %i "$BIN_REAL"       2>/dev/null || echo b)
                        if [ "$d_ino" != "$b_ino" ]; then
                            STALE=true
                            STALE_REASON="inode-mismatch"
                        fi
                    fi
                fi
            fi
        fi
    fi

  STEP 5 — Act (skip writes in --check mode)
    UNINSTALL mode: SIGTERM the actual port owner, escalate to SIGKILL if it
    lingers; do NOT respawn. Fallback to pkill only if PID discovery failed.
      if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
          kill -TERM "$DAEMON_PID" || true
          for _ in 1 2 3; do kill -0 "$DAEMON_PID" 2>/dev/null || break; sleep 1; done
          kill -0 "$DAEMON_PID" 2>/dev/null && kill -KILL "$DAEMON_PID" || true
      else
          pkill -f 'ktn-linter serve'     || true
          pkill -f 'ktn-linter mcp serve' || true   # legacy form
      fi
      return status=fixed, action=stopped.

    NEEDS_RESPAWN = (not alive) OR STALE OR force-restart
    If NEEDS_RESPAWN:
      # Read-only mode never writes/spawns — surface drift only.
      if READ_ONLY: return without acting (see RETURN below).

      # 5a. Kill the actual port owner first (TERM → KILL escalation).
      if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
          kill -TERM "$DAEMON_PID" || true
          for _ in 1 2 3; do kill -0 "$DAEMON_PID" 2>/dev/null || break; sleep 1; done
          kill -0 "$DAEMON_PID" 2>/dev/null && kill -KILL "$DAEMON_PID" || true
      else
          # Fallback if ss/lsof unavailable (e.g. busybox container).
          pkill -f 'ktn-linter serve'     || true
          pkill -f 'ktn-linter mcp serve' || true   # legacy form
      fi
      sleep 1

      # 5b. Prefer the canonical Makefile bootstrap when present (matches
      # upstream scripts/install-hooks.sh / Makefile:hooks-install).
      if [ -f "$WORKSPACE/Makefile" ] && \
         grep -qE '^hooks-install:' "$WORKSPACE/Makefile"; then
          (cd "$WORKSPACE" && make hooks-install) && return
      fi

      # 5c. Direct respawn fallback.
      nohup "$BIN" serve --port=7717 \
        >/tmp/ktn-linter-mcp.log 2>&1 < /dev/null & disown
      # Poll /health up to 5 s.
      for i in 1 2 3 4 5; do
        sleep 1
        curl -fsS --max-time 1 http://127.0.0.1:7717/health >/dev/null 2>&1 && break
      done

  STEP 6 — Smoke-test hook endpoint (only after a successful respawn or when alive=true && !STALE)
    curl -fsS -X POST http://127.0.0.1:7717/hooks/post-tool-use \
      -H 'content-type: application/json' \
      -d '{"session_id":"ktn-skill","cwd":"/workspace","tool_input":{},"tool_response":{}}' \
      > /dev/null

  RETURN:
  {
    "agent": "daemon",
    "status": "ok|fixed|skipped|error",
    "before_health": "200|503|connection-refused|...",
    "after_health":  "200|...",
    "fresh":         true|false,
    "stale_reason":  null|"exe-deleted"|"inode-mismatch"|"missing-exe-path",
    "action": "noop|respawned|forced-restart|stopped",
    "pid":    <int|null>,
    "exe":    "<DAEMON_EXE_RAW or null>",
    "bin":    "<BIN_REAL>",
    "notes":  "..."
  }

  Status decision matrix:
    - alive && !STALE && !force-restart   → status=ok,    action=noop
    - alive &&  STALE && READ_ONLY        → status=ok,    action=noop  (surfaces drift via fresh=false)
    - alive &&  STALE && !READ_ONLY       → status=fixed, action=respawned
    - !alive && !READ_ONLY                → status=fixed, action=respawned
    - force-restart && !READ_ONLY         → status=fixed, action=forced-restart
    - error during kill/respawn           → status=error, notes=<diagnostic>
```

### Agent E — `phases`

```yaml
# PR4 — Skills Architecture v1.3: routed via route-agent.sh to
# devops-executor-linux instead of general-purpose. The route-agent
# call returns {subagent_type, resolved_model, effort}; pass them
# through to the Task primitive.
subagent_type: devops-executor-linux
description: ".ktn-linter.yaml phase configuration"
prompt: |
  You manage /workspace/.ktn-linter.yaml. Upstream default active set is
  {1..7} (phase 8 = `tests` is opt-in). Don't touch the file unless a
  PHASES_SPEC was provided OR the file is invalid.

  PHASES_SPEC: {{PHASES_SPEC or "(none)"}}
  Mode: {{READ_ONLY ? "read-only" : "reconcile"}}

  STEP 1 — Read .ktn-linter.yaml if present.
    Parse `phases.enabled` and `phases.disabled`. Reject if BOTH are set
    (ErrPhasesEnabledDisabledMutex) — surface drift=mutex-violation.
    Reject legacy `max_phase:` (ErrMaxPhaseRemoved) — drift=legacy-max-phase.

    Token parser (per pkg/config/phasetoken.go):
      ints 1..9 OR canonical/variant aliases (case-insensitive):
        1 structural    2 signatures|signature  3 logic
        4 performance|perf  5 modern  6 style
        7 comment|comments  8 tests|test  9 health
      Unknown → ErrInvalidPhaseAlias.

  STEP 2 — Apply PHASES_SPEC (if any, skip in --check mode)
    "default"        → delete file (or write {version:1, phases:{enabled:[1,2,3,4,5,6,7]}})
    "all"            → phases.enabled = [1..8]
    "1-7"            → range expand
    "1,3,6"          → explicit subset
    "structural,logic,style" → alias → ints, sort, dedupe
    "+tests"         → current ∪ {8}
    "-comment"       → current \ {7}
    "show"           → DO NOT write; just report the resolved active set
                       with source precedence (CLI > YAML > default)

  STEP 3 — Validate after write
    Re-parse the file: every entry must be an int 1..9 and
    enabled/disabled cannot both be set.

  RETURN:
  {
    "agent": "phases",
    "status": "ok|fixed|error",
    "drift": "none|mutex-violation|legacy-max-phase|invalid-alias|out-of-range|none-spec-applied",
    "active_set": [1,2,3,4,5,6,7],
    "source": "default|yaml|cli",
    "wrote_file": true|false,
    "notes": "..."
  }
```

---

## Phase 3 — Synthesize results

Collect the 5 JSON payloads. Decide the final user message in this order:

1. **Any `status: "error"`** → print the agent's `notes` line by line under a
   `═══ /ktn — ERRORS` banner, then list any agent that did succeed.
   Exit non-zero in spirit (return code is informational; the user sees the report).

2. **All `status: "ok"` AND no agent reports `wrote_file: true` AND
   `daemon.action: "noop"` AND `daemon.fresh: true`** → silent OK:

   ```text
   ✓ ktn-linter healthy — nothing to do.
       binary    {version} ({path})
       mcp       registered  (key=servers|mcpServers)
       settings  pre+post hooks wired
       daemon    :7717  health=200 ready=200  fresh=yes
       phases    {active_set}  (source={default|yaml})
   ```

   When `daemon.fresh: false` is observed in `--check` mode (read-only, no
   respawn), emit the **stale daemon banner** instead — drift surfaced,
   no writes performed:

   ```text
   ⚠ ktn-linter daemon is alive but STALE
       pid       {pid}
       exe       {exe}
       bin       {bin}
       reason    {stale_reason}   # exe-deleted | inode-mismatch | missing-exe-path
       action    rerun /ktn (without --check) to kill+respawn
   ```

3. **Otherwise (any fix happened)** → consolidated report:

   ```text
   ═══════════════════════════════════════════════════════════════
     /ktn — reconcile complete
   ═══════════════════════════════════════════════════════════════

     binary    {before} → {after}   {action}
     mcp       {drift → none}        wrote={true|false}
     settings  {drift → none}        wrote={true|false}  +entries={pre,post}
     daemon    health {before → after}  fresh={yes|no→yes}  {action}  pid={pid}
     phases    active={...}  source={default|yaml|cli}  wrote={true|false}
   ```

   **Conditional restart prompt** — print ONLY when `settings.wrote_file == true`:

   ```text
   ⚠ Restart the Claude Code session to activate the new hooks.
     (settings.json is read at session start — current session is unhooked.)
   ```

   If `settings.wrote_file == false` and only `binary` / `mcp` / `daemon` /
   `phases` changed, DO NOT print the restart prompt — those don't require a
   session restart (mcp.json is read by the MCP client, phases hot-reloads via
   the daemon watcher, daemon respawn is in-place).

---

## Idempotency contract

Two consecutive `/ktn` invocations from a clean state MUST produce zero file
changes on the second run:

| Agent | Idempotency key |
|-------|----------------|
| binary | local version == latest tag |
| mcp | `.servers["ktn-linter"].args` matches `["serve"]` or `["serve","--port","7717"]` (accept `.mcpServers` on read) |
| settings | any inner hook URL startswith `http://localhost:7717/` under matcher `Edit\|Write\|MultiEdit` for both Pre and Post |
| daemon | `/health` returns 200 AND `/proc/<pid>/exe` resolves to the same inode as the on-disk `ktn-linter` binary (no `(deleted)` marker) |
| phases | `.ktn-linter.yaml` parses; no spec passed |

If you find yourself patching the same file twice in a row, you have a bug in
the drift detection — fix the detection, not the write logic.

---

## Guardrails

- **NEVER** write `/usr/local/bin/ktn-linter` without trying `sudo -n` first
  and falling back to `~/.local/bin/` / `~/bin/` (must be on `$PATH`).
- **NEVER** overwrite `mcp.json` or `.claude/settings.json` wholesale — always
  `jq`-merge to preserve unrelated entries (git-guard, rtk, post-edit, …).
- **NEVER** delete a user's `.ktn-linter.yaml` without `--phases default`.
- **NEVER** print the restart prompt unless `.claude/settings.json` was
  actually modified by Agent C.
- **NEVER** spawn the daemon with `--port` different from `7717` — that's the
  hard-coded default in upstream `cmd/ktn-linter/cmd/serve.go` and what the
  hooks point to.
- **NEVER** run `/ktn` against a binary that reports `dev` — surface a warning
  and bail out of the upgrade step (a developer rebuilding locally doesn't
  want their workspace clobbered by a release asset).
- **NEVER** run scans here. That's `/lint`. `/ktn` owns the **lifecycle**.
- **`--check`** mode is read-only: no `Write`, no `Edit`, no `nohup`, no
  `pkill`. It exits with a structured drift report only.

## Boundaries vs `/lint`

| `/ktn` (this skill) | `/lint` |
|---|---|
| Installs / upgrades the binary | Runs scans |
| Wires hooks in `.claude/settings.json` | Reads hook output |
| Configures `.ktn-linter.yaml` phases | Respects the configured phase set |
| Spawns / heals the `:7717` daemon | Calls the daemon (or falls back) |

## PR8 — Daily health probe (Skills Architecture v1.3)

```bash
# Schedules a daily health probe at 08:07 local time (avoids :00 spike).
# CronCreate is gated by PR0's primitives.json.
/ktn --schedule-daily

# Equivalent:
# CronCreate(
#   cron: "7 8 * * *",
#   prompt: "/ktn --check",
#   recurring: true,
#   durable: true
# )
```

`/ktn --check` is read-only (see boundaries above). Only emits a
`PushNotification` when drift is detected; silent on a healthy stack.
