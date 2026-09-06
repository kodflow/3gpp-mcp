package mcp

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// EVERY HANDLER MUST BE REGISTERED THROUGH shielded(), AND A GREP IS THE HONEST
// WAY TO CHECK IT.
//
// Cancelling a running DuckDB query makes it raise duckdb::InterruptException,
// which crosses cgo and ABORTS THE PROCESS — it never becomes a Go error. A
// client that disconnects mid-call cancels the request context, so an unshielded
// tool is a way for any user to kill the server. Measured against the published
// image on 2026-09-06.
//
// A behavioural test would only cover the tools it happens to call, and the
// failure mode here is a handler someone ADDS LATER and forgets to wrap. That is
// a property of the registration list, so the registration list is what is
// checked. Crude on purpose, and it fails on exactly the mistake it exists for.
func TestEveryHandlerIsRegisteredShielded(t *testing.T) {
	// `), h.searchSpec)` — a tool handler passed straight to AddTool.
	bareTool := regexp.MustCompile(`\),\s*h\.\w+\)`)
	// `\t\th.readSpecResource,` — a resource handler as a bare argument.
	bareResource := regexp.MustCompile(`(?m)^\s+h\.\w+,\s*$`)

	for _, f := range []string{"server.go", "resources.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)

		if m := bareTool.FindAllString(src, -1); len(m) > 0 {
			t.Errorf("%s registers %d tool handler(s) without shielded(): %v\n"+
				"a client disconnect on any of them cancels a DuckDB query, which aborts "+
				"the process rather than returning an error", f, len(m), m)
		}
		if m := bareResource.FindAllString(src, -1); len(m) > 0 {
			var got []string
			for _, s := range m {
				got = append(got, strings.TrimSpace(s))
			}
			t.Errorf("%s registers %d resource handler(s) without shielded(): %v",
				f, len(m), got)
		}
	}
}

// And the shield must actually be there: a file that registers nothing would
// pass the test above by vacuity, which is the classic way a guard like this
// rots into decoration.
func TestTheShieldIsActuallyUsed(t *testing.T) {
	for _, c := range []struct {
		file string
		want int
	}{
		{"server.go", 12},
		{"resources.go", 2},
	} {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(b), "shielded(h."); got != c.want {
			t.Errorf("%s wraps %d handler(s), want %d — a registration was added or "+
				"removed without the count being reconsidered", c.file, got, c.want)
		}
	}
}
