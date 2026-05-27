# Terraform/Terragrunt Workflows

## Phase 1.0: Detection

Detect infrastructure tool and configuration:

```yaml
detection:
  terraform:
    files: ["*.tf", "terraform.tfvars"]
    tool: "terraform"

  terragrunt:
    files: ["terragrunt.hcl"]
    tool: "terragrunt"

  opentofu:
    files: ["*.tf", ".terraform-version"]
    check: "tofu version"
    tool: "tofu"
```

**Output:**

```
═══════════════════════════════════════════════════════════════
  /infra - Infrastructure Detection
═══════════════════════════════════════════════════════════════

  Directory: /workspace/terraform/k8s_driver

  Detected:
    ├─ Tool: Terragrunt (terragrunt.hcl found)
    ├─ Backend: Consul
    └─ Modules: 5 referenced

  Dependencies:
    ├─ ../infrastructure (required)
    └─ ../vault (required)

═══════════════════════════════════════════════════════════════
```

---

## Phase 2.0: Secret Discovery (1Password)

**Check 1Password for infrastructure secrets before any operation:**

```yaml
infra_secret_discovery:
  trigger: "Before --plan, --apply, --validate"
  blocking: false  # Informatif seulement

  1_check_1password:
    condition: "command -v op && test -n $OP_SERVICE_ACCOUNT_TOKEN"
    on_failure: "Skip (1Password not configured)"

  2_resolve_project_path:
    action: "Extract org/repo from git remote"

  3_scan_tfvars:
    action: "Detect variables referenced in *.tf and *.tfvars"
    command: 'grep -rh "variable\s" *.tf | sed "s/variable\s*\"/TF_VAR_/;s/\".*//"'
    output: "required_vars[]"

  4_check_vault_for_secrets:
    action: "List 1Password items matching project path + TF_VAR_ prefix"
    command: |
      op item list --vault='$VAULT_ID' --format=json \
        | jq -r '.[] | select(.title | startswith("'$PROJECT_PATH'/")) | .title'
    filter: "Items matching TF_VAR_*, AWS_*, AZURE_*, GCP_*"

  5_cross_path_check:
    action: "Also check shared-infra path for common secrets"
    paths:
      - "${ORG}/shared-infra"
      - "${ORG}/infrastructure"
    match: "AWS_*, AZURE_*, GCP_*, TF_VAR_*"

  6_output:
    if_secrets_found: |
      ═══════════════════════════════════════════════════════════════
        /infra - 1Password Secrets Available
      ═══════════════════════════════════════════════════════════════

        Project secrets ({PROJECT_PATH}):
          ├─ TF_VAR_db_password
          └─ TF_VAR_api_key

        Shared secrets ({ORG}/shared-infra):
          ├─ AWS_CREDENTIALS
          └─ TF_VAR_region

        Use /secret --get <key> to retrieve
        Or /secret --get <key> --path <org>/shared-infra

      ═══════════════════════════════════════════════════════════════
    if_no_secrets: "(no infra secrets found in 1Password)"
```

---

## Phase 4.0: Plan (--plan)

Generate and analyze execution plan:

```yaml
plan_workflow:
  1_init:
    condition: ".terraform not exists OR --force-init"
    command: "terraform init -upgrade"

  2_plan:
    command: "terraform plan -out=tfplan"
    output: "tfplan"

  3_analyze:
    action: "Parse plan for changes"
    categories:
      - create
      - update
      - replace
      - destroy

  4_security_review:
    action: "Check for sensitive changes"
    warn_on:
      - "aws_iam_*"
      - "vault_*_secret*"
      - "*_password*"
      - "*_token*"
```

**Output:**

