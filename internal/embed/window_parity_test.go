package embed

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mean_pool word split exists twice: here in Go, and in rust/embedder/src/window.rs,
// which is the one that actually embeds the corpus. `windowing` is an EmbedIdentity
// component, so if the two ever disagreed about where a clause splits, the corpus would be
// embedded under one split and queried under another — a silent quality regression no other
// gate can see, because both sides would still produce 1024 well-formed floats.
//
// So the split is pinned in ONE file, testdata/window_parity.json, generated from this
// (reference) implementation and asserted by both languages. Regenerate deliberately:
//
//	go test ./internal/embed -run TestWindowParityFixture -update
//
// Changing the fixture changes what the embedder does, which changes EmbedIdentity, which
// means a full re-embed. That is the point: it should not be possible to do by accident.
//
// EVERY CASE IS SYNTHETIC, and must stay that way. DATA_NOTICE.md forbids verbatim clause
// text on any public channel, and this repository is one — a fixture built from real
// 23.501 tables and 33.128 ASN.1 would be a small corpus export committed to a public git
// history, which is exactly what the notice rules out. The split logic does not care whose
// words it splits, so the cases below reproduce the SHAPES that matter (long tables, ASN.1
// blocks, dense identifier soup, ragged whitespace) without reproducing anyone's text.
// Validation against real long clauses belongs on the GPU, against the private corpus.
var updateFixture = flag.Bool("update", false, "regenerate testdata/window_parity.json from this implementation")

type parityCase struct {
	Name     string   `json:"name"`
	MaxWords int      `json:"max_words"`
	Text     string   `json:"text"`
	Windows  []string `json:"windows"`
}

// repeatWords builds n distinct words, so a window's boundary is identifiable by eye when
// a case fails.
func repeatWords(prefix string, n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return strings.Join(w, " ")
}

// tableLike imitates the shape of a specification table: short pipe-delimited cells, many
// of them, which is what makes a 300-word window tokenise far past prose rates.
func tableLike(rows int) string {
	var b strings.Builder
	b.WriteString("| Parameter | Presence | Range | Description |\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "| param_%d_id | M | 0..%d | value of parameter %d in the set |\n", i, i*7+3, i)
	}
	return b.String()
}

// asn1Like imitates an ASN.1 block: long dotted-and-underscored identifiers with almost no
// natural-language words, the densest tokens-per-word content in the corpus.
func asn1Like(fields int) string {
	var b strings.Builder
	b.WriteString("SomeContainer ::= SEQUENCE {\n")
	for i := 0; i < fields; i++ {
		fmt.Fprintf(&b, "    fieldNameNumber%d [%d] SomeOtherType%d OPTIONAL,\n", i, i, i%13)
	}
	b.WriteString("}\n")
	return b.String()
}

func parityCases() []parityCase {
	return []parityCase{
		{Name: "empty", MaxWords: 300, Text: ""},
		{Name: "one-word", MaxWords: 300, Text: "AMF"},
		// Irregular whitespace: the single-window path must hand back these exact bytes.
		{Name: "ragged-whitespace-short", MaxWords: 300, Text: "alpha   beta\tgamma\n\n  delta "},
		{Name: "exactly-max", MaxWords: 300, Text: repeatWords("w", 300)},
		{Name: "one-over-max", MaxWords: 300, Text: repeatWords("w", 301)},
		{Name: "several-windows", MaxWords: 300, Text: repeatWords("w", 750)},
		{Name: "small-max-words", MaxWords: 7, Text: repeatWords("w", 20)},
		// max_words < 1 must fall back to the default, not panic or yield one window.
		{Name: "zero-max-words-uses-default", MaxWords: 0, Text: repeatWords("w", 700)},
		// Multi-byte content must split on words, never inside a rune.
		{Name: "non-ascii", MaxWords: 3, Text: "réf ↔ ASN.1 « clause » naïve fenêtre"},
		// A trailing window of exactly one word.
		{Name: "remainder-of-one", MaxWords: 10, Text: repeatWords("w", 11)},
		// The shapes that motivated #208. Newline-heavy, so they also pin that the split
		// treats a newline as a word separator exactly as strings.Fields does.
		{Name: "table-small", MaxWords: 300, Text: tableLike(40)},
		{Name: "table-large", MaxWords: 300, Text: tableLike(400)},
		{Name: "asn1-small", MaxWords: 300, Text: asn1Like(30)},
		{Name: "asn1-large", MaxWords: 300, Text: asn1Like(500)},
		// Prose long enough to window several times over.
		{Name: "long-prose", MaxWords: 300, Text: repeatWords("clause", 2400)},
		// A single token far longer than any window: nothing to split on.
		{Name: "one-enormous-word", MaxWords: 300, Text: strings.Repeat("A", 4000)},
	}
}

