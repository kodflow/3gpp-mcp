package etsicat

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestTokenToID(t *testing.T) {
	cases := []struct {
		token string
		want  string
		ok    bool
	}{
		{"103280", "103 280", true},         // no part
		{"10322101", "103 221-1", true},     // part 1
		{"10319206", "103 192-6", true},     // part 6
		{"1023220105", "102 322-1-5", true}, // chained parts (rare)
		{"12345", "", false},                // too short
		{"1032801", "", false},              // base + 1 stray digit
		{"abcdef", "", false},               // non-numeric
	}
	for _, c := range cases {
		got, ok := TokenToID(c.token)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("TokenToID(%q)=(%q,%v) want (%q,%v)", c.token, got, ok, c.want, c.ok)
		}
	}
}

// dirFetcher serves canned directory-listing HTML per URL — a no-network ETSI
// /deliver tree for the enumeration crawl.
func dirFetcher(tree map[string][]string) Fetcher {
	return func(url string) (io.ReadCloser, error) {
		entries, ok := tree[url]
		if !ok {
			return io.NopCloser(strings.NewReader("<html><body></body></html>")), nil
		}
		var b strings.Builder
		b.WriteString("<html><body><a href=\"../\">Parent</a>")
		for _, e := range entries {
			b.WriteString("<a href=\"" + e + "\">" + e + "</a>")
		}
		b.WriteString("</body></html>")
		return io.NopCloser(strings.NewReader(b.String())), nil
	}
}

func TestEnumerateIDs(t *testing.T) {
	base := "https://www.etsi.org/deliver/etsi_ts/"
	tree := map[string][]string{
		base:                    {"103200_103299/", "102200_102299/", "not_a_range/", "README.txt"},
		base + "103200_103299/": {"10322101/", "10322102/", "103280/", "../"},
		base + "102200_102299/": {"10223201/"},
	}
	ids, failed := EnumerateIDs(dirFetcher(tree), "etsi_ts")
	if len(failed) != 0 {
		t.Fatalf("unexpected failures: %v", failed)
	}
	want := []string{"102 232-1", "103 221-1", "103 221-2", "103 280"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("EnumerateIDs = %v, want %v", ids, want)
	}
}

func TestEnumerateIDsRootFetchFails(t *testing.T) {
	fetch := func(string) (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF }
	ids, failed := EnumerateIDs(fetch, "etsi_ts")
	if len(ids) != 0 || len(failed) != 1 || failed[0] != "etsi_ts" {
		t.Fatalf("root fetch failure must report the type dir: ids=%v failed=%v", ids, failed)
	}
}
