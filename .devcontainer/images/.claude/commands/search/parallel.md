# Phase 3.0: Parallel Search (RLM Pattern: Partition + Map)

**Per PR4 — Skills Architecture v1.3, /search dispatches per-language
specialists via the router rather than the generic Explore agent.**

```bash
# Route once per concept; expand to per-language specialists via agent_template
ROUTER=~/.claude/scripts/route-agent.sh
DISPATCHES=$(bash "$ROUTER" --skill /search --phase external \
  --profile .claude/state/profile.json)

# For local-source lookups (~/.claude/docs/) use the patterns analyzer
LOCAL_DISPATCH=$(bash "$ROUTER" --skill /search --phase local \
  --profile .claude/state/profile.json)
```

Each dispatch returns `{subagent_type, resolved_model, effort}`. Hand
those values to the `Task` primitive — agents handle language-specific
documentation (Go: go.dev, Python: docs.python.org, Node: nodejs.org)
plus the local Design-Patterns KB via `docs-analyzer-patterns`.

Legacy `Explore`-based dispatch (kept for unrouted contexts):

```
Task({
  subagent_type: "Explore",
  prompt: "Search <concept> on <domain>. Extract: definition, usage, examples.",
  model: "haiku"
})
```

**IMPORTANT**: Launch ALL agents in A SINGLE message (parallel).

**Multi-agent example:**
```
// Single message with 3 Task calls
Task({ prompt: "OAuth2 on rfc-editor.org", ... })
Task({ prompt: "JWT on tools.ietf.org", ... })
Task({ prompt: "REST API on developer.mozilla.org", ... })
```

---

## Phase 4.0: Peek at Results

**Before full analysis, peek at each result:**

1. Read the first 500 characters of each response
2. Check relevance (score 0-10)
3. Filter irrelevant results (< 5)

```
Agent results:
  ✓ OAuth2 (score: 9) - RFC 6749 found
  ✓ JWT (score: 8) - RFC 7519 found
  ✗ REST (score: 3) - Result too generic
    → Relaunch with refined query
```

---

## Phase 5.0: Deep Fetch (RLM Pattern: Summarization)

**For relevant results, WebFetch with summarization:**

```
WebFetch({
  url: "<found url>",
  prompt: "Summarize in 5 key points: 1) Definition, 2) Use cases, 3) Implementation, 4) Security, 5) Examples"
})
```

**Progressive summarization:**

- Level 1: Summary per source (5 points)
- Level 2: Merge summaries (synthesis)
- Level 3: Final context (actionable)

---

## Phase 8.0: Questions (if needed)

**ONLY if ambiguity detected:**

```
AskUserQuestion({
  questions: [{
    question: "The query mentions X and Y. Which one to prioritize?",
    header: "Priority",
    options: [
      { label: "X first", description: "Focus on X" },
      { label: "Y first", description: "Focus on Y" },
      { label: "Both", description: "Full search" }
    ]
  }]
})
```

**DO NOT ask if:**

- Query is clear and unambiguous
- Single technology
- Sufficient context
