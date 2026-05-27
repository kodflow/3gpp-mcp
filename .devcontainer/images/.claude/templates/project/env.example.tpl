# {{PROJECT_NAME}} Environment Variables
# Copy to .env and fill in values

# Application
APP_NAME={{PROJECT_NAME}}
APP_ENV=development
LOG_LEVEL=debug
{{#IF_API}}
PORT=8080
HOST=0.0.0.0
{{/IF_API}}

{{#IF_POSTGRES}}
# PostgreSQL
DATABASE_URL=postgres://user:password@localhost:5432/{{PROJECT_NAME_SNAKE}}
{{/IF_POSTGRES}}

{{#IF_MONGODB}}
# MongoDB
MONGODB_URI=mongodb://localhost:27017/{{PROJECT_NAME_SNAKE}}
{{/IF_MONGODB}}

{{#IF_REDIS}}
# Redis
REDIS_URL=redis://localhost:6379
{{/IF_REDIS}}

{{#IF_AWS}}
# AWS
AWS_REGION=us-east-1
AWS_ACCESS_KEY_ID=
AWS_SECRET_ACCESS_KEY=
{{/IF_AWS}}

{{#IF_GCP}}
# GCP
GCP_PROJECT_ID=
GOOGLE_APPLICATION_CREDENTIALS=
{{/IF_GCP}}

{{#IF_AZURE}}
# Azure
AZURE_SUBSCRIPTION_ID=
AZURE_TENANT_ID=
{{/IF_AZURE}}

# MCP Tokens (auto-configured in container)
# GITHUB_TOKEN=
