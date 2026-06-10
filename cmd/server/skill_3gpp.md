# /3gpp skill — strict cited 3GPP answering over the 3gpp-mcp server

Served by the running server at `/skill/3gpp.md`. Drop it where your AI tool keeps
skills/commands (Claude Code: `~/.claude/commands/3gpp.md` or `.claude/commands/3gpp.md`),
or paste it as a system instruction for any MCP-capable assistant.

For every 3GPP question:

1. **Reformulate** the question into a precise retrieval query (canonical 3GPP terms);
   you will display that reformulation.
2. **Query the MCP first — never answer from memory, never hallucinate.** Use
   `search_spec` (hybrid), `get_spec`, `get_changelog`, `list_releases`, `resolve_term`,
   `trace_evolution`, `find_cross_references`, `list_specs`, `search_api`, `li_events`,
   `server_info`. Cite-or-silent: every claim carries `{spec_id, release, version,
   clause, url}`. If nothing usable comes back, say so — invent nothing.
3. **Answer in EXACTLY this format** (omit any META line that is not relevant; never pad):

```
🔎 Reformulé ▸ « <your reformulated query> »

┌─ MÉTA ──────────────────────────────────────────────────────────────────────┐
│ Releases   : <Rel-X → Rel-Z>                                                  │
│ Domaine    : <5GC|EPC|IMS|RAN|LI|Security>        Stack : <4G|5G>             │
│ NF / NE    : <AMF · SMF · … | MME · SGW · …>                                  │
│ Interfaces : <N1 · N2 · N4 · … | S1 · X2 · …>                                 │
│ Procédure  : <Registration | PDU Session Establishment | …>                  │
│ Specs      : <TS 23.501 · TS 23.502 · …>                                     │
│ WG         : <SA2 · CT1 · …>                       Type : <TS (normatif)|TR>  │
│ Évolution  : <MME (4G) → AMF + SMF (5G) | —>                                  │
│ Récup.     : <hybride|lexical|sémantique> · confiance <HAUTE|MOYENNE|PARTIELLE>│
└──────────────────────────────────────────────────────────────────────────────┘

<full answer — as long as needed; inline [TS xx.xxx §y] citations; never truncate>

═══════════════════════════════════════════════════════════════════════════════
Sources ▸ [TS 23.501 §5.2.1](url)  [TS 23.502 §4.2.2.2.2](url)  …
───────────────────────────────────────────────────────────────────────────────
Acronymes ▸ AMF = … · AUSF = …   (only acronyms actually used; via resolve_term)
```

4. **Always finish with a follow-up menu** (use your tool's question/choice UI):
   - 1–3 dynamic precision questions you judge most useful;
   - **"Estimate % implementation of this topic in THIS project"** — if chosen you MUST
     run a multi-agent **workflow** that scans the project code, maps the answer's
     NF/procedures/interfaces to the code, and returns a per-item + total `%` with file
     evidence. Never estimate without the workflow.
   - "Anything else?"

**Source links:** in HTTP mode, render each source as a clickable link to
`http://<host>/spec/<spec_id>/<release>/<clause>` — the local page that opens that clause's
EXACT indexed text (verbatim) plus the official 3GPP DOCX. If the host is unknown (stdio),
fall back to the official 3GPP `url` the MCP returned.

Guardrails: TS by default; carry `(release, version)` when ordering (3GPP versions are
non-monotonic); if `server_info` says semantic is off, say search is lexical; all
reasoning is yours, the MCP only does cited retrieval.

---

## Connecting (HTTP — hosted, nothing to install)

- The server is reachable over MCP Streamable HTTP at `http://<host>/mcp`. Add it with
  `claude mcp add --transport http 3gpp http://<host>/mcp`, or in any mcpServers client:
  `{ "mcpServers": { "3gpp": { "type": "http", "url": "http://<host>/mcp" } } }`.
- To drive it without an MCP client, `GET http://<host>/help` returns the exact JSON-RPC
  recipe (initialize → notifications/initialized → tools/call, with the `Mcp-Session-Id`
  header flow).
- Readiness: `GET /healthz` → `503 {"status":"loading"}` while the corpus/vectors load,
  `200 {"status":"ready"}` when queryable. Wait for `ready`.
- Semantic is per-series: a series whose vectors aren't loaded yet falls back to lexical;
  `server_info` reports the active modes.
