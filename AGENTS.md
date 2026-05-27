# Specialist Agents

86 agents organized in 8 categories (incl. 5 new from PR2b: react, postgres, playwright, cloudflare, github-actions). All agents return condensed JSON to protect parent context.

## Orchestrators (2)

| Agent | Model | Purpose | Invoked By |
|-------|-------|---------|------------|
| `developer-orchestrator` | opus | Code review, refactoring, testing coordination | `/review`, `/do` |
| `devops-orchestrator` | opus | Infrastructure, security, cost, sysadmin coordination | `/infra`, `/do` |

## Language Specialists (25)

Each targets the **current stable version** and consults context7/official docs before generating code.

| Agent | Expertise | Min Version |
|-------|-----------|-------------|
| `developer-specialist-go` | Idiomatic Go, concurrency, error handling | 1.26+ |
| `developer-specialist-python` | Type hints, async, mypy strict, ruff | 3.14+ |
| `developer-specialist-nodejs` | TypeScript strict, ESLint, async patterns | 25+ |
| `developer-specialist-rust` | Ownership, lifetimes, clippy pedantic | 1.92+ |
| `developer-specialist-java` | Virtual threads, records, sealed classes | 25+ |
| `developer-specialist-csharp` | Nullable refs, async/await, Roslyn analyzers | 13+ |
| `developer-specialist-cpp` | C++23/26, concepts, coroutines, Clang-Tidy | C++23 |
| `developer-specialist-c` | Memory safety, UB prevention, C23 | C23 |
| `developer-specialist-php` | Strict typing, attributes, PHPStan max | 8.5+ |
| `developer-specialist-ruby` | ZJIT, Ractors, RuboCop, Sorbet | 4.0+ |
| `developer-specialist-elixir` | OTP, GenServer, LiveView, Dialyzer | 1.19+ |
| `developer-specialist-kotlin` | Null safety, coroutines, ktlint, Detekt | 2.2+ |
| `developer-specialist-swift` | Actors, structured concurrency, SwiftLint | 6+ |
| `developer-specialist-scala` | Context functions, opaque types, Scalafix | 3.7+ |
| `developer-specialist-dart` | Sound null safety, Flutter, dart analyze | 3.10+ |
| `developer-specialist-perl` | Modern Perl, Moose/Moo, Perl::Critic | 5.40+ |
| `developer-specialist-lua` | Metatables, coroutines, Luacheck, Busted | 5.4+ |
| `developer-specialist-r` | Tidyverse, S4/R6, lintr, testthat | 4.4+ |
| `developer-specialist-fortran` | Modern Fortran 2023, coarrays, fpm | F2023 |
| `developer-specialist-ada` | Ada 2022, SPARK, contracts, tasking | Ada 2022 |
| `developer-specialist-cobol` | COBOL 2014, COPY books, GnuCOBOL | 2014 |
| `developer-specialist-pascal` | Object Pascal, units, generics, FPC | FPC 3.2+ |
| `developer-specialist-vbnet` | Option Strict, LINQ, nullable, Roslyn | VB 17+ |
| `developer-specialist-matlab` | Vectorized ops, signal processing, Octave | R2024+ |
| `developer-specialist-assembly` | x86_64, syscalls, registers, linking | x86_64 |

## Developer Executors (6)

| Agent | Task | Invoked By |
|-------|------|------------|
| `developer-specialist-review` | Code review orchestration (5 sub-executors) | `/review` |
| `developer-executor-correctness` | Invariants, state machines, concurrency bugs | `developer-specialist-review` |
| `developer-executor-security` | Taint analysis, OWASP Top 10, secrets detection | `developer-specialist-review` |
| `developer-executor-design` | Patterns, SOLID, DDD violations | `developer-specialist-review` |
| `developer-executor-quality` | Complexity, code smells, maintainability | `developer-specialist-review` |
| `developer-executor-shell` | Shell, Dockerfile, CI/CD safety | `developer-specialist-review` |

## DevOps Specialists (9)

