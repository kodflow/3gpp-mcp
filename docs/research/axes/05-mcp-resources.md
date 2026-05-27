# Axis 05 — MCP Resources + Pagination

> **Status:** research / implementation-ready design. No code changed yet.
> **Scope of this doc only:** design, code sketches, step plan, risks. Implementation lands in a separate MR.
> **Library pins (verified against module cache):** `github.com/mark3labs/mcp-go v0.54.1`, `github.com/yosida95/uritemplate/v3 v3.0.2` (already an indirect dep — becomes direct).
> **MCP spec version targeted:** `2025-06-18`.

## 1. Problem statement

`get_spec` returns the full `text` of every matched clause inline in the tool result
(`internal/mcp/server.go`, `getSpec` → `clauseOut.Text`). A clause *prefix* like
`clause="6"` on TS 33.128 expands to a whole subtree — easily multiple MB of normative
text — and the entire body is serialised into one `CallToolResult` JSON string via
`jsonResult` / `mcp.NewToolResultText`. Consequences:

- **Token blow-up.** The client pays for the full subtree even when it only needs to
  know *which* clauses matched. `search_spec` already truncates to 400 chars
  (`truncate(hit.Clause.Text, 400)`), but `get_spec` does not.
- **No pagination.** `search_spec` (`top_k`) and `get_changelog` return everything in a
  single shot; there is no way to ask for "the next page" of a large result.
- **No stable addressing.** There is no canonical handle a client can hold onto to
  re-fetch one specific clause body on demand.

The fix has two independent halves:

1. **MCP Resources** — expose clause/spec bodies as addressable, on-demand resources
   under a `3gpp://` URI scheme. Tools return *citations + short snippet + a resource
   URI*; the full body is fetched via `resources/read` only when the client decides it
   needs it.
2. **Cursor pagination** — add opaque-cursor paging to `search_spec` and
   `get_changelog` so large result sets stream in bounded pages.

These are orthogonal and can ship in either order; both are additive (no breaking change
to the 9-tool surface).

---

## 2. MCP Resources — spec primer (2025-06-18)

What the protocol gives us (confirmed from `modelcontextprotocol.io/specification/2025-06-18/server/resources`):

| Method | Purpose | Paginated? |
|---|---|---|
| `resources/list` | enumerate *concrete* resources | yes (`cursor` / `nextCursor`) |
| `resources/templates/list` | enumerate *parameterised* resources (RFC 6570 URI templates) | yes |
| `resources/read` | fetch contents of one URI | no (single URI) |
| `resources/subscribe` + `notifications/resources/updated` | optional change subscriptions | n/a |
| `notifications/resources/list_changed` | optional list-changed signal | n/a |

**Capability declaration** (server side):

```json
{ "capabilities": { "resources": { "subscribe": false, "listChanged": false } } }
```

We need **neither** `subscribe` nor `listChanged`: the corpus is a static, read-only
snapshot per ingestion (CLAUDE.md §1 "Reproductibilité d'ingestion"). Declaring both
`false` is the correct, minimal posture.

**`resources/read` response shape** — a `contents` array; each entry is either text or
binary:

```json
{ "result": { "contents": [
  { "uri": "3gpp://33.128/Rel-19/6.2.2.2", "mimeType": "text/markdown", "text": "..." }
] } }
```

- **Text content:** `{ uri, mimeType, text }`.
- **Binary content:** `{ uri, mimeType, blob }` (base64). We only ever return text — the
  corpus is parsed DOCX → plain/markdown text, never binary (CLAUDE.md §13 "no PDF/OCR").

**Resource object fields:** `uri`, `name`, `title?`, `description?`, `mimeType?`,
`size?`, `annotations?` (`audience`, `priority`, `lastModified`).

**Error handling:** `resources/read` on an unknown/invalid URI → JSON-RPC error
`-32002` (Resource not found). mcp-go produces this automatically when no template
matches (see §4).

