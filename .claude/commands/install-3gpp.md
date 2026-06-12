---
description: Register the 3gpp-mcp server in Claude Code (stdio via Docker, or HTTP) and verify it answers.
---

# /install-3gpp

Register the **3gpp-mcp** retrieval server so Claude can query the 3GPP corpus with
exact citations. Two transports — pick one.

## Image variants — pick by need

| Tag | Contents | Use when |
|-----|----------|----------|
| **`:full` = `:latest`** | onnx binary + corpus DB + vector sub-bases + BGE-M3 model **baked in** (as `.zst`) | semantic search, offline / "just works" on pull (larger image; decompresses once into `/data` on first start) |
| **`:light`** | binary only (lexical/BM25); bootstraps the DB from the network on first run | small image, lexical-only is enough, or you provide data yourself |

> FULL stdio with `--rm` re-decompresses every run. Mount a **named volume** so the
> one-time decompression persists: `-v 3gpp-mcp-data:/data`.

## Option A — stdio (local, recommended)

```bash
docker pull ghcr.io/kodflow/3gpp-mcp:latest                       # full (semantic)
claude mcp add 3gpp -- docker run -i --rm -v 3gpp-mcp-data:/data \
  ghcr.io/kodflow/3gpp-mcp:latest serve
# lexical-only, smallest:
# claude mcp add 3gpp -- docker run -i --rm ghcr.io/kodflow/3gpp-mcp:light serve
```

> The repo already ships a project `.mcp.json` (stdio) — teammates who open the repo
> get the server on workspace-trust, no command needed. This skill is for a user-scope
> install or an explicit re-add.

## Option B — HTTP (a shared/long-running server)

```bash
docker run -d --name 3gpp-mcp -p 8765:8765 -e MCP_TRANSPORT=http \
  -v 3gpp-mcp-data:/data ghcr.io/kodflow/3gpp-mcp:latest serve
claude mcp add --transport http 3gpp http://localhost:8765/mcp
```

Readiness: `GET /healthz` reports `503 {"status":"loading"}` while the corpus/vectors
load, then `200 {"status":"ready"}` — wait for `ready` before querying (a baked full
image is ready almost instantly; light/first-run pulls the DB so loads longer).

## Install the /3gpp skill (strict cited answers + deep-research protocol)

The MCP registration gives Claude the *tools*; the **/3gpp skill** (a real SKILL.md
with frontmatter) pins the cited-answer format AND the mandatory deep-research
protocol on top (never answer from a single tool call). The skill is **embedded in
the server binary** (single source, always version-matched) — install it FROM THE
SERVER, never from the repository. Inside this repo it is already active
(project-scope `.claude/skills/3gpp/SKILL.md`); anywhere else, install it user-scope:

```bash
mkdir -p ~/.claude/skills/3gpp
# stdio install — the binary prints its own embedded skill:
docker run --rm ghcr.io/kodflow/3gpp-mcp:latest skill > ~/.claude/skills/3gpp/SKILL.md
# HTTP install — the running server serves the same bytes:
curl -fsSL http://localhost:8765/skill/3gpp.md -o ~/.claude/skills/3gpp/SKILL.md
```

Restart the Claude Code instance afterwards: MCP registrations and `~/.claude/skills/`
are both read at session start, so after the relaunch `claude mcp list` shows the server
AND `/3gpp` appears in the skills list — nothing else to do.

## Verify

```bash
claude mcp list           # expect: 3gpp — Connected
ls ~/.claude/skills/3gpp/SKILL.md         # skill present (user scope)
curl -fsS http://localhost:8765/healthz   # HTTP mode: {"status":"ready"}
```

Then ask Claude to call `server_info` — it reports which retrieval modes are active
(lexical / semantic / hnsw). If `Connected` but tools error, increase the timeout:
`MCP_TIMEOUT=15000 claude mcp list`.

## Notes / gotchas
- stdio uses `docker run -i` (NO `-t`: a TTY corrupts the JSON-RPC framing).
- The image is private; ensure you are authenticated to GHCR (`docker login ghcr.io`).
- No secrets in `args`; the server needs none for query.
- `:full` is semantic only when the vector sub-bases cover the series you query;
  uncovered series fall back to lexical (BM25). `server_info` shows `semantic`.
