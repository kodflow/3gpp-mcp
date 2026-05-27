# Go Linting Gateway

> Invoked from lint.md when Go is detected with ktn-linter available.

## Prerequisites

1. Verify `go.mod` exists in project root
2. Check for ktn-linter binary:

```bash
# Check binary
ls ./builds/ktn-linter 2>/dev/null

# If missing, build it
if [ -d ./cmd/ktn-linter ]; then
    go build -o ./builds/ktn-linter ./cmd/ktn-linter
fi
```

3. If ktn-linter not available (no binary, no source): fall back to `lint/generic.md` with `golangci-lint`

## Execution

Once ktn-linter is confirmed available, execute the full 8-phase workflow:

1. **Run ktn-linter**: Refer to `execution.md` Step 1
2. **Parse & classify**: Refer to `rules.md` for phase mapping
3. **DTO detection**: Refer to `dto.md` when KTN-STRUCT-ONEFILE or KTN-STRUCT-CTOR
4. **Execute fixes**: Refer to `execution.md` for Agent Teams or sequential mode
5. **Re-run until convergence**: Refer to `execution.md` final verification

## Quick Reference

| Phase | Category | Rules | Mode |
|-------|----------|-------|------|
| 1 | STRUCTURAL | 7 | Lead (sequential) |
| 2 | SIGNATURES | 7 | Lead (sequential) |
| 3 | LOGIC | 17 | Lead (sequential) |
| 4 | PERFORMANCE | 11 | Teammate "perf" |
| 5 | MODERN | 20 | Teammate "modern" |
| 6 | STYLE | 13 | Teammate "polish" |
| 7 | DOCS | 8 | Teammate "polish" |
| 8 | TESTS | 8 | Teammate "tester" |

## Module Reference

| Action | Module |
|--------|--------|
| All 148 rules by phase | Read ~/.claude/commands/lint/rules.md |
| Execution workflow & agent teams | Read ~/.claude/commands/lint/execution.md |
| DTO convention & detection | Read ~/.claude/commands/lint/dto.md |
