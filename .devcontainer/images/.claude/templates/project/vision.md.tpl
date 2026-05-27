# Vision: {{PROJECT_NAME}}

## Purpose

{{PROJECT_DESCRIPTION}}

## Goals

{{#GOALS}}
{{INDEX}}. **{{.}}**
{{/GOALS}}

## Success Criteria

| Criterion | Target |
|-----------|--------|
| Test Coverage | {{COVERAGE_TARGET}} |
| Availability | {{SLA_TARGET}} |
| Response Time | {{PERFORMANCE_TARGET}} |

## Design Principles

- Progressive disclosure - Details in subdirectories
- Convention over configuration - Sensible defaults
- Security-first - Audit trails, compliance

## Non-Goals

{{#NON_GOALS}}
- {{.}}
{{/NON_GOALS}}

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| {{PRIMARY_LANGUAGE}} | Best fit for {{PROJECT_TYPE}} |
| {{PRIMARY_DATABASE}} | {{DB_RATIONALE}} |
| {{CLOUD_PROVIDER}} | Team expertise, cost optimization |
