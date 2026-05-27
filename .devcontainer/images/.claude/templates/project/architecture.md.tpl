# Architecture: {{PROJECT_NAME}}

## System Context

```
{{ARCHITECTURE_DIAGRAM}}
```

## Components

| Component | Technology | Purpose |
|-----------|------------|---------|
{{#COMPONENTS}}
| {{NAME}} | {{TECH}} | {{PURPOSE}} |
{{/COMPONENTS}}

## Data Flow

{{DATA_FLOW_DESCRIPTION}}

## External Dependencies

| Service | Purpose |
|---------|---------|
{{#EXTERNAL_SERVICES}}
| {{NAME}} | {{PURPOSE}} |
{{/EXTERNAL_SERVICES}}

## Technology Stack

- **Language**: {{PRIMARY_LANGUAGE}}
- **Database**: {{DATABASES_LIST}}
- **Cloud**: {{CLOUD_LIST}}
- **Container**: {{CONTAINER_STRATEGY}}

## Security

- Secrets managed via environment variables
- MCP tokens in mcp.json (git-ignored)
- Codacy security scanning on every edit
