package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
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
	// (RFC6570 reserved expansion) matches dotted paths literally — and the
	// "@<version>" suffix with them, since reserved expansion admits "@".
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"3gpp://{spec_id}/{release}/{+clause}",
			"3GPP clause body",
			mcp.WithTemplateDescription(
				"Verbatim text of a clause (or clause-prefix subtree), 3GPP or ETSI. "+
					"URI: 3gpp://<spec_id>/<release>/<clause>[@<version>]. "+specIDInURI),
			mcp.WithTemplateMIMEType("text/markdown"),
		),
		shielded(h.readClauseResource),
	)
	// Whole spec at a release: 3gpp://<spec>/<release>, and at one version:
	// 3gpp://<spec>/<release>@<version>.
	//
	// TWO TEMPLATES, BECAUSE ONE COULD NOT MATCH WHAT IT ADVERTISED. This used to
	// be the first one alone, described as "3gpp://<spec_id>/<release>[@<version>]"
	// — but {release} is a simple expansion, whose RFC 6570 character class has no
	// "@", so every URI written the way the description said answered "handler
	// not found for resource URI", on both halves. The version is not decoration:
	// an ETSI deliverable has no releases (its release is the constant "ETSI"), so
	// the version is the ONLY way to name one of its published versions.
	//
	// {+release} would have matched too, and was not used: reserved expansion
	// also admits "/", so that template would match every clause URI as well, and
	// mcp-go picks among matching templates in map order. These two and the
	// clause template above match disjoint sets.
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"3gpp://{spec_id}/{release}",
			"3GPP spec body",
			mcp.WithTemplateDescription(
				"Verbatim text of an entire spec, 3GPP or ETSI, at its newest version in a release. "+
					"URI: 3gpp://<spec_id>/<release>. For one version, use 3gpp://<spec_id>/<release>@<version>. "+
					specIDInURI),
			mcp.WithTemplateMIMEType("text/markdown"),
		),
		shielded(h.readSpecResource),
	)
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"3gpp://{spec_id}/{release}@{version}",
			"3GPP spec body at a version",
			mcp.WithTemplateDescription(
				"Verbatim text of an entire spec, 3GPP or ETSI, at one version. "+
					"URI: 3gpp://<spec_id>/<release>@<version>, e.g. 3gpp://23.501/Rel-18@18.5.0. "+specIDInURI),
			mcp.WithTemplateMIMEType("text/markdown"),
		),
		shielded(h.readSpecResource),
	)
}

// specIDInURI tells a client how to write an id that carries spaces — every
// ETSI id does — in a URI a template can match.
const specIDInURI = "spec_id is written as in a citation, with each space as %20 " +
	"(ETSI TS 103 221-1 -> ETSI%20TS%20103%20221-1); the ETSI release is ETSI."

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

// versionIsInRelease refuses a URI whose release and version name two different
// documents (Qodo, #345). GetClauses selects by spec and version only, so
// 3gpp://33.128/Rel-18@19.5.0 served the Rel-19 publication under a Rel-18
// address. A version the spec does not hold at all is left to GetClauses, which
// answers not found.
func versionIsInRelease(ctx context.Context, st store.Reader, specID, release, version string) error {
	vs, err := st.ListReleases(ctx, specID)
	if err != nil {
		return fmt.Errorf("list the releases of %s: %w", specID, err)
	}
	other := ""
	for _, v := range vs {
		if v.Version != version {
			continue
		}
		if v.Release == release {
			return nil
		}
		other = v.Release
	}
	if other != "" {
		return fmt.Errorf("%w: %s version %s is published in %s, not %s", server.ErrResourceNotFound,
			specID, version, other, release)
	}
	return nil
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
		// A release with no version of the spec is NOT FOUND. This fell back to the
		// spec's latest version, so 3gpp://33.128/Rel-15 served Rel-19 text under a
		// Rel-15 address (Qodo, #345). VersionForRelease already answers the
		// newest version for an empty release, which is the only fallback meant.
		v, ok, verr := st.VersionForRelease(ctx, ref.specID, ref.release)
		switch {
		case verr != nil:
			return nil, fmt.Errorf("resolve the version of %s in %s: %w", ref.specID, ref.release, verr)
		case !ok:
			return nil, fmt.Errorf("%w: %s holds no version of %s in %s", server.ErrResourceNotFound,
				firstLine(uri, errorTextLimit), ref.specID, ref.release)
		}
		version = v
	} else if ref.release != "" {
		if err := versionIsInRelease(ctx, st, ref.specID, ref.release, version); err != nil {
			return nil, err
		}
	}
	clauses, err := st.GetClauses(ctx, ref.specID, version, ref.clause)
	if err != nil {
		return nil, fmt.Errorf("get clauses: %w", err)
	}
	if len(clauses) == 0 {
		return nil, fmt.Errorf("%w: %s", server.ErrResourceNotFound, uri)
	}
	// The filing the URI names, once — not every release's copy of the version
	// (filings.go: 3gpp://30.531/Rel-12@1.62.0 served the same text nine times).
	clauses, _ = oneFiling(clauses, ref.release)
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
