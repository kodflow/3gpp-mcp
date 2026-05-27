# Phase 7.0: Final Synthesis

## Path Resolution (MANDATORY)

All `.claude/` paths MUST be absolute: `${WORKSPACE_ROOT}/.claude/plans/` (resolve via `git rev-parse --show-toplevel || echo /workspace`).

---

## Success Report

```
═══════════════════════════════════════════════════════════════
  /do - Task Completed Successfully
═══════════════════════════════════════════════════════════════

  Task       : {original_task}
  Iterations : {n}/{max}

  ✓ All Criteria Met:
    - Tests: 23/23 PASS
    - Lint: 0 errors
    - Build: SUCCESS

  Files Modified ({count}):
    - package.json (+3, -3)
    - vitest.config.ts (+25, -0)
    - src/**/*.test.ts (23 files)

  Decomposition Results:
    ✓ [DEPS] Replaced dependencies
    ✓ [CONFIG] Created vitest config
    ✓ [IMPORTS] Adapted 23 test files
    ✓ [COMPAT] Fixed mock APIs
    ✓ [VERIFY] All tests pass

═══════════════════════════════════════════════════════════════
  IMPORTANT: Review the diff before merging!
  → git diff HEAD~{n}
═══════════════════════════════════════════════════════════════
```

## Failure Report

```
═══════════════════════════════════════════════════════════════
  /do - Task Stopped (Max Iterations / Blocker)
═══════════════════════════════════════════════════════════════

  Task       : {original_task}
  Iterations : {n}/{max}
  Reason     : {MAX_REACHED | BLOCKER_DETECTED | CIRCULAR_FIX}

  ✗ Criteria NOT Met:
    - Tests: 20/23 PASS (3 failing)
    - Lint: 0 errors

  Blockers Identified:
    1. tests/api.test.ts:45 - Cannot mock external service
    2. tests/db.test.ts:78 - Database connection required

  Decomposition Status:
    ✓ [DEPS] Replaced dependencies
    ✓ [CONFIG] Created vitest config
    ✓ [IMPORTS] Adapted 23 test files
    ✗ [COMPAT] 3 incompatible mocks
    ✗ [VERIFY] Tests failing

  Suggested Next Steps:
    1. Review failing tests manually
    2. Consider mocking strategy for external services
    3. Re-run with narrower scope

═══════════════════════════════════════════════════════════════
```

---

## Integration with /review (Cyclic Workflow)

**`/review --loop` generates plans that `/do` executes automatically.**

```yaml
review_integration:
  detection:
    trigger: "plan filename contains 'review-fixes-'"
    location: ".claude/plans/review-fixes-*.md"

  mode: "REVIEW_EXECUTION"

  workflow:
    1_load_plan:
      action: "Read .claude/plans/review-fixes-{timestamp}.md"
      extract:
        - findings: [{file, line, fix_patch, language, specialist}]
        - priorities: ["CRITICAL", "HIGH", "MEDIUM"]

    2_group_by_language:
      action: "Group findings by file extension"
      example:
        ".go": ["finding1", "finding2"]
        ".ts": ["finding3"]

    3_dispatch_to_specialists:
      mode: "parallel (by language)"
      for_each_language:
        agent: "developer-specialist-{lang}"
        prompt: |
          You are the {language} specialist.

          ## Findings to Fix
          {findings_json}

          ## Constraints
          - Apply fixes in priority order (CRITICAL → HIGH)
          - Use fix_patch as starting point
          - Verify fix doesn't introduce new issues
          - Follow repo conventions

          ## Output
          For each fix applied:
          - File modified
          - Lines changed
          - Brief explanation

    4_validate:
      action: "Run quick /review (no loop) on modified files"
      check:
        - "Were original issues from the plan fixed?"
        - "Were any new CRITICAL/HIGH issues introduced?"

    5_report:
      action: "Summary of fixes applied"
      format: |
        Files modified: {n}
        Findings fixed: CRIT={a}, HIGH={b}, MED={c}
        New issues: {new_count}

    6_return_to_review:
      condition: "Called from /review --loop"
      action: "Return control to /review for re-validation"
```

**Language-Specialist Routing:**