**Custom URI scheme:** allowed by the spec ("implementations are always free to use
additional, custom URI schemes") provided it is RFC 3986-conformant. `https://` is
reserved for "client can fetch it directly off the web" — **not** our case (the client
must read through the MCP server, which holds the DuckDB), so a **custom `3gpp://`
scheme is the spec-correct choice**, not `https://`.

---

## 3. The `3gpp://` URI scheme

### 3.1 Grammar

```
3gpp://<spec_id>/<release>/<clause>        ← one clause subtree (the workhorse)
3gpp://<spec_id>/<release>                 ← whole spec at the release's version
3gpp://<spec_id>/<release>@<version>       ← pin an exact version (optional, see §3.3)
3gpp://<spec_id>/<release>/<clause>@<version>
```

| Token | Example | Notes |
|---|---|---|
| `spec_id` | `33.128` | series.number, the `.` is literal and RFC3986-safe |
| `release` | `Rel-19` | `-` is unreserved; matches the corpus' `release` column |
| `clause` | `6.2.2.2` | clause path **or prefix** — selecting a subtree, same semantics as `get_spec`'s `clause` arg |
| `version` | `19.6.0` | optional pin; absent ⇒ resolve via `VersionForRelease` |

Concrete examples:

- `3gpp://33.128/Rel-19/6.2.2.2` — clause 6.2.2.2 subtree of TS 33.128 in Rel-19.
- `3gpp://23.501/Rel-18` — all of TS 23.501 at the Rel-18 version.
- `3gpp://33.128/Rel-19/6.2.2.2@19.6.0` — version-pinned (reproducible citation).

### 3.2 Why this shape

- **One-to-one with the existing `model.Citation`.** A citation already carries
  `{spec_id, release, version, clause, url}`. The resource URI is just the first four
  fields rendered as a path. Round-trip: `Citation → URI → resources/read → body` is
  loss-free.
- **Release-first, version-optional.** The whole server is release-scoped
  (`baseline`); a client almost always thinks in releases, not versions. Version is an
  optional pin for reproducibility (CLAUDE.md §8.3 "versions non monotones").
- **Prefix-friendly.** Because `clause` is a prefix, `…/6` and `…/6.2.2.2` are both
  valid and map straight onto `store.GetClauses(ctx, specID, version, clausePrefix)`.

### 3.3 RFC 6570 matching caveat (critical)

mcp-go matches a read URI to a template with `template.Regexp().MatchString(uri)` and
extracts vars via `URITemplate.Match` (`server/server.go:1534-1565`). RFC 6570 **simple
string expansion** `{var}` is percent-encoded and **does not match `/`**. A clause path
`6.2.2.2` contains no `/`, but a *spec/release* segmenting template must not accidentally
swallow following segments.

Therefore:

- For the clause body template, the clause var **must use reserved expansion**
  `{+clause}` (the `+` operator) so a value with dots — and, defensively, any value — is
  matched literally without re-encoding. Dots are fine under simple expansion too, but
  `{+clause}` is the safe, explicit choice and future-proofs against clause labels that
  ever contain reserved chars (e.g. annex labels `A.1`).
- The `@<version>` pin is handled **inside the handler** by splitting the matched
  `clause`/`release` var on `@`, rather than adding a separate template variable. This
  keeps the template count low and avoids RFC6570 optional-segment complexity.

Net: register **two templates** (clause-level and spec-level), parse the optional
`@version` suffix in the handler.

---

## 4. mcp-go resource API (v0.54.1, verified from module source)

Signatures pulled from `$(go env GOMODCACHE)/github.com/mark3labs/mcp-go@v0.54.1`:

```go
// server/server.go
func WithResourceCapabilities(subscribe, listChanged bool) ServerOption
func (s *MCPServer) AddResource(resource mcp.Resource, handler ResourceHandlerFunc)
func (s *MCPServer) AddResourceTemplate(template mcp.ResourceTemplate, handler ResourceTemplateHandlerFunc)

type ResourceHandlerFunc         func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error)
type ResourceTemplateHandlerFunc func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error)

// mcp/resources.go
func NewResource(uri, name string, opts ...ResourceOption) Resource
func NewResourceTemplate(uriTemplate, name string, opts ...ResourceTemplateOption) ResourceTemplate
func WithResourceDescription(string) ResourceOption
func WithMIMEType(string) ResourceOption
func WithResourceSize(int64) ResourceOption
func WithAnnotations(audience []Role, priority float64, lastModified string) ResourceOption
func WithTemplateDescription(string) ResourceTemplateOption
func WithTemplateMIMEType(string) ResourceTemplateOption

// mcp/types.go
type ReadResourceRequest struct { /* ... */ Params struct {
    URI       string         `json:"uri"`
    Arguments map[string]any `json:"arguments,omitempty"` // ← matched RFC6570 vars land here
} }
type TextResourceContents struct { Meta map[string]any; URI string; MIMEType string; Text string }
```

**Note (vs context7 docs):** the README/docs snippets sometimes show the handler
returning `*mcp.ReadResourceResult`. The **actual v0.54.1 type** is
`([]mcp.ResourceContents, error)` — the server wraps it in `ReadResourceResult{Contents: …}`
itself (`server/server.go:1519`). Use the slice return.

**Matching mechanics (so the handler knows what it received):** on `resources/read`,
the server iterates templates, and for the first whose `Regexp()` matches the URI it
fills `request.Params.Arguments` with the captured RFC6570 vars (`server.go:1556-1565`).
If nothing matches → `RESOURCE_NOT_FOUND` (-32002) automatically. So the handler reads
`request.Params.Arguments["spec_id"]` etc., **or** re-parses `request.Params.URI`
itself (more robust given the `@version` suffix trick).

---

## 5. Resource design for 3gpp-mcp

### 5.1 New file: `internal/mcp/resources.go`

A small `resources.go` alongside `server.go`, wired from `New()`:

```go
func registerResources(s *server.MCPServer, h *handlers) {
    // Clause subtree: 3gpp://33.128/Rel-19/6.2.2.2  (clause may be a prefix; {+clause}
    // uses RFC6570 reserved expansion so dotted paths match literally).
    s.AddResourceTemplate(
        mcp.NewResourceTemplate(
            "3gpp://{spec_id}/{release}/{+clause}",
            "3GPP clause body",
            mcp.WithTemplateDescription(
                "Verbatim text of a 3GPP clause (or clause-prefix subtree). "+
                    "URI form: 3gpp://<spec_id>/<release>/<clause>[@<version>]."),
            mcp.WithTemplateMIMEType("text/markdown"),
        ),
        h.readClauseResource,
    )

    // Whole spec at a release: 3gpp://23.501/Rel-18[@18.6.0]
    s.AddResourceTemplate(
        mcp.NewResourceTemplate(
            "3gpp://{spec_id}/{release}",
            "3GPP spec body",
            mcp.WithTemplateDescription(
                "Verbatim text of an entire 3GPP spec at a release. "+
                    "URI form: 3gpp://<spec_id>/<release>[@<version>]."),
            mcp.WithTemplateMIMEType("text/markdown"),
        ),
        h.readSpecResource,
    )
}
```

And in `New()`:

```go
s := server.NewMCPServer("3gpp-mcp", version,
    server.WithToolCapabilities(true),
    server.WithResourceCapabilities(false, false), // ← add: static corpus, no subscribe/listChanged
    server.WithInstructions(...),
)
...
h := &handlers{st: st, eng: eng, baseline: baseline}
registerResources(s, h)
```

> **`resources/list` policy.** We deliberately register **templates only**, not a
> concrete `resources/list` enumeration. The corpus is ~150 specs × many releases ×
> thousands of clauses = millions of URIs; enumerating them is pointless and would force
> us to implement list-pagination for no client benefit. Templates advertise the *shape*;
> clients construct URIs from tool citations. `resources/templates/list` returns the two
> templates above (mcp-go paginates that automatically, trivially one page).

### 5.2 Handler: clause resource

```go
// readClauseResource serves 3gpp://<spec>/<release>/<clause>[@<version>] as text.
func (h *handlers) readClauseResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
    ref, err := parse3GPPURI(req.Params.URI) // {spec_id, release, clause, version}
    if err != nil {
        return nil, err // surfaces as INTERNAL_ERROR; unknown-shape URIs never reach here (no template match → -32002)
    }
    version := ref.version
    if version == "" {
        if v, ok, _ := h.st.VersionForRelease(ctx, ref.specID, ref.release); ok {
            version = v
        } else if _, v, ok, _ := h.st.LatestVersion(ctx, ref.specID); ok {
            version = v
        }
    }
    clauses, err := h.st.GetClauses(ctx, ref.specID, version, ref.clause)
    if err != nil {
        return nil, fmt.Errorf("get clauses: %w", err)
    }
    if len(clauses) == 0 {
        // No body → not-found, not an empty 200. Lets mcp-go report a clean miss.
        return nil, fmt.Errorf("%w: %s", server.ErrResourceNotFound, req.Params.URI)
    }
    // Render the subtree as one markdown document, headings + text in clause order.
    var b strings.Builder
    for _, c := range clauses {
        fmt.Fprintf(&b, "## %s %s\n\n%s\n\n", c.ClausePath, c.Heading, c.Text)
    }
    return []mcp.ResourceContents{
        mcp.TextResourceContents{
            URI:      req.Params.URI,
            MIMEType: "text/markdown",
            Text:     b.String(),
        },
    }, nil
}
```

The spec-level handler (`readSpecResource`) is identical with `ref.clause == ""` (whole
spec). The two handlers can share a private `renderClauses` helper.

### 5.3 URI parse/build helpers

```go
type specRef struct{ specID, release, clause, version string }

// build3GPPURI is the inverse of parse3GPPURI; used by tools to emit resource URIs.
func build3GPPURI(c model.Citation) string {
    u := "3gpp://" + c.SpecID + "/" + c.Release
    if c.Clause != "" {
        u += "/" + c.Clause
    }
    if c.Version != "" {
        u += "@" + c.Version
    }
    return u
}

// parse3GPPURI splits a 3gpp:// URI. Tolerant of the optional @version suffix on the
// last path segment. Does NOT depend on mcp-go's matched Arguments (more robust).
func parse3GPPURI(uri string) (specRef, error) {
    rest, ok := strings.CutPrefix(uri, "3gpp://")
    if !ok {
        return specRef{}, fmt.Errorf("not a 3gpp:// uri: %q", uri)
    }
    body, version, _ := strings.Cut(rest, "@") // version optional
    parts := strings.SplitN(body, "/", 3)        // spec / release / clause(remainder)
    if len(parts) < 2 {
        return specRef{}, fmt.Errorf("malformed 3gpp uri: %q", uri)
    }
    ref := specRef{specID: parts[0], release: parts[1], version: version}
    if len(parts) == 3 {
        ref.clause = parts[2]
    }
    return ref, nil
}
```

> Parsing the URI string directly (rather than trusting `req.Params.Arguments`) sidesteps
> any RFC6570 decoding surprises and handles `@version` uniformly for both templates.
> Round-trip unit tests (`build3GPPURI ∘ parse3GPPURI == identity`) are cheap and lock it.

---

## 6. How the tools change

### 6.1 `get_spec` — snippet + URI instead of full body (the headline fix)

Today `clauseOut.Text` carries the entire clause text. Change it to carry a **bounded
snippet** plus a **resource URI** that addresses the full body. Add an explicit
`truncated` flag and the byte size so the client can decide whether to `resources/read`.

```go
type clauseOut struct {
    ClausePath  string         `json:"clause_path"`
    Heading     string         `json:"heading"`
    Snippet     string         `json:"snippet"`        // was: Text (full) → now bounded
    Truncated   bool           `json:"truncated"`      // true if Snippet < full body
    Bytes       int            `json:"bytes"`          // full body size, so client can budget
    Resource    string         `json:"resource"`       // 3gpp://… → resources/read for full text
    IsNormative bool           `json:"is_normative"`
    Citation    model.Citation `json:"citation"`
}
```

`getSpec` body becomes:

```go
for _, c := range clauses {
    cite := c.Cite()
    full := c.Text
    out = append(out, clauseOut{
        ClausePath:  c.ClausePath,
        Heading:     c.Heading,
        Snippet:     truncate(full, snippetLimit), // snippetLimit ≈ 600
        Truncated:   len(strings.TrimSpace(full)) > snippetLimit,
        Bytes:       len(full),
        Resource:    build3GPPURI(cite),
        IsNormative: c.IsNormative,
        Citation:    cite,
    })
    cites = append(cites, cite)
}
```

Add a top-level hint so the client knows the contract:

```go
resp["resource_hint"] = "Full clause text is omitted when truncated=true. " +
    "Call resources/read on the per-clause `resource` URI (3gpp://…) to fetch it."
```

**Optional `full` escape hatch:** add a boolean tool arg `full` (default `false`). When
`true`, `getSpec` inlines `text` exactly as today (back-compat for clients that don't do
`resources/read`). This makes the change strictly additive and de-risks rollout.

```go
mcp.WithBoolean("full", mcp.Description("inline full clause text instead of snippet+resource URI (default false)")),
```

Result: a `get_spec clause="6"` that used to return MBs now returns a compact index of
clause headings + 600-char snippets + URIs; the model reads only the clauses it actually
needs.

### 6.2 `search_spec` — already snippets; add resource URI + pagination

`hitOut` already has `Snippet` (400 chars). Add the resource URI so a hit is directly
fetchable, and add pagination (§7):

```go
type hitOut struct {
    SpecID   string         `json:"spec_id"`
    Clause   string         `json:"clause"`
    Heading  string         `json:"heading"`
    Snippet  string         `json:"snippet"`
    Resource string         `json:"resource"` // ← new: 3gpp://<spec>/<release>/<clause>
    Score    float64        `json:"score"`
    Citation model.Citation `json:"citation"`
}
// out = append(out, hitOut{..., Resource: build3GPPURI(hit.Citation)})
```

### 6.3 Net data-flow

```
search_spec / get_spec  ──▶  [ snippet + citation + 3gpp:// URI ]   (cheap, bounded)
                                          │  client decides it needs the body
                                          ▼
                              resources/read 3gpp://33.128/Rel-19/6.2.2.2
                                          │
                                          ▼
                              { contents:[{ text: "<full clause subtree>" }] }  (paid once, on demand)
```

---

## 7. Cursor pagination for `search_spec` / `get_changelog`

### 7.1 Why protocol `nextCursor` does NOT apply here

The MCP pagination utility (`…/utilities/pagination`) lists exactly four paginated
operations: `resources/list`, `resources/templates/list`, `prompts/list`, `tools/list`.
**Tool *call results* are not paginated by the protocol** — a `tools/call` result is an
opaque content blob. So we cannot use the transport-level `nextCursor`. Instead we model
pagination **inside the tool's own arguments and result body**: a `cursor` input arg and
a `next_cursor` output field. This mirrors the protocol's opaque-cursor semantics at the
application layer and is the idiomatic approach for paginating tool output.

### 7.2 Cursor contract (mirror the spec's rules)

- **Opaque** to the client: base64-encoded JSON; client must not parse/modify/persist it.
- **Stable**: same query + same cursor ⇒ same page (within one ingestion snapshot).
- **Stateless server-side**: the cursor *is* the position; no server session state.
- **Invalid cursor → error**, matching spec's `-32602` intent (here: a tool error result
  `"invalid cursor"`, since tool args validate at the application layer, not JSON-RPC).
