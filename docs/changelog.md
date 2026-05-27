# Changelog

Notable changes to the devcontainer-template, generated from conventional commits.

## Recent

### Breaking changes

- **devcontainer**: Removed `grepai` + `ollama` semantic-search stack (high CPU/RAM
  cost for marginal benefit). RTK PreToolUse rewrite is now the canonical
  token-savings mechanism. `/update` propagates the cleanup to downstream
  projects (deletes `.grepai/`, `grepai.config.yaml`, runtime MCP entry, kills
  orphan daemons). Action: rebuild image without cache; optionally
  `brew uninstall ollama` / `sudo apt purge ollama` if no longer used elsewhere.

### Features

- **agents**: Add parallel-safe plan/context files and OS specialist agents ([#205](https://github.com/kodflow/devcontainer-template/pull/205))
- **infra**: Add infrastructure feature and remove all hardcoded versions ([#203](https://github.com/kodflow/devcontainer-template/pull/203))
- **hooks**: Spec-compliant output format, experimental flags, and 17 hook improvements ([#212](https://github.com/kodflow/devcontainer-template/pull/212))
- **skills**: Add MCP integration, context persistence, and Kilo Code patterns ([#210](https://github.com/kodflow/devcontainer-template/pull/210))
- **update**: Profile-aware sync with git tarball download ([#201](https://github.com/kodflow/devcontainer-template/pull/201))
- **skills**: Enrich 6 skills with 16 targeted improvements ([#199](https://github.com/kodflow/devcontainer-template/pull/199))
- **hooks**: Delegate lifecycle hooks to image-embedded scripts ([#187](https://github.com/kodflow/devcontainer-template/pull/187))
- **versions**: Convert all version pins to dynamic latest resolution ([#183](https://github.com/kodflow/devcontainer-template/pull/183))
- **vpn**: Add multi-protocol VPN clients and network tools ([#174](https://github.com/kodflow/devcontainer-template/pull/174))
- **languages**: Add cross-language parity for 26 languages ([#180](https://github.com/kodflow/devcontainer-template/pull/180))

### Fixes

- **kubernetes**: Move color definitions before first use ([#215](https://github.com/kodflow/devcontainer-template/pull/215))
- **devcontainer**: Apply 66 optimization fixes across batches 2-5 ([#213](https://github.com/kodflow/devcontainer-template/pull/213))
- **ci**: Replace release-please with simple release workflow ([#211](https://github.com/kodflow/devcontainer-template/pull/211))
- **install**: Add `|| true` to 1Password token checks ([#209](https://github.com/kodflow/devcontainer-template/pull/209))
- **install**: Prevent set -e exit on successful downloads ([#207](https://github.com/kodflow/devcontainer-template/pull/207))
- **install**: Replace file command with tar validation and add release pipeline ([#206](https://github.com/kodflow/devcontainer-template/pull/206))
- **hooks**: Skip shared directory in feature validation ([#204](https://github.com/kodflow/devcontainer-template/pull/204))
- **grepai**: Clean stale index.gob.lock on daemon start ([#192](https://github.com/kodflow/devcontainer-template/pull/192))
- **hooks**: Prevent exit code 1 leak and improve grepai observability ([#191](https://github.com/kodflow/devcontainer-template/pull/191))
- **grepai**: Launch watchdog even when Ollama unavailable at startup ([#190](https://github.com/kodflow/devcontainer-template/pull/190))
- **hooks**: Remove executable bit from sourced utils.sh ([#189](https://github.com/kodflow/devcontainer-template/pull/189))
- **grepai**: Add multi-factor health stamp and watchdog for robust init ([#185](https://github.com/kodflow/devcontainer-template/pull/185))
- **hooks**: Non-blocking lifecycle scripts with run_step pattern ([#175](https://github.com/kodflow/devcontainer-template/pull/175))

### Documentation

- **docs**: Rewrite documentation with practical usage guide ([#202](https://github.com/kodflow/devcontainer-template/pull/202))

### Maintenance

- **deps**: Bump docker/build-push-action from 6.19.1 to 6.19.2 ([#214](https://github.com/kodflow/devcontainer-template/pull/214))

### Performance

- **docker**: Consolidate Dockerfile layers from ~37 to 15 ([#184](https://github.com/kodflow/devcontainer-template/pull/184))
- **features**: Optimize all 25 language install.sh scripts ([#193](https://github.com/kodflow/devcontainer-template/pull/193))

### Documentation

- **commands**: Add interview mode and complexity check ([#198](https://github.com/kodflow/devcontainer-template/pull/198))
- **claude-md**: Deep update all 15 CLAUDE.md with verified content ([#194](https://github.com/kodflow/devcontainer-template/pull/194))
- **i18n**: Translate all French content to English ([#195](https://github.com/kodflow/devcontainer-template/pull/195))