| Extension | Specialist Agent |
|-----------|------------------|
| `.go` | `developer-specialist-go` |
| `.py` | `developer-specialist-python` |
| `.java` | `developer-specialist-java` |
| `.ts`, `.js` | `developer-specialist-nodejs` |
| `.rs` | `developer-specialist-rust` |
| `.rb` | `developer-specialist-ruby` |
| `.ex`, `.exs` | `developer-specialist-elixir` |
| `.php` | `developer-specialist-php` |
| `.c`, `.h` | `developer-specialist-c` |
| `.cpp`, `.cc`, `.hpp` | `developer-specialist-cpp` |
| `.cs` | `developer-specialist-csharp` |
| `.kt`, `.kts` | `developer-specialist-kotlin` |
| `.swift` | `developer-specialist-swift` |
| `.r`, `.R` | `developer-specialist-r` |
| `.pl`, `.pm` | `developer-specialist-perl` |
| `.lua` | `developer-specialist-lua` |
| `.f90`, `.f95`, `.f03` | `developer-specialist-fortran` |
| `.adb`, `.ads` | `developer-specialist-ada` |
| `.cob`, `.cbl` | `developer-specialist-cobol` |
| `.pas`, `.dpr`, `.pp` | `developer-specialist-pascal` |
| `.vb` | `developer-specialist-vbnet` |
| `.m` (Octave) | `developer-specialist-matlab` |
| `.asm`, `.s` | `developer-specialist-assembly` |
| `.scala` | `developer-specialist-scala` |
| `.dart` | `developer-specialist-dart` |

**Infrastructure/SysAdmin Task Routing:**

When the task involves infrastructure, system administration, or OS-level operations,
dispatch to the appropriate DevOps agents:

| Task Pattern | Agent | Dispatch |
|-------------|-------|----------|
| Terraform, IaC, cloud resources | `devops-orchestrator` | Coordinates infra specialists |
| Docker, containers, images | `devops-specialist-docker` | Container optimization |
| Kubernetes, Helm, K8s | `devops-specialist-kubernetes` | K8s orchestration |
| Security scanning, CVEs | `devops-specialist-security` | Vulnerability detection |
| Cost optimization, FinOps | `devops-specialist-finops` | Cloud cost analysis |
| AWS services | `devops-specialist-aws` | AWS best practices |
| GCP services | `devops-specialist-gcp` | GCP best practices |
| Azure services | `devops-specialist-azure` | Azure best practices |
| HashiCorp (Vault, Consul) | `devops-specialist-hashicorp` | HashiCorp stack |
| Linux sysadmin | `devops-executor-linux` | Routes to OS specialist |
| BSD sysadmin | `devops-executor-bsd` | Routes to BSD specialist |
| macOS sysadmin | `devops-executor-osx` | Routes to macOS specialist |
| Windows sysadmin | `devops-executor-windows` | Routes to Windows specialist |
| QEMU/KVM VMs | `devops-executor-qemu` | VM management |
| VMware vSphere | `devops-executor-vmware` | VMware operations |

**OS Executor Routing Chain:**

```
Task detected as OS-level
  → devops-executor-{linux|bsd|osx|windows}  (router)
    → os-specialist-{distro}                   (specialist)
      → Returns condensed JSON
    ← Merged into task result
```

---

## Integration with Other Skills

| Before /do | After /do |
|-----------|-----------|
| `/plan` (optional but recommended) | `/git --commit` |
| `/review` (generates plan) | `/review` (re-validate if --loop) |
| `/search` (if research needed) | N/A |

**Recommended workflow (standard plan):**

```
/search "vitest migration from jest"  # If research needed
    ↓
/plan "Migrate Jest tests"            # Plan the approach
    ↓
(user approves plan)                   # Human validation
    ↓
/do                                    # Detects the plan → executes
    ↓
(review diff)                          # Verify changes
    ↓
/git --commit                          # Commit + PR
```

**Cyclic workflow (with /review --loop):**

```
/review --loop 5                       # Analyze + generate fix plan
    ↓
/do (auto-triggered)                   # Execute via language-specialists
    ↓
/review (auto-triggered)               # Re-validate corrections
    ↓
(loop until no CRITICAL/HIGH OR limit)
    ↓
/git --commit                          # Commit corrections
```

**Quick workflow (without plan):**

```
/do "Fix all lint bugs"               # Simple + measurable task
    ↓
(iterations until success)
    ↓
/git --commit
```

**Note**: `/do` replaces `/apply`. The `/apply` skill is deprecated.
