//go:build onnx

package rerank

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
)

func rerankerTokenizer(t *testing.T) *tokenizer.Tokenizer {
	t.Helper()
	dir := envOr("BGE_RERANKER_DIR", filepath.Join("..", "..", "data", "models", "bge-reranker-v2-m3"))
	tok, err := pretrained.FromFile(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		if _, serr := os.Stat(filepath.Join(dir, "tokenizer.json")); os.IsNotExist(serr) {
			t.Skipf("no reranker tokenizer at %s", dir)
		}
		t.Fatal(err)
	}
	return tok
}

// repeatTo repeats unit until the result is at least n bytes.
func repeatTo(unit string, n int) string {
	return strings.Repeat(unit, n/len(unit)+1)
}

// THE CROSS-ENCODER SEES THE SAME IDS WHETHER THE PASSAGE IS TOKENIZED WHOLE OR
// CUT FIRST — on every shape that could make a prefix tokenize differently from
// the start of the whole: runs of spaces (the library leaves a stray ▁ in "a  b"),
// newlines and tabs, paragraph breaks, accents and other multi-byte text around
// the cut, a special token's text, words longer than the window, a passage that
// ends in whitespace (the tokenizer panics there and the whole passage is
// folded), a passage with no ASCII space at all, and one whose cut yields fewer
// ids than the window so the prefix has to grow.
//
// Falsified: cutting inside a word (windowPrefix without its word-end check) or
// dropping the endsInSpace fold makes a shape below disagree.
func TestTheWindowIsTheWholePassages(t *testing.T) {
	tok := rerankerTokenizer(t)
	const q = "AMF registration procedure over N1"
	english := "The AMF shall send the Registration Accept message to the UE, including the 5G-GUTI; " +
		"see clause 5.3.2 and TS 24.501 [22]. "
	passages := map[string]string{
		"plain":            "Registration procedures\n" + repeatTo(english, 6000),
		"double spaces":    "Heading\n" + repeatTo("Step  Direction    Description  RQ ", 6000),
		"paragraphs":       "Heading\n" + repeatTo(english+"\n\n", 6000),
		"tabs and CR":      "Heading\r\n" + repeatTo("a\tb\r\nc d ", 6000),
		"accents":          "Présentation\n" + repeatTo("réseau d'accès où l'élément ç̧ é é ", 6000),
		"special token":    "Heading\n" + repeatTo("the <mask> token and <s> here ", 6000),
		"long words":       "Heading\n" + repeatTo(strings.Repeat("x", 700)+" ", 9000),
		"ends in newline":  "Heading\n" + repeatTo(english, 6000) + "\n",
		"ends in spaces":   "Heading\n" + repeatTo(english, 6000) + "   ",
		"no ascii space":   "見出し\n" + repeatTo("登録手順は次のとおりである。", 6000),
		"short":            "5.1 Scope\nThis clause specifies the registration procedure.",
		"empty body":       "Foreword\n",
		"space run later":  "Heading\n" + repeatTo(english, 3000) + strings.Repeat(" ", 40) + repeatTo(english, 3000),
		"numbers and dots": "Heading\n" + repeatTo("1.2.3 4.5.6 7 8 9 10.0.0 v19.4.0 ", 6000),
	}
	for name, p := range passages {
		whole, err := encodePair(tok, q, p)
		if err != nil {
			t.Fatalf("%s: the whole passage does not encode: %v", name, err)
		}
		got, err := windowIDs(tok, q, p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := clipIDs(whole.Ids); !slices.Equal(got, want) {
			i := 0
			for i < len(got) && i < len(want) && got[i] == want[i] {
				i++
			}
			t.Errorf("%s: the window differs from the whole passage's at id %d (got %d ids, want %d)",
				name, i, len(got), len(want))
		}
	}
}

// The tokenizer is shared by the goroutines scoreBatch starts; encoding the same
// passages in parallel must give the ids a serial pass gives (run under -race to
// check the library's shared state too).
func TestWindowIDsInParallelMatchSerial(t *testing.T) {
	tok := rerankerTokenizer(t)
	const q = "session management"
	var ps []string
	for i := 0; i < 12; i++ {
		ps = append(ps, "Heading\n"+repeatTo(strings.Repeat("word ", i+1)+"PDU session establishment ", 3000))
	}
	serial := make([][]int, len(ps))
	for i, p := range ps {
		ids, err := windowIDs(tok, q, p)
		if err != nil {
			t.Fatal(err)
		}
		serial[i] = ids
	}
	par := make([][]int, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, err := windowIDs(tok, q, p)
			if err != nil {
				t.Error(err)
				return
			}
			par[i] = ids
		}()
	}
	wg.Wait()
	for i := range ps {
		if !slices.Equal(serial[i], par[i]) {
			t.Errorf("passage %d: parallel ids differ from serial", i)
		}
	}
}