| Agent | Domain | Invoked By |
|-------|--------|------------|
| `devops-specialist-infrastructure` | Terraform, OpenTofu, IaC | `devops-orchestrator`, `/infra` |
| `devops-specialist-security` | Vulnerability scanning, compliance | `devops-orchestrator`, `/infra` |
| `devops-specialist-finops` | Cost optimization, right-sizing | `devops-orchestrator`, `/infra` |
| `devops-specialist-docker` | Dockerfile optimization, Compose, security | `devops-orchestrator` |
| `devops-specialist-kubernetes` | K8s, Helm, GitOps, operators | `devops-orchestrator` |
| `devops-specialist-hashicorp` | Vault, Consul, Nomad, Packer | `devops-orchestrator` |
| `devops-specialist-aws` | EC2, EKS, IAM, VPC, Lambda | `devops-orchestrator`, `/infra` |
| `devops-specialist-gcp` | GCE, GKE, IAM, BigQuery | `devops-orchestrator`, `/infra` |
| `devops-specialist-azure` | VMs, AKS, RBAC, Key Vault | `devops-orchestrator`, `/infra` |

## DevOps Executors / Routers (6)

Executors detect the target OS and **route to the appropriate OS specialist**.

| Agent | Routing | Dispatch Target |
|-------|---------|-----------------|
| `devops-executor-linux` | `/etc/os-release` ID field | `os-specialist-{distro}` (15 distros) |
| `devops-executor-bsd` | `uname -s` | `os-specialist-{freebsd,openbsd,netbsd,dragonflybsd}` |
| `devops-executor-osx` | Darwin detected | `os-specialist-macos` |
| `devops-executor-windows` | ProductType (1=Desktop, 3=Server) | `os-specialist-windows-{server,desktop}` |
| `devops-executor-qemu` | QEMU/KVM, libvirt | Direct execution (no sub-routing) |
| `devops-executor-vmware` | vSphere, ESXi | Direct execution (no sub-routing) |

## OS Specialists (22)

Each agent knows its OS's package manager, init system, kernel, security model, and official documentation URLs. All return **condensed JSON**.

### Linux (15)

| Agent | Distro | Pkg Manager | Init System |
|-------|--------|-------------|-------------|
| `os-specialist-debian` | Debian 13 Trixie | apt/dpkg | systemd |
| `os-specialist-ubuntu` | Ubuntu 24.04 LTS | apt/snap | systemd |
| `os-specialist-fedora` | Fedora 43 | dnf5 | systemd |
| `os-specialist-rhel` | RHEL/CentOS/Rocky/Alma | dnf/yum | systemd |
| `os-specialist-arch` | Arch Linux (rolling) | pacman/AUR | systemd |
| `os-specialist-alpine` | Alpine 3.23 | apk | OpenRC/s6 |
| `os-specialist-opensuse` | openSUSE Leap/Tumbleweed | zypper/YaST | systemd |
| `os-specialist-void` | Void Linux (rolling) | xbps | runit |
| `os-specialist-devuan` | Devuan 6 Excalibur | apt/dpkg | sysvinit/OpenRC |
| `os-specialist-artix` | Artix Linux | pacman | dinit/runit/s6/66 |
| `os-specialist-gentoo` | Gentoo | portage/emerge | OpenRC/systemd |
| `os-specialist-nixos` | NixOS 25.05 | nix | systemd (declarative) |
| `os-specialist-manjaro` | Manjaro | pacman/pamac | systemd |
| `os-specialist-kali` | Kali Rolling | apt | systemd |
| `os-specialist-slackware` | Slackware 15.0 | slackpkg/sbopkg | BSD-style rc |

### BSD (4)

| Agent | OS | Pkg Manager | Key Features |
|-------|-----|-------------|-------------|
| `os-specialist-freebsd` | FreeBSD 15.0 | pkg/ports | ZFS, jails, pf, bhyve |
| `os-specialist-openbsd` | OpenBSD 7.8 | pkg_add | pledge/unveil, pf, W^X |
| `os-specialist-netbsd` | NetBSD 10.1 | pkgsrc/pkgin | NPF, rump kernels |
| `os-specialist-dragonflybsd` | DragonFly 6.4 | pkg/dports | HAMMER2, vkernel |

