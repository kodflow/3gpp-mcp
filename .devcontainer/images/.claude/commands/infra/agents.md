# Specialist Agent Dispatch

## Phase 1.5: Agent Dispatch (Parallel)

**After detection, dispatch to specialized agents in parallel:**

```yaml
agent_dispatch:
  trigger: "After Phase 1.0 detection completes"

  1_detect_providers:
    action: "Grep for provider blocks in *.tf files"
    pattern: 'provider\s+"(aws|google|azurerm|oci|alicloud)"'
    output: [detected_providers]

  2_parallel_dispatch:
    mode: "single message, N Task calls"
    agents:
      infrastructure:
        always: true
        agent: "devops-specialist-infrastructure"
        focus: "IaC validation, module analysis, state management"

      security:
        always: true
        agent: "devops-specialist-security"
        focus: "tfsec findings, secret exposure, compliance"

      finops:
        condition: "--plan OR --apply"
        agent: "devops-specialist-finops"
        focus: "Cost estimation, waste detection, right-sizing"

      cloud_specialist:
        condition: "provider detected"
        routing:
          aws: "devops-specialist-aws"
          google: "devops-specialist-gcp"
          azurerm: "devops-specialist-azure"
        focus: "Provider-specific best practices, service limits"

      os_specialist:
        condition: "provisioner or user_data detected"
        routing:
          detect: "Parse target OS from AMI, image, or user_data"
          dispatch: "devops-executor-linux → os-specialist-{distro}"
        focus: "OS-level provisioning commands validation"

  3_collect_results:
    action: "Merge agent results into consolidated report"
    format: "condensed JSON per agent → unified summary"
```

**Example dispatch (AWS + security + cost):**

```
# Single message with 3 parallel Task calls:
Task(subagent_type="devops-specialist-infrastructure", prompt="Validate Terraform modules in /workspace/terraform/")
Task(subagent_type="devops-specialist-aws", prompt="Review AWS provider config and resource best practices")
Task(subagent_type="devops-specialist-security", prompt="Run security analysis on Terraform code")
```

---

## Integration with Other Skills

| Skill | Integration |
|-------|-------------|
| `/plan` | Use before `/infra --plan` for complex changes |
| `/review` | Review infrastructure code changes |
| `/git` | Commit infrastructure changes |
| `/search` | Research Terraform patterns |

---

## Examples

### Initialize and Plan

```
/infra --init --module terraform/k8s_driver
/infra --plan --module terraform/k8s_driver
```

### Validate All Modules

```
/infra --validate --all
```

### Apply with Terragrunt

```
/infra --apply --all
```

### Generate Documentation

```
/infra --docs
```
