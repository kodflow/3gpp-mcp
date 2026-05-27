# Infrastructure Validation

## Phase 3.0: Validation (--validate)

Run comprehensive validation suite:

```yaml
validation_suite:
  1_format:
    command: "terraform fmt -check -recursive"
    fix: "terraform fmt -recursive"

  2_validate:
    command: "terraform validate"

  3_tflint:
    command: "tflint --config .tflint.hcl"
    config: |
      plugin "terraform" {
        enabled = true
        preset  = "recommended"
      }

  4_tfsec:
    command: "tfsec --soft-fail"

  5_checkov:
    command: "checkov -d . --framework terraform"
    optional: true
```

**Output:**

```
═══════════════════════════════════════════════════════════════
  /infra --validate
═══════════════════════════════════════════════════════════════

  Format Check:
    └─ All files properly formatted

  Terraform Validate:
    └─ Configuration is valid

  TFLint:
    ├─ Warnings: 2
    │   ├─ Line 45: Consider using for_each instead of count
    │   └─ Line 89: Variable 'unused_var' is declared but not used
    └─ Errors: 0

  TFSec:
    ├─ Critical: 0
    ├─ High: 0
    ├─ Medium: 1
    │   └─ aws-ec2-no-public-ip: EC2 instance has public IP
    └─ Low: 3

  Overall: Validation passed (warnings present)

═══════════════════════════════════════════════════════════════
```

---

## Safety Guards

| Action | Guard |
|--------|-------|
| Destroy | ALWAYS requires confirmation |
| Apply without plan | BLOCKED |
| Apply to production | Requires `--environment production` flag |
| Sensitive resource changes | Warning + review |
| State manipulation | BLOCKED (manual only) |

### Blocked Commands

```yaml
blocked_commands:
  - "terraform state rm"
  - "terraform state mv"
  - "terraform import"
  - "terraform force-unlock"
  - "terragrunt destroy --terragrunt-non-interactive"
```