func TestWindowParityFixture(t *testing.T) {
	cases := parityCases()
	for i := range cases {
		cases[i].Windows = windowText(cases[i].Text, cases[i].MaxWords)
	}

	path := filepath.Join("testdata", "window_parity.json")
	if *updateFixture {
		b, err := json.MarshalIndent(cases, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("regenerated %s with %d cases", path, len(cases))
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the fixture (regenerate with -update): %v", err)
	}
	var want []parityCase
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(cases) {
		t.Fatalf("fixture holds %d cases, this implementation produced %d — regenerate deliberately, it changes EmbedIdentity", len(want), len(cases))
	}
	for i, c := range cases {
		if want[i].Name != c.Name {
			t.Fatalf("case %d: fixture is %q, got %q", i, want[i].Name, c.Name)
		}
		if want[i].Text != c.Text {
			t.Fatalf("case %q: the input text drifted from the fixture", c.Name)
		}
		if len(want[i].Windows) != len(c.Windows) {
			t.Fatalf("case %q: fixture has %d windows, got %d", c.Name, len(want[i].Windows), len(c.Windows))
		}
		for j := range c.Windows {
			if want[i].Windows[j] != c.Windows[j] {
				t.Fatalf("case %q window %d differs from the fixture", c.Name, j)
			}
		}
	}
}

// TestWindowingLosesNothing is the property the whole feature exists for: truncation
// dropped the tail of a long clause, and the split must not drop anything at all.
func TestWindowingLosesNothing(t *testing.T) {
	for _, c := range parityCases() {
		got := windowText(c.Text, c.MaxWords)
		var joined []string
		for _, w := range got {
			joined = append(joined, strings.Fields(w)...)
		}
		want := strings.Fields(c.Text)
		if len(joined) != len(want) {
			t.Fatalf("case %q: %d words in, %d words across %d windows", c.Name, len(want), len(joined), len(got))
		}
		for i := range want {
			if joined[i] != want[i] {
				t.Fatalf("case %q: word %d is %q, want %q", c.Name, i, joined[i], want[i])
			}
		}
	}
}

// TestFixtureCarriesNoCorpusText guards the rule above mechanically. A future "let's pin it
// against a real clause" is the natural thing to do and is precisely what DATA_NOTICE.md
// forbids on a public repository, so the fixture is required to stay synthetic.
func TestFixtureCarriesNoCorpusText(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "window_parity.json"))
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	// Every case is generated by parityCases(); anything else means real text crept in.
	var got []parityCase
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, c := range parityCases() {
		want[c.Name] = c.Text
	}
	for _, c := range got {
		w, ok := want[c.Name]
		if !ok {
			t.Fatalf("fixture case %q is not generated by parityCases() — the fixture must stay synthetic (DATA_NOTICE.md)", c.Name)
		}
		if w != c.Text {
			t.Fatalf("fixture case %q does not match its generator — regenerate with -update", c.Name)
		}
	}
	if entries, err := os.ReadDir("testdata"); err == nil {
		for _, e := range entries {
			if e.Name() != "window_parity.json" {
				t.Fatalf("unexpected file testdata/%s — corpus samples must not be committed (DATA_NOTICE.md)", e.Name())
			}
		}
	}
}
