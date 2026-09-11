package mcp

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// A HALF THAT COULD NOT BE READ IS NAMED, NEVER DROPPED.
//
// The two corpora are federated at read time, and a federated answer is only as
// honest as its account of which halves it actually read. search_spec asked the
// ETSI half and, when that failed, served the 3GPP hits alone without a word — so
// "ETSI has nothing on this" and "ETSI could not be asked" came back identical.
// Measured by the served retrieval gate (#340): under DUCKDB_MEMORY_LIMIT=4GB the
// ETSI half fails after its first semantic query, and every answer after that is
// silently half an answer. The same class as #311 and #332 — a silence read as a
// zero — and list_specs and resolve_term had it too (list_specs dropped the ETSI
// catalogue on error; resolve_term went the other way and failed the whole call,
// losing the 3GPP definitions to an ETSI read error).
//
// The rule, for every tool that reads both halves in one call:
//
//   - a half that fails does not fail the other: its answer is served;
//   - the failed half is named, in `unread_halves` (for a program) and in `note`
//     (for the model reading the answer), with the error that stopped it;
//   - only when every half fails is there nothing to serve, and the call errors.
//
// trace_evolution has followed this rule since #332; it now reports the same field.

// unreadHalf names a half of the federation — or the part of one — that a call
// could not read.
type unreadHalf struct {
	Half  string `json:"half"`           // "3gpp" | "etsi"
	Part  string `json:"part,omitempty"` // what of it, when not the whole half: "EN documents"
	Error string `json:"error"`
	// effect is what the note says the failure cost this answer; the default is
	// that the half is not covered at all.
	effect string
}

// errorTextLimit bounds an error quoted into an answer: its first line, at most
// this many bytes. DuckDB errors can run to a paragraph.
const errorTextLimit = 300

// firstLine returns the first line of s, bounded to n bytes.
func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func unreadOf(half, part string, err error) unreadHalf {
	return unreadHalf{Half: half, Part: part, Error: firstLine(err.Error(), errorTextLimit)}
}

// docTypePart names one document type of the ETSI half as a part of it.
func docTypePart(dt string) string {
	if dt == "" {
		return ""
	}
	return dt + " documents"
}

// unreadNote renders the failures for the answer's note.
func unreadNote(unread []unreadHalf) string {
	var parts []string
	for _, u := range unread {
		subject := "the " + halfLabel(u.Half) + " half"
		if u.Part != "" {
			subject = "the " + u.Part + " of the " + halfLabel(u.Half) + " half"
		}
		effect := u.effect
		if effect == "" {
			effect = "this answer does not cover it: what is missing from it here was not looked for, " +
				"and is not a count of zero"
		}
		parts = append(parts, fmt.Sprintf("%s could not be read (%s), so %s.", subject, u.Error, effect))
	}
	return strings.Join(parts, " ")
}

// withUnread records the failures on a response, leading its note: a reader who
// does not know half the corpus was skipped reads everything after as complete.
func withUnread(resp map[string]any, unread []unreadHalf) {
	if len(unread) == 0 {
		return
	}
	resp["unread_halves"] = unread
	existing, _ := resp["note"].(string)
	resp["note"] = joinNotes(unreadNote(unread), existing)
}

// Option adjusts what New builds.
type Option func(*handlers)

// WithETSIUnavailable tells the server that the ETSI half was asked for and could
// not be opened, and why. The federated tools then name it in every answer, the
// way they name a half that fails mid-query, instead of serving the 3GPP half as
// if it were the whole corpus.
func WithETSIUnavailable(reason string) Option {
	return func(h *handlers) { h.etsiDown = strings.TrimSpace(reason) }
}

// etsiOpenFailure is the ETSI half that was ASKED FOR and could not be opened at
// startup. cmd/server used to log it on stderr and serve the 3GPP half alone —
// from then on, every federated answer omitted ETSI without saying so, and a
// client had no way to tell that server from one that was never given ETSI.
func (h *handlers) etsiOpenFailure() (unreadHalf, bool) {
	if h.etsi != nil || h.etsiDown == "" {
		return unreadHalf{}, false
	}
	return unreadHalf{Half: "etsi", Error: firstLine(h.etsiDown, errorTextLimit)}, true
}

// etsiRefusal answers a call about ONE ETSI document on a server whose ETSI half
// was asked for and could not be opened. Routing sends such an id to the 3GPP
// store when there is no ETSI one, which answered "no such spec in corpus" (or,
// for trace_clause, "this corpus predates paragraph provenance") — each a false
// statement about the corpus, where the truth is that the half holding the
// document is not there to ask. nil when the call can proceed.
func (h *handlers) etsiRefusal(specID string) *mcp.CallToolResult {
	if err := h.etsiUnavailableFor(specID); err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return nil
}

// etsiUnavailableFor is etsiRefusal as an error, for resources/read, which has no
// tool result to carry it.
func (h *handlers) etsiUnavailableFor(specID string) error {
	u, down := h.etsiOpenFailure()
	if !down || !isETSISpecID(specID) {
		return nil
	}
	return fmt.Errorf("%s is an ETSI document, and the ETSI half of this server could not be opened (%s): "+
		"nothing about it can be answered here. This is not an absence from the corpus; the half that holds "+
		"it is unavailable", specID, u.Error)
}

// etsiDetached is how server_info and help describe an ETSI half that is not
// attached: never asked for, or asked for and unavailable — and why.
func (h *handlers) etsiDetached() map[string]any {
	out := map[string]any{"attached": false}
	if u, down := h.etsiOpenFailure(); down {
		out["unavailable"] = u.Error
	}
	return out
}