- **Missing `next_cursor` ⇒ last page.**

### 7.3 Cursor payload

A self-describing offset cursor (offset is sufficient and stable for a read-only
snapshot; it also lets us bind the cursor to the query so a cursor from query A can't be
replayed against query B):

```go
type pageCursor struct {
    Offset int    `json:"o"`
    QHash  string `json:"q"` // fnv hash of normalised query+filters; guards cross-query replay
}

func encodeCursor(c pageCursor) string {
    b, _ := json.Marshal(c)
    return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (pageCursor, error) {
    raw, err := base64.RawURLEncoding.DecodeString(s)
    if err != nil {
        return pageCursor{}, fmt.Errorf("invalid cursor")
    }
    var c pageCursor
    if err := json.Unmarshal(raw, &c); err != nil {
        return pageCursor{}, fmt.Errorf("invalid cursor")
    }
    return c, nil
}
```

### 7.4 `search_spec` with pagination

Add a `cursor` arg; keep `top_k` as the page size (rename semantics: "max per page").
Over-fetch by one to detect "more exists" without a count query:

```go
mcp.WithString("cursor", mcp.Description("opaque pagination cursor from a previous call's next_cursor")),
```

```go
func (h *handlers) searchSpec(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    q, err := r.RequireString("query")
    if err != nil { return mcp.NewToolResultError(err.Error()), nil }

    pageSize := r.GetInt("top_k", 10)
    qh := queryHash(q, filter) // fnv over normalised query + filter fields
    offset := 0
    if cur := r.GetString("cursor", ""); cur != "" {
        c, err := decodeCursor(cur)
        if err != nil || c.QHash != qh {
            return mcp.NewToolResultError("invalid cursor for this query"), nil
        }
        offset = c.Offset
    }

    // Ask the engine for pageSize+1 starting at offset (engine/store gains Offset+Limit).
    hits, err := h.eng.Search(ctx, search.Request{
        Text: q, Filter: filter, TopK: pageSize + 1, Offset: offset,
    })
    if err != nil { return mcp.NewToolResultErrorFromErr("search failed", err), nil }

    var next string
    if len(hits) > pageSize {
        hits = hits[:pageSize]
        next = encodeCursor(pageCursor{Offset: offset + pageSize, QHash: qh})
    }
    // ... build out/cites from hits, add Resource URIs ...
    resp := map[string]any{
        "query": q, "intent": string(search.Classify(q)),
        "count": len(out), "hits": out, "citations": cites,
    }
    if next != "" { resp["next_cursor"] = next }
    return jsonResult(resp)
}
```

