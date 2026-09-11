package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// registerResources exposes clause/spec bodies as addressable, on-demand MCP
// resources under the 3gpp:// scheme (axis #5). Tools return citations + a
// snippet + a resource URI; the full body is fetched via resources/read only
// when the client decides it needs it — bounding token cost.
func registerResources(s *server.MCPServer, h *handlers) {
	// Same shield as the tools, same reason: a client that disconnects mid-read
	// cancels the request context, and a cancelled DuckDB query aborts the process
	// on Linux rather than returning an error. See the note at the tool
	// registrations in server.go.
	shielded := func(f server.ResourceTemplateHandlerFunc) server.ResourceTemplateHandlerFunc {
		return func(ctx context.Context, r mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return f(context.WithoutCancel(ctx), r)
		}
	}

	// Clause subtree: 3gpp://<spec>/<release>/<clause>[@<version>]. {+clause}
	// (RFC6570 reserved expansion) matches dotted paths literally.
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"3gpp://{spec_id}/{release}/{+clause}",
			"3GPP clause body",
			mcp.WithTemplateDescription(
				"Verbatim text of a 3GPP clause (or clause-prefix subtree). "+
					"URI: 3gpp://<spec_id>/<release>/<clause>[@<version>]."),
			mcp.WithTemplateMIMEType("text/markdown"),
		),
		shielded(h.readClauseResource),
	)
	// Whole spec at a release: 3gpp://<spec>/<release>[@<version>].
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"3gpp://{spec_id}/{release}",
			"3GPP spec body",
			mcp.WithTemplateDescription(
				"Verbatim text of an entire 3GPP spec at a release. "+
					"URI: 3gpp://<spec_id>/<release>[@<version>]."),
			mcp.WithTemplateMIMEType("text/markdown"),
		),
		shielded(h.readSpecResource),
	)
}

// specRef is a parsed 3gpp:// URI.
type specRef struct{ specID, release, clause, version string }

// uriEscaper percent-encodes what a corpus id can carry and a resource template
// cannot match. The space is the one that matters: EVERY ETSI id has two
// ("ETSI TS 103 221-1"), and mcp-go matches resources/read against the templates
// with an RFC 6570 regexp that admits no space — so every ETSI resource URI this
// server handed out answered "handler not found for resource URI", before any
// handler ran. "%" goes first so the encoding round-trips.
var uriEscaper = strings.NewReplacer("%", "%25", " ", "%20")

// uriUnescape is uriEscaper's inverse; a segment that does not decode is kept
// as written.
func uriUnescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// build3GPPURI renders a citation as a 3gpp:// resource URI (inverse of parse).
func build3GPPURI(c model.Citation) string {
	u := "3gpp://" + uriEscaper.Replace(c.SpecID) + "/" + uriEscaper.Replace(c.Release)
	if c.Clause != "" {
		u += "/" + uriEscaper.Replace(c.Clause)
	}
	if c.Version != "" {
		u += "@" + uriEscaper.Replace(c.Version)
	}
	return u
}

// parse3GPPURI splits a 3gpp:// URI, tolerating the optional @version suffix.
// It parses the string directly (not mcp-go's matched args) so the @version
// trick works uniformly for both templates.
func parse3GPPURI(uri string) (specRef, error) {
	rest, ok := strings.CutPrefix(uri, "3gpp://")
	if !ok {
		return specRef{}, fmt.Errorf("not a 3gpp:// uri: %q", uri)
	}
	body, version, _ := strings.Cut(rest, "@")
	parts := strings.SplitN(body, "/", 3)
	if len(parts) < 2 {
		return specRef{}, fmt.Errorf("malformed 3gpp uri: %q", uri)
	}
	ref := specRef{specID: uriUnescape(parts[0]), release: uriUnescape(parts[1]), version: uriUnescape(version)}
	if len(parts) == 3 {
		ref.clause = uriUnescape(parts[2])
	}
	return ref, nil
}

func (h *handlers) readClauseResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return h.readResource(ctx, req.Params.URI)
}

func (h *handlers) readSpecResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return h.readResource(ctx, req.Params.URI)
}

// readResource resolves a 3gpp:// URI to the verbatim clause(s) as markdown.
// Both templates share it (the only difference is whether ref.clause is set).
func (h *handlers) readResource(ctx context.Context, uri string) ([]mcp.ResourceContents, error) {
	ref, err := parse3GPPURI(uri)
	if err != nil {
		return nil, err
	}
	if err := h.etsiUnavailableFor(ref.specID); err != nil {
		return nil, err
	}
	// ROUTED, like every per-spec lookup: this read the 3GPP store unconditionally,
	// so an ETSI clause's resource URI answered "not found" even once it matched.
	st := h.storeFor(ref.specID)
	version := ref.version
	if version == "" {
		if v, ok, _ := st.VersionForRelease(ctx, ref.specID, ref.release); ok {
			version = v
		} else if _, v, ok, _ := st.LatestVersion(ctx, ref.specID); ok {
			version = v
		}
	}
	clauses, err := st.GetClauses(ctx, ref.specID, version, ref.clause)
	if err != nil {
		return nil, fmt.Errorf("get clauses: %w", err)
	}
	if len(clauses) == 0 {
		return nil, fmt.Errorf("%w: %s", server.ErrResourceNotFound, uri)
	}
	var b strings.Builder
	for _, c := range clauses {
		if c.ClausePath != "" || c.Heading != "" {
			fmt.Fprintf(&b, "## %s %s\n\n", c.ClausePath, c.Heading)
		}
		b.WriteString(c.Text)
		b.WriteString("\n\n")
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{URI: uri, MIMEType: "text/markdown", Text: b.String()},
	}, nil
}
