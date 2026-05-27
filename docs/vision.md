# Vision: devcontainer-template

## Purpose

A universal DevContainer shell that provides a complete AI ecosystem — specialist agents, slash commands, and automated workflows — to bootstrap and develop any project with maximum quality. The developer writes intent; the system produces reliable, idiomatic, up-to-date code.

## Problem Statement

- Development environments lack deeply integrated AI that understands language-specific best practices
- Workflows don't self-improve: the same mistakes repeat across projects
- Generated code often follows outdated patterns instead of the latest stable version idioms
- Solo developers waste time on boilerplate, tooling setup, and cross-referencing documentation
- No feedback loop: code is produced without validation against official sources

## Target Users

Solo developers who want to multiply their output by delegating to a reliable AI system that:
- Produces production-quality code on first pass
- Self-corrects when results don't meet standards
- Cross-references multiple sources before committing to an approach
- Stays current with the latest stable versions of every supported language

## Goals

1. **Reliability over speed** — Every output must be correct and idiomatic before it's fast
2. **Self-correction** — Agents detect their own mistakes and iterate until quality criteria are met
3. **Source cross-referencing** — Consult official docs (context7), web search, and codebase context before producing code
4. **Latest best practices** — Agents target the current stable version of each language, not legacy patterns
5. **Universal shell** — Zero opinion on the final project; all project types bootstrap from the same base
6. **Deep reasoning** — Apply Peek, Decompose, Parallelize, Synthesize before complex actions

## Success Criteria

| Criterion | Target |
|-----------|--------|
| Container startup | < 60s on cached rebuild |
| Language support | Go, Python, Node.js, Rust, Elixir, Java, PHP, Ruby, Scala, Dart, C/C++, C#, Kotlin, Swift, Perl, Lua, R, Fortran, Ada, COBOL, Pascal, VB.NET, MATLAB, Assembly |
| Specialist agents | 25 language + 6 dev executors + 9 devops + 6 platform executors + 22 OS + 9 docs analyzers + 2 orchestrators (79 total) |
| MCP servers | GitHub, GitLab (core) + context7, ktn-linter (fragments) + Playwright (browser feature) |
| Code quality | Passes language-specific strict linting on first generation |
| Self-correction | Agents retry with fixes when linting/tests fail |
| Source validation | Agents consult context7 or official docs before generating non-trivial code |

## Design Principles

- **MCP-first** — Use structured MCP tools before CLI fallbacks; auth is pre-configured
- **Reason then act** — Deep analysis before code generation; never guess when you can verify
- **Fail forward** — When something breaks, auto-correct and learn; don't stop
- **Progressive disclosure** — Basic info at root, details in subdirectories, full specs in agents
- **Convention over configuration** — Sensible defaults that work out of the box
- **Latest stable** — Always target the current stable version, never legacy

## Non-Goals

- Not a deployment platform (development environment only)
- Not prescriptive about application architecture (the project decides)
- Not a monorepo solution (single project focus)
- Not a replacement for human judgment on design decisions

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Ubuntu 24.04 base | LTS stability, widest package support |
| Named volumes for caches | Persist tooling state across container rebuilds |
| MCP-first integrations | Structured auth, no manual token handling |
| Specialist agents per language | Each agent knows current stable version and idiomatic patterns |
| RLM decomposition | Recursive Language Model pattern for reliable multi-step reasoning |
| context7 + WebSearch | Cross-reference official docs before generating code |
| Iterative self-correction | Agents validate output and retry until quality criteria are met |
