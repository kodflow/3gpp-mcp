# Agent Drift Patterns

Skills Architecture v1.3 — living document.

This file collects signatures of agent-output drift that `/refine` and
`/review` lens-9 (scope) should flag. When the router migrates more
skills to specialists, new patterns observed in the field belong here.

## Pattern 1: "I'll just also fix…"

Specialist quietly extends the scope beyond `owned_paths_hint`. Signature
in transcript:

- "While I'm here, I'll also…"
- "I noticed unrelated…"
- file modifications outside the task contract's `owned_paths`

Remediation: reject the dispatch result; re-issue with explicit scope.

## Pattern 2: Generic answer despite specialist routing

Routed `developer-specialist-go` but reply has no `gofmt`/`golangci-lint`
references — suggests the agent fell back to general advice.

Remediation: verify `router-fallbacks.jsonl` — `fallback_used:true` means
no agent specialist was actually dispatched.

## Pattern 3: Stale model assumption

Agent answers with API examples from an SDK version older than what
`mcp__context7__*` returns. Signature: code uses deprecated entrypoints
flagged by Context7 docs as `deprecated since vX.Y`.

Remediation: skill `/refine` lens-6 (dependency) catches this; add a
fixture case here when the pattern recurs.

## Pattern 4: Over-cautious refusal

Specialist refuses to act citing "human review needed" when the change is
trivially testable. Common with security agents on low-risk patches.

Remediation: agent definition should treat "automated test exists" as a
form of human review for risk-low changes.

## Adding a pattern

Append a short numbered section: title, signature (≥2 concrete signals),
remediation. Keep each pattern under 10 lines so the file stays scannable.
