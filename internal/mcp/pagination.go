package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
)

// pageCursor is an opaque, stateless offset cursor for tool-result pagination
// (axis #5 §7). MCP paginates only list operations at the protocol layer, so
// tool calls carry their own cursor arg + next_cursor field. QHash binds a
// cursor to its query so a cursor from query A can't be replayed against B.
type pageCursor struct {
	Offset int    `json:"o"`
	QHash  string `json:"q"`
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

// queryHash is a short stable digest of the normalised query + filters, used to
// reject a cursor replayed against a different query.
func queryHash(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum64())
}

// resolveOffset validates an optional cursor against qh and returns the page
// offset. An empty cursor starts at 0; a cursor bound to another query errors.
func resolveOffset(cursor, qh string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	c, err := decodeCursor(cursor)
	if err != nil || c.QHash != qh {
		return 0, fmt.Errorf("invalid cursor for this query")
	}
	return c.Offset, nil
}

// paginate cuts ONE page out of an already ordered, already filtered list: it
// resolves the cursor against qh, clamps the window to the list, and mints the
// cursor of the next page when records remain. It is the one place page bounds
// are computed, so a tool that pages a list cannot drift from another that does
// (Qodo, #341). An invalid cursor, or one bound to another query, is an error the
// caller turns into a tool error.
//
// A page past the end is empty rather than an error: a cursor from a larger list
// (the corpus was rebuilt between two calls) must not crash the walk, and the
// caller's note says the page holds nothing.
func paginate[T any](items []T, cursor, qh string, limit int) (page []T, start int, next string, err error) {
	offset, err := resolveOffset(cursor, qh)
	if err != nil {
		return nil, 0, "", err
	}
	if offset < 0 {
		return nil, 0, "", fmt.Errorf("invalid cursor for this query")
	}
	if limit <= 0 {
		return nil, 0, "", fmt.Errorf("page size must be positive")
	}
	start = min(offset, len(items))
	end := min(start+limit, len(items))
	if end < len(items) {
		next = encodeCursor(pageCursor{Offset: end, QHash: qh})
	}
	return items[start:end], start, next, nil
}