**Engine/store change required (out of this doc's write-scope, noted for the impl MR):**
`search.Request` gains an `Offset int`; the store's ranked query gains `... LIMIT ?
OFFSET ?`. For the lexical/FTS path this is a straight SQL `LIMIT/OFFSET`. For the
hybrid/RRF path, fuse first then slice `[offset:offset+pageSize+1]` of the fused list
(RRF is deterministic for a fixed query, so offset slicing is stable).

### 7.5 `get_changelog` with pagination

Same pattern. `get_changelog` currently filters in-memory after fetch; cleanest is to
keep the in-memory filter and slice the filtered slice by `[offset:offset+pageSize]`,
emitting `next_cursor` when more remain. Add a `page_size` arg (default e.g. 50) and a
`cursor` arg; cursor `QHash` binds `{spec_id, from_release, to_release, clause}`.

```go
filtered := applyClauseFilter(changes, clause)
page, next := paginate(filtered, offset, pageSize, qh) // helper returns slice + next_cursor
resp := map[string]any{"spec_id": specID, "count": len(page), "changes": page}
if next != "" { resp["next_cursor"] = next }
```

A shared `paginate[T any](items []T, offset, size int, qh string) ([]T, string)` helper
keeps both tools DRY.

### 7.6 What does NOT get pagination

`list_releases`, `resolve_term`, `find_cross_references`, `list_specs`, `trace_evolution`,
`li_events` return inherently small sets (tens of rows). Leave them single-shot; adding
cursors there is churn for no benefit. If `list_specs` ever spans the full corpus
(~150+ specs) it can adopt the same `paginate` helper later.

---

## 8. Step-by-step implementation plan

Ordered, each step independently compilable/testable. (Files in `internal/`, `cmd/` are
**out of scope for this doc** — listed here as the impl-MR plan.)

1. **URI helpers + tests** — new `internal/mcp/uri.go`: `build3GPPURI`,
   `parse3GPPURI`, `specRef`. Table-driven round-trip tests incl. `@version`, prefix
   clauses, whole-spec, malformed input. *No server wiring yet.*
2. **Resource handlers + registration** — new `internal/mcp/resources.go`:
   `registerResources`, `readClauseResource`, `readSpecResource`, shared `renderClauses`.
   Add `server.WithResourceCapabilities(false, false)` and `registerResources(s, h)` in
   `New()`. Promote `yosida95/uritemplate/v3` from indirect → direct in `go.mod` (mcp-go
   pulls it; `go mod tidy` will mark it direct once we... actually we don't import it
   directly — mcp-go does — so it stays indirect; no go.mod edit needed).
3. **`get_spec` snippet+URI** — change `clauseOut` (add `Snippet/Truncated/Bytes/Resource`,
   drop inline `Text` unless `full=true`), add `full` bool arg, `resource_hint`. Update
   `internal/mcp/server_test.go` expectations.
4. **`search_spec` resource URI** — add `Resource` to `hitOut`.
5. **Pagination plumbing** — add `Offset` to `search.Request`; add `LIMIT/OFFSET` (lexical)
   / offset-slice (hybrid) to the store/engine. Unit-test stable paging.
6. **`search_spec` cursor** — `cursor` arg, over-fetch-by-one, `next_cursor` out,
   `pageCursor`/`queryHash` helpers (new `internal/mcp/cursor.go` + tests).
7. **`get_changelog` cursor** — `page_size` + `cursor` args, `paginate` helper, `next_cursor`.
8. **Docs/tool descriptions** — update tool `WithDescription` strings + `WithInstructions`
   to tell the client about the snippet+`resource` contract and `resources/read`.
9. **Integration smoke test** — drive `resources/templates/list` + `resources/read` over
   the in-process server harness (mirror existing `server_test.go` style) and assert a
   known clause body round-trips via a tool-emitted URI.

**Effort:** ~1–1.5 days solo. Steps 1–4 (resources) and 5–7 (pagination) are independent
and can be split into two MRs.

---

## 9. Risks & mitigations

| # | Risk | Mitigation |
|---|---|---|
| R1 | **RFC6570 `/` non-match.** `{clause}` (simple expansion) won't match dotted/slashed values as intended; template silently fails to match → `-32002`. | Use `{+clause}` reserved expansion; **and** parse the raw URI in-handler (`parse3GPPURI`) rather than relying on `req.Params.Arguments`. Add a read test for `3gpp://33.128/Rel-19/6.2.2.2`. |
| R2 | **Template ambiguity.** `3gpp://{spec}/{release}/{+clause}` vs `3gpp://{spec}/{release}` — a 2-segment URI must hit the spec template, a 3-segment URI the clause template. mcp-go picks the **first matching** template in registration order. | Register the **more specific** (clause, 3-segment) template **first**; verify with a 2-segment read test that hits the spec handler. |
| R3 | **Client doesn't support resources.** Some MCP clients only call tools. If `get_spec` stops returning full text, those clients lose data. | `full=true` escape hatch on `get_spec` (default false). Document it. Snippet+`bytes`+`resource` is still useful even without `resources/read`. |
| R4 | **Cursor instability across ingestions.** Offset cursors break if the corpus is re-ingested mid-pagination (rows shift). | Cursors are explicitly **session-scoped, single-snapshot** (spec: "Don't persist cursors across sessions"). `QHash` guards cross-query replay; re-ingestion is a new snapshot — acceptable per spec. |
| R5 | **Hybrid/RRF offset correctness.** Offset slicing only stable if fused ranking is deterministic for a fixed query. | RRF (k=60) over fixed candidate sets is deterministic; fuse-then-slice. Add a test paging through the same query and asserting no dup/skip across page boundaries. |
| R6 | **`size` field accuracy.** Resource `size`/`bytes` should be the body length, but we compute it post-fetch. | Report `bytes` from the assembled text in tool output (cheap); omit `WithResourceSize` on the *template* (unknown a priori). |
| R7 | **Large whole-spec reads.** `3gpp://23.501/Rel-18` (no clause) can still be MBs on `resources/read`. | This is *opt-in* — the client explicitly asked for the whole spec. Acceptable; the default tool path never emits a whole-spec URI (it emits per-clause URIs). Optionally cap and document. |
| R8 | **Citation lacks `release` when version-only fallback fires.** `getSpec`'s fallback sets `release=""`; `build3GPPURI` would emit `3gpp://33.128//6.2.2.2`. | When release is empty, build the URI with the resolved `version` pin form `3gpp://<spec>/<release-or-latest>@<version>`; ensure `parse3GPPURI` tolerates an empty release segment, or substitute the resolved release. Covered by round-trip tests. |

---

## 10. Sources

- MCP Resources spec (2025-06-18): https://modelcontextprotocol.io/specification/2025-06-18/server/resources
- MCP Pagination utility (2025-06-18): https://modelcontextprotocol.io/specification/2025-06-18/server/utilities/pagination
- mark3labs/mcp-go v0.54.1 — module source (`server/server.go`, `mcp/resources.go`, `mcp/types.go`) verified in local `GOMODCACHE`; resource handler signatures, `matchesTemplate`/`URITemplate.Match` dispatch, `RESOURCE_NOT_FOUND` behaviour.
- mark3labs/mcp-go docs via Context7 (`/mark3labs/mcp-go`) — `AddResource`/`AddResourceTemplate`/`NewResourceTemplate` usage patterns.
- RFC 6570 (URI Templates) — reserved expansion `{+var}`; RFC 3986 (URI scheme legality).
- Existing code: `internal/mcp/server.go`, `internal/model/types.go`, `internal/model/spec3gpp.go`, `internal/store/store.go`.
