# devcontainer-template

Coquille DevContainer universelle fournissant un ecosysteme IA complet — 35 agents specialistes, 11 commandes slash, workflows auto-correctifs — pour bootstrapper et developper n'importe quel projet avec une qualite maximale. Fiabilite d'abord : les agents raisonnent en profondeur, recoupent les sources officielles, et s'auto-corrigent jusqu'a ce que le resultat respecte les standards.

## Installation Rapide

### One-Liner (Machine Hôte ou Projet Existant)

Installez Claude Code avec **TOUS les assets** (35 agents, 11 commands, 11 scripts, 155+ patterns) en une seule commande :

```bash
curl -fsSL https://raw.githubusercontent.com/kodflow/devcontainer-template/main/.devcontainer/install.sh | bash
```

**Ce qui est installé :**
- ✅ Claude CLI (si pas déjà installé)
- ✅ 35 agents spécialisés (Go, Python, Rust, Node.js, etc.)
- ✅ 11 commandes slash (`/git`, `/review`, `/plan`, `/do`, etc.)
- ✅ 11 scripts de hooks (security, lint, format, test)
- ✅ 155+ design patterns (GoF, Cloud, DDD, Enterprise)
- ✅ Outils additionnels (rtk, status-line)

**Total :** 239 fichiers (~3.2MB) en 1-2 minutes

**Installation minimale (sans documentation) :**

```bash
curl -fsSL https://raw.githubusercontent.com/kodflow/devcontainer-template/main/.devcontainer/install.sh | bash -s -- --minimal
```

**Installation avec target personnalisé :**

```bash
DC_TARGET=/path/to/project curl -fsSL https://raw.githubusercontent.com/kodflow/devcontainer-template/main/.devcontainer/install.sh | bash
```

**Emplacements d'installation :**
- **Machine hôte :** `~/.claude/`
- **DevContainer :** `/workspace/.devcontainer/images/.claude/`

**Mise à jour ultérieure :**

```bash
# Dans Claude Code
/update

# Ou manuellement (dans DevContainer)
bash .devcontainer/install.sh
```

---

## Outils inclus

### Base
- **Ubuntu 24.04 LTS**
- **Zsh + Oh My Zsh + Powerlevel10k**
- **Git, jq, yq, curl, build-essential**

### Cloud & DevOps
| Outil | Description |
|-------|-------------|
| **AWS CLI v2** | Amazon Web Services |
| **gcloud** | Google Cloud SDK |
| **az** | Azure CLI |
| **terraform** | Infrastructure as Code |
| **vault, consul, nomad, packer** | HashiCorp Suite |
| **kubectl, helm** | Kubernetes |
| **ansible** | Configuration Management |

### Development
| Outil | Description |
|-------|-------------|
| **gh** | GitHub CLI |
| **claude** | Claude Code CLI |
| **op** | 1Password CLI |
| **bazel** | Build System |
| **task** | Taskwarrior |
| **status-line** | Claude Code status bar |

### Langages
Les langages sont ajoutés via **DevContainer Features** selon vos besoins :

```jsonc
// Dans devcontainer.json, décommenter les langages souhaités :
"features": {
  "ghcr.io/kodflow/devcontainer-features/go:1": {},
  "ghcr.io/kodflow/devcontainer-features/python:1": {},
  "ghcr.io/kodflow/devcontainer-features/rust:1": {}
}
```

25 langages disponibles sur `ghcr.io/kodflow/devcontainer-features/`

## Installation

### Nouveau projet

```bash
gh repo create mon-projet --template kodflow/devcontainer-template --public
cd mon-projet
code .
```

### Projet existant

Copiez le dossier `.devcontainer/` dans votre projet.

## Configuration MCP

Le template inclut des serveurs MCP pré-configurés pour Claude Code.

### Serveurs MCP inclus

| Serveur | Description |
|---------|-------------|
| **github** | Intégration GitHub (PR, Issues) |
| **gitlab** | Intégration GitLab (MR, Pipelines) |
| **context7** | Documentation à jour (fragment image) |
| **ktn-linter** | Linting de code (fragment image) |
| **playwright** | Tests E2E, navigateur (feature browser) |

### Configuration des tokens

**Option 1 : Variables d'environnement**

```bash
export GITHUB_API_TOKEN="ghp_xxx"
```

**Option 2 : 1Password**

Configurez `OP_SERVICE_ACCOUNT_TOKEN` et les items correspondants dans votre vault.

### Fichiers MCP

| Fichier | Description |
|---------|-------------|
| `mcp.json` | Config MCP projet (ignoré par git) |
| `.devcontainer/images/mcp.json.tpl` | Template MCP |

### ktn-linter & Claude Code Hooks

Le template intègre `ktn-linter` comme serveur MCP **et** fournisseur de hooks Claude Code. Le template déclare 3 scripts wrapper qui appellent les endpoints HTTP de ktn-linter. Dégradation gracieuse si ktn-linter n'est pas en cours d'exécution.

| Hook | Événement | Timeout | Rôle |
|------|-----------|---------|------|
| PreToolUse | Write/Edit | 5s | Contexte package avant édition |
| PostToolUse | Write/Edit | 15s | Scan fichier + blocage si violation |
| Stop | * | 30s | Validation finale des packages modifiés |

**Vérification rapide :**

```bash
which ktn-linter && curl -sf http://localhost:7717/health && echo "OK"
```

Voir [docs/ktn-linter-integration.md](docs/ktn-linter-integration.md) pour le contrat complet.

## Structure

```
.devcontainer/
├── devcontainer.json          # Configuration DevContainer
├── docker-compose.yml         # Services Docker
├── Dockerfile                 # Extends l'image de base
├── hooks/
│   └── lifecycle/
│       └── initialize.sh      # Avant création (hôte : Ollama, .env)
└── images/
    ├── Dockerfile.base        # Layer stable (apt, Cloud CLIs) — hebdo
    ├── Dockerfile             # Layer dynamique (Claude, outils) — quotidien
    ├── mcp.json.tpl           # Template MCP (+ fragments par feature)
    └── hooks/lifecycle/       # Hooks embarqués dans l'image
```

## Commandes

### Rebuild container

```bash
# VS Code
Cmd+Shift+P > "Dev Containers: Rebuild Container"
```

### Claude avec MCP

```bash
# Alias configuré automatiquement
super-claude
```

### Nettoyer

```bash
docker compose -f .devcontainer/docker-compose.yml down -v
```

## Volumes persistants

- `zsh-history` : Historique shell
- `package-cache` : Caches packages (npm, pip, cargo, etc.)
- `claude-config` : Configuration Claude (credentials, sessions)
- `cloud-config` : Config cloud (AWS, GCP, Azure, 1Password)

## License

MIT
