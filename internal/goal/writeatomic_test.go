package goal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mtimeOf is the only property under test here: inputsHash identifies an input by
// size and modification time, so the mtime IS the invalidation signal.
func mtimeOf(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime().UnixNano()
}

// TestRewritingIdenticalBytesDoesNotTouchTheFile is the whole fix.
//
// `discover` rewrites series.json and worklist.txt on every run, and it runs
// whenever the cached 3GPP status report crosses its 6 h TTL — a clock, not a
// change. Before this, those rewrites moved the mtime, and `fetch` declares both
// files as Inputs, so a heavy step went back on the plan to reproduce bytes
// nobody had touched. Measured 2026-09-04: two consecutive discover runs, output
// shas identical both times, fetch RUN both times.
func TestRewritingIdenticalBytesDoesNotTouchTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worklist.txt")
	body := []byte("Rel-16 https://example.invalid/23736-020.zip 23736-020.zip\n")

	if err := WriteAtomic(path, body); err != nil {
		t.Fatal(err)
	}
	before := mtimeOf(t, path)

	// A filesystem whose timestamps are coarse would make this pass for the wrong
	// reason, so put real distance between the two writes.
	time.Sleep(20 * time.Millisecond)

	if err := WriteAtomic(path, body); err != nil {
		t.Fatal(err)
	}
	if after := mtimeOf(t, path); after != before {
		t.Errorf("rewriting identical bytes moved the mtime (%d -> %d): every consumer of this file is now invalidated for nothing", before, after)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("the file no longer holds what was written: %q", got)
	}
}

// THE NEGATIVE CONTROL. Not writing an unchanged file must never become not
// writing a changed one: that would leave a consumer reading stale bytes while
// its fingerprint says it is current, which is worse than the churn being fixed.
func TestARealChangeIsAlwaysWritten(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][2]string{
		"different content, same length": {"aaaa", "bbbb"},
		"longer":                         {"aaaa", "aaaaa"},
		"shorter":                        {"aaaa", "aaa"},
		"emptied":                        {"aaaa", ""},
		"filled from empty":              {"", "aaaa"},
		"a single trailing newline":      {"a", "a\n"},
	}
	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, "f-"+name)
			if err := WriteAtomic(path, []byte(pair[0])); err != nil {
				t.Fatal(err)
			}
			before := mtimeOf(t, path)
			time.Sleep(20 * time.Millisecond)

			if err := WriteAtomic(path, []byte(pair[1])); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != pair[1] {
				t.Fatalf("a changed file was not written: have %q, want %q", got, pair[1])
			}
			if after := mtimeOf(t, path); after == before {
				t.Errorf("content changed but the mtime did not move, so no consumer will notice")
			}
		})
	}
}

// A first write still has to create the file, parent directories included.
func TestWriteAtomicStillCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "series.json")
	if err := WriteAtomic(path, []byte("[\"21\"]")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[\"21\"]" {
		t.Errorf("first write did not land: %q", got)
	}
}

// TestAnUnchangedEnumeratorLeavesItsConsumerAlone is the property the fix exists
// for, stated the way the pipeline sees it: the same bytes written twice must
// leave inputsHash — and therefore every consumer fingerprint — identical.
func TestAnUnchangedEnumeratorLeavesItsConsumerAlone(t *testing.T) {
	dir := t.TempDir()
	series := filepath.Join(dir, "series.json")
	worklist := filepath.Join(dir, "worklist.txt")
	writeBoth := func() {
		if err := WriteAtomic(series, []byte("[\"21\",\"23\"]\n")); err != nil {
			t.Fatal(err)
		}
		if err := WriteAtomic(worklist, []byte("Rel-16 https://example.invalid/a.zip a.zip\n")); err != nil {
			t.Fatal(err)
		}
	}

	writeBoth()
	first, _ := inputsHash([]string{series, worklist})
	time.Sleep(20 * time.Millisecond)
	writeBoth()
	second, _ := inputsHash([]string{series, worklist})

	if first != second {
		t.Errorf("an enumerator that produced identical output still changed its consumers' fingerprint (%s -> %s)", first, second)
	}

	// And the control: real new work must still reach the consumer.
	time.Sleep(20 * time.Millisecond)
	if err := WriteAtomic(worklist, []byte("Rel-16 https://example.invalid/a.zip a.zip\nRel-17 https://example.invalid/b.zip b.zip\n")); err != nil {
		t.Fatal(err)
	}
	if third, _ := inputsHash([]string{series, worklist}); third == second {
		t.Error("a work list that gained an entry did not invalidate its consumer")
	}
}