```
═══════════════════════════════════════════════════════════════
  /infra --plan
═══════════════════════════════════════════════════════════════

  Module: terraform/k8s_driver

  Changes Summary:
    ├─ Create: 3
    │   ├─ kubernetes_deployment.app
    │   ├─ kubernetes_service.app
    │   └─ kubernetes_config_map.config
    ├─ Update: 1
    │   └─ helm_release.cilium (values changed)
    ├─ Replace: 0
    └─ Destroy: 0

  Resource Details:
    + kubernetes_deployment.app
      + metadata.name = "my-app"
      + spec.replicas = 3

    ~ helm_release.cilium
      ~ values = (sensitive)

  Security Review:
    └─ No sensitive resources modified

  Plan saved to: tfplan

  Next: Run `/infra --apply` to apply these changes

═══════════════════════════════════════════════════════════════
```

---

## Phase 5.0: Apply (--apply)

Apply changes with safety checks:

```yaml
apply_workflow:
  1_verify_plan:
    condition: "tfplan file exists"
    action: "Verify plan is current"

  2_confirmation:
    condition: "NOT --auto-approve"
    tool: AskUserQuestion
    question: "Apply these changes?"

  3_apply:
    command: "terraform apply tfplan"

  4_verify:
    command: "terraform show"
    action: "Verify resources created"

  5_cleanup:
    action: "Remove tfplan file"
```

### Monitor stream (PR5b — Skills Architecture v1.3)

When `Monitor` primitive is `present` (per PR0), wrap the `terraform
apply` invocation so progress events stream as notifications instead of
filling the agent transcript with raw lines:

```
Monitor(
  description: "terraform apply <module>",
  timeout_ms: 1800000,
  command: "terraform apply -auto-approve tfplan 2>&1 | grep --line-buffered -E '^(Apply complete|Error:|Plan:|\\s+\\w+\\.\\w+:.*[Cc]reat|\\s+\\w+\\.\\w+:.*[Dd]estroy)'"
)
```

Emit one `PushNotification` on terminal state (success / error / drift detected).

**Output:**

```
═══════════════════════════════════════════════════════════════
  /infra --apply
═══════════════════════════════════════════════════════════════

  Module: terraform/k8s_driver
  Plan: tfplan (generated 5m ago)

  Applying changes...

  Progress:
    [====================] 100%

  Applied:
    kubernetes_deployment.app (created)
    kubernetes_service.app (created)
    kubernetes_config_map.config (created)
    helm_release.cilium (updated)

  Summary:
    ├─ Created: 3
    ├─ Updated: 1
    ├─ Destroyed: 0
    └─ Duration: 45s

  State: terraform.tfstate updated

═══════════════════════════════════════════════════════════════
```

---

## Phase 6.0: Documentation (--docs)

Generate documentation with terraform-docs:

```yaml
docs_workflow:
  1_find_modules:
    command: "find . -name '.terraform-docs.yml'"

  2_generate:
    for_each: module
    command: "terraform-docs --config .terraform-docs.yml ."

  3_verify:
    command: "terraform-docs --output-check"
```

**Output:**

```
═══════════════════════════════════════════════════════════════
  /infra --docs
═══════════════════════════════════════════════════════════════

  Generating documentation...

  Modules processed:
    ├─ terraform/_modules/networking  README.md updated
    ├─ terraform/_modules/kubernetes  README.md updated
    ├─ terraform/_modules/vault       README.md updated
    └─ terraform/_modules/openstack   README.md updated

  Summary: 4 modules documented

═══════════════════════════════════════════════════════════════
```

---

## Terragrunt Support

### run-all Commands

```yaml
terragrunt_commands:
  plan_all:
    command: "terragrunt run-all plan"

  apply_all:
    command: "terragrunt run-all apply"
    options:
      - "--terragrunt-parallelism 3"

  destroy_all:
    command: "terragrunt run-all destroy"
    confirmation: MANDATORY
```

### Dependency Graph

```yaml
dependency_analysis:
  command: "terragrunt graph-dependencies"
  output: "Show dependency tree"
```

---

## Configuration

### .infra.yml (optional)

```yaml
# Project-specific infrastructure configuration

default_tool: terragrunt
modules_path: terraform/_modules

validation:
  tflint: true
  tfsec: true
  checkov: false

environments:
  production:
    require_approval: true
    backend: consul
  staging:
    require_approval: false
    backend: local
```
