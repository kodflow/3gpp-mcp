---
description: Register the 3gpp-mcp server in Claude Code (stdio via Docker, or HTTP) and verify it answers.
---

# /install-3gpp

Register the **3gpp-mcp** retrieval server so Claude can query the 3GPP corpus with
exact citations. Two transports — pick one.

## Option A — stdio (local, recommended)

```bash
docker pull ghcr.io/kodflow/3gpp-mcp:latest
claude mcp add 3gpp -- docker run -i --rm ghcr.io/kodflow/3gpp-mcp:latest serve
```

> The repo already ships a project `.mcp.json` (stdio) — teammates who open the repo
> get the server on workspace-trust, no command needed. This skill is for a user-scope
> install or an explicit re-add.

## Option B — HTTP (a shared/long-running server)

```bash
docker run -d --name 3gpp-mcp -p 8765:8765 -e MCP_TRANSPORT=http \
  ghcr.io/kodflow/3gpp-mcp:latest serve
claude mcp add --transport http 3gpp http://localhost:8765/mcp
```

## Verify

```bash
claude mcp list           # expect: 3gpp — Connected
```

Then ask Claude to call `server_info` — it reports which retrieval modes are active
(lexical / semantic / hnsw). If `Connected` but tools error, increase the timeout:
`MCP_TIMEOUT=15000 claude mcp list`.

## Notes / gotchas
- stdio uses `docker run -i` (NO `-t`: a TTY corrupts the JSON-RPC framing).
- The image is private; ensure you are authenticated to GHCR (`docker login ghcr.io`).
- No secrets in `args`; the server needs none for query.