### Other (3)

| Agent | OS | Pkg Manager | Key Features |
|-------|-----|-------------|-------------|
| `os-specialist-macos` | macOS 16 Tahoe | Homebrew/mas | launchd, APFS, SIP |
| `os-specialist-windows-server` | Windows Server 2025 | winget/choco | AD, IIS, Hyper-V |
| `os-specialist-windows-desktop` | Windows 11 24H2 | winget/scoop | WSL2, winget, Store |

## Documentation Analyzers (9)

| Agent | Purpose | Invoked By |
|-------|---------|------------|
| `docs-analyzer-structure` | Project structure mapper | `/docs` |
| `docs-analyzer-config` | Configuration inventory | `/docs` |
| `docs-analyzer-commands` | Slash commands inventory | `/docs` |
| `docs-analyzer-agents` | Agent types inventory | `/docs` |
| `docs-analyzer-hooks` | Lifecycle hooks inventory | `/docs` |
| `docs-analyzer-languages` | Language features inventory | `/docs` |
| `docs-analyzer-mcp` | MCP server inventory | `/docs` |
| `docs-analyzer-patterns` | Design patterns inventory | `/docs` |
| `docs-analyzer-architecture` | Deep architecture analysis (C4) | `/docs` |

## Meta Agents (2)

| Agent | Model | Purpose | Invoked By |
|-------|-------|---------|------------|
| `developer-commentator` | opus | Orchestrate comment auditing across entire project | `/comment` |
| `developer-commentator-worker` | haiku | Audit/fix comments in a single file (WHY not WHAT) | `developer-commentator` |

## Routing Chains

```text
/review → developer-specialist-review (sonnet)
            → developer-executor-correctness (sonnet)
            → developer-executor-security (opus)
            → developer-executor-design (sonnet)
            → developer-executor-quality (haiku)
            → developer-executor-shell (haiku)

/do, /plan → developer-orchestrator (opus)
               → developer-specialist-{lang} (sonnet)

/infra → devops-orchestrator (opus)
           → devops-specialist-{domain} (sonnet)
           → devops-executor-{platform} (haiku, router)
             → os-specialist-{distro} (haiku)

/docs → docs-analyzer-structure (haiku)
          → docs-analyzer-{aspect} (haiku) × 8

/comment → developer-commentator (opus)
             → developer-commentator-worker (haiku) × N files
```

## Decision Tree: Which Agent For My Task?

| I want to... | Agent | Model |
|--------------|-------|-------|
| Review code changes | `developer-specialist-review` | sonnet |
| Write Go/Python/Rust/etc. code | `developer-specialist-{lang}` | sonnet |
| Fix security vulnerabilities | `developer-executor-security` | opus |
| Analyze code complexity | `developer-executor-quality` | haiku |
| Check shell/Dockerfile safety | `developer-executor-shell` | haiku |
| Provision cloud infrastructure | `devops-specialist-{aws,gcp,azure}` | sonnet |
| Configure Kubernetes | `devops-specialist-kubernetes` | sonnet |
| Manage OS packages/services | `os-specialist-{distro}` | haiku |
| Audit/fix code comments | `developer-commentator` | opus |
| Generate project documentation | `docs-analyzer-structure` | haiku |
| Optimize cloud costs | `devops-specialist-finops` | sonnet |
| Scan for secrets/compliance | `devops-specialist-security` | sonnet |

## Agent Behavior

Agents must:
- Consult context7 or official docs before generating non-trivial code
- Target the current stable version of each language/OS
- Validate output against strict linting before returning
- Self-correct when linting or tests fail
- Return structured JSON for orchestrators to process
- Ask permission before destructive operations

## Registry

Machine-readable agent catalog: `~/.claude/agents/registry.json`
