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
