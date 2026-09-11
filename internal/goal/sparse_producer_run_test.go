package goal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runSparse, driven end to end over stand-ins for the three binaries it launches:
// cmd/embedid, embed-io and embed-core-sparse. The stand-ins are THIS test binary,
// copied under their names; init() below turns it into the tool its name says
// before the testing package parses a single flag. (An init, not a TestMain: a
// package has one TestMain, and this one is shared.)
//
// What the stand-ins cannot fake is DuckDB, and they do not try: the SQL side of a
// re-encode (--export-sparse-all, --import-sparse-replace) is held by
// rust/store/tests/sparse_reencode.rs against a real corpus. These tests hold the
// DECISION — which of those flags runSparse passes, when, and what it does to the
// ledger and the record around them.

const (
	fakeSparseToolsEnv = "GOAL_FAKE_SPARSE_TOOLS" // the call log; set = act as a tool
	fakeSparseIDEnv    = "GOAL_FAKE_SPARSE_ID"    // what embedid --sparse prints
	fakeSparseTodoEnv  = "GOAL_FAKE_SPARSE_TODO"  // clauses with no posting
	fakeSparseAllEnv   = "GOAL_FAKE_SPARSE_ALL"   // every embeddable clause
	fakeSparseFailEnv  = "GOAL_FAKE_SPARSE_FAIL"  // embed-core-sparse dies after one line
	fakeImportFailEnv  = "GOAL_FAKE_IMPORT_FAIL"  // embed-io --import-sparse dies
)

func init() {
	if os.Getenv(fakeSparseToolsEnv) == "" {
		return
	}
	os.Exit(fakeSparseTool(strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"), os.Args[1:]))
}

func fakeSparseTool(name string, args []string) int {
	logf, err := os.OpenFile(os.Getenv(fakeSparseToolsEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer logf.Close()
	fmt.Fprintf(logf, "%s %s\n", name, strings.Join(args, " "))
	flag := func(f string) string {
		for i, a := range args {
			if a == f && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	has := func(f string) bool {
		for _, a := range args {
			if a == f {
				return true
			}
		}
		return false
	}
	switch name {
	case "embedid":
		fmt.Println(os.Getenv(fakeSparseIDEnv))
		return 0
	case "embed-io":
		if w := flag("--export-sparse-worklist"); w != "" {
			n, _ := strconv.Atoi(os.Getenv(fakeSparseTodoEnv))
			if has("--export-sparse-all") {
				n, _ = strconv.Atoi(os.Getenv(fakeSparseAllEnv))
			}
			var b strings.Builder
			for i := 1; i <= n; i++ {
				fmt.Fprintf(&b, "{\"chunk_id\":%d,\"heading\":\"H\",\"text\":\"clause %d\"}\n", i, i)
			}
			if err := os.WriteFile(w, []byte(b.String()), 0o644); err != nil {
				return 2
			}
			return 0
		}
		if l := flag("--import-sparse"); l != "" {
			if os.Getenv(fakeImportFailEnv) == "1" {
				fmt.Fprintln(logf, "import died")
				return 1
			}
			fmt.Fprintf(logf, "imported=%d\n", countLines(l))
			return 0
		}
		return 2
	case "embed-core-sparse":
		in, out := flag("--in"), flag("--out")
		done := map[uint64]bool{}
		if f, err := os.Open(out); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				var d struct {
					ChunkID uint64 `json:"chunk_id"`
				}
				if json.Unmarshal(sc.Bytes(), &d) == nil {
					done[d.ChunkID] = true
				}
			}
			f.Close()
		}
		w, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return 2
		}
		defer w.Close()
		f, err := os.Open(in)
		if err != nil {
			return 2
		}
		defer f.Close()
		embedded := 0
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var it struct {
				ChunkID uint64 `json:"chunk_id"`
				Heading string `json:"heading"`
				Text    string `json:"text"`
			}
			if json.Unmarshal(sc.Bytes(), &it) != nil || done[it.ChunkID] {
				continue
			}
			fmt.Fprintf(w, "{\"chunk_id\":%d,\"h\":%q,\"terms\":[[7,0.5]]}\n", it.ChunkID, sparseResumeHash(it.Heading, it.Text))
			embedded++
			if os.Getenv(fakeSparseFailEnv) == "1" {
				fmt.Fprintf(logf, "embedded=%d (died)\n", embedded)
				return 1
			}
		}
		fmt.Fprintf(logf, "embedded=%d\n", embedded)
		return 0
	}
	return 2
}

// sparseRig is a Ctx whose tools are the stand-ins, over a producer tree and a
// sparse model stub.
type sparseRig struct {
	c    *Ctx
	t    corpusTarget
	log  string
	work string
	out  string
}

func newSparseRig(t *testing.T) *sparseRig {
	t.Helper()
	c, _ := newTestCtx(t)
	for rel, body := range map[string]string{
		sparseProducerCrate + "/Cargo.toml":         "[package]\nname = \"embed-core\"\n",
		sparseProducerCrate + "/Cargo.lock":         "version = 3\n",
		sparseProducerCrate + "/src/lib.rs":         "pub fn f() {}\n",
		sparseProducerCrate + "/src/ort_backend.rs": "pub fn g() {}\n",
		sparseProducerBin:                           "fn main() {}\n",
		sparseProducerCrate + "/src/bin/check.rs":   "fn main() {}\n",
	} {
		write(t, filepath.Join(c.Root, filepath.FromSlash(rel)), body)
	}
	write(t, c.dataPath("models", sparseModelName, "model.onnx"), "stub")

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{c.bin("embedid"), c.rbin("embed-io"), c.rbin("embed-core-sparse")} {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		// A copy, not a hard link: Windows refuses to delete a link to an image that
		// is still mapped — this test process — and t.TempDir's cleanup would fail.
		copyFile(t, self, dst)
	}
	r := &sparseRig{c: c, t: corpus3GPP(), log: filepath.Join(c.Root, "calls.log")}
	r.work, r.out = r.t.sparseFiles(c)
	t.Setenv(fakeSparseToolsEnv, r.log)
	t.Setenv(fakeSparseIDEnv, "model-1")
	t.Setenv(fakeSparseTodoEnv, "0")
	t.Setenv(fakeSparseAllEnv, "3")
	t.Setenv(fakeSparseFailEnv, "")
	t.Setenv(fakeImportFailEnv, "")
	return r
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// current is the producer the rig's tree and model describe.
func (r *sparseRig) current(t *testing.T) sparseProducer {
	t.Helper()
	cur, err := sparseProducerIdentity(r.c, os.Getenv(fakeSparseIDEnv))
	if err != nil {
		t.Fatal(err)
	}
	return cur
}

// run is one attempt of the step: the log is cleared, so calls() describes it alone.
func (r *sparseRig) run(t *testing.T, prev *Record) error {
	t.Helper()
	_ = os.Remove(r.log)
	r.c.previous = prev
	return runSparse(r.c, r.t)
}

func (r *sparseRig) calls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if err != nil {
		return ""
	}
	return string(b)
}

func (r *sparseRig) state(t *testing.T) *sparseProducerState {
	t.Helper()
	b, err := os.ReadFile(sparseProducerStatePath(r.c, r.t))
	if err != nil {
		t.Fatalf("no producer record after the run: %v", err)
	}
	var st sparseProducerState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

// recordOf is the sparse record the step wrote before this mechanism existed: the
// producer's sources and the model, never its manifest or lockfile.
func recordOf(cur sparseProducer) *Record {
	impl := map[string]string{}
	for k, v := range cur.Impl {
		if !strings.HasSuffix(k, "Cargo.toml") && !strings.HasSuffix(k, "Cargo.lock") {
			impl[k] = v
		}
	}
	return &Record{Step: "sparse", Status: StatusSuccess, Declined: true,
		Environment: map[string]string{"sparse_identity": cur.Model}, Impl: impl}
}

// THE PRICE CONSTRAINT: the first run after this change, over a corpus whose every
// clause already carries a posting, adopts the producer and DECLINES. It launches
// nothing that writes — no --export-sparse-all, no GPU, no import — so the corpus
// file is not opened for writing and no layer moves.
func TestTheFirstRunAdoptsTheProducerAndTouchesNothing(t *testing.T) {
	r := newSparseRig(t)
	cur := r.current(t)
	write(t, r.out, "{\"chunk_id\":1,\"h\":\"x\",\"terms\":[]}\n") // an old ledger, no sidecar

	err := r.run(t, recordOf(cur))
	if !Declined(err) {
		t.Fatalf("the first run did not decline: %v\ncalls:\n%s", err, r.calls(t))
	}
	calls := r.calls(t)
	for _, never := range []string{"--export-sparse-all", "embed-core-sparse", "--import-sparse"} {
		if strings.Contains(calls, never) {
			t.Errorf("the adopting run launched %q — the first build would rewrite both corpora:\n%s", never, calls)
		}
	}
	st := r.state(t)
	if st.Pending || st.Producer == nil || st.Producer.digest() != cur.digest() {
		t.Errorf("the adoption recorded %+v, want the current producer", st)
	}
	if !fileNonEmpty(r.out) {
		t.Error("the adopting run archived the postings ledger of the producer it adopted")
	}
}

// AN UNCHANGED PRODUCER DECLINES, and a CHANGED one re-encodes every clause: the
// full work list, the GPU over all of it with the old ledger out of the way, and a
// replace import — never the changed-only one, which would keep the old postings.
func TestAChangedProducerReencodesEveryClauseAndAnUnchangedOneDeclines(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, r *sparseRig)
	}{
		{"a lockfile bump", func(t *testing.T, r *sparseRig) {
			write(t, filepath.Join(r.c.Root, filepath.FromSlash(sparseProducerCrate+"/Cargo.lock")), "version = 3\n# bumped\n")
		}},
		{"a manifest edit", func(t *testing.T, r *sparseRig) {
			write(t, filepath.Join(r.c.Root, filepath.FromSlash(sparseProducerCrate+"/Cargo.toml")), "[package]\nname = \"embed-core\"\n# ort bump\n")
		}},
		{"a fix in the library", func(t *testing.T, r *sparseRig) {
			write(t, filepath.Join(r.c.Root, filepath.FromSlash(sparseProducerCrate+"/src/lib.rs")), "pub fn f() { let _ = 1; }\n")
		}},
		{"a new sparse model", func(t *testing.T, r *sparseRig) { t.Setenv(fakeSparseIDEnv, "model-2") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSparseRig(t)
			old := r.current(t)
			if err := r.run(t, recordOf(old)); !Declined(err) {
				t.Fatalf("setup: the adopting run did not decline: %v", err)
			}
			// The ledger the old producer appended to.
			write(t, r.out, "{\"chunk_id\":1,\"h\":\"stale\",\"terms\":[[7,0.1]]}\n")

			// Nothing changed: decline, and nothing is archived.
			if err := r.run(t, recordOf(old)); !Declined(err) {
				t.Fatalf("an unchanged producer did not decline: %v\n%s", err, r.calls(t))
			}
			if strings.Contains(r.calls(t), "--export-sparse-all") {
				t.Fatalf("an unchanged producer asked for every clause:\n%s", r.calls(t))
			}

			tc.change(t, r)
			cur := r.current(t)
			if cur.digest() == old.digest() {
				t.Fatal("fixture: the change did not move the producer")
			}
			if err := r.run(t, recordOf(old)); err != nil {
				t.Fatalf("the re-encode failed: %v\n%s", err, r.calls(t))
			}
			calls := r.calls(t)
			for _, want := range []string{"--export-sparse-all", "embedded=3", "--import-sparse-replace", "imported=3"} {
				if !strings.Contains(calls, want) {
					t.Errorf("a changed producer did not re-encode every clause: no %q in\n%s", want, calls)
				}
			}
			if strings.Contains(calls, "--import-sparse-changed-only") {
				t.Errorf("the re-encode imported changed-only, which keeps every posting already there:\n%s", calls)
			}
			if b, _ := os.ReadFile(r.out); strings.Contains(string(b), "stale") {
				t.Error("the old producer's ledger was resumed from: its postings would be imported as the new producer's")
			}
			if archives, _ := filepath.Glob(r.out + ".*.producer.bak"); len(archives) != 1 {
				t.Errorf("the old ledger was not archived (found %v)", archives)
			}
			st := r.state(t)
			if st.Pending || st.Producer == nil || st.Producer.digest() != cur.digest() {
				t.Errorf("after the re-encode the record is %+v, want the new producer", st)
			}

			// And the build after that declines again.
			if err := r.run(t, recordOf(cur)); !Declined(err) {
				t.Fatalf("the run after a completed re-encode did not decline: %v\n%s", err, r.calls(t))
			}
		})
	}
}

// A RE-ENCODE THAT DIES IS FINISHED BY THE NEXT RUN, and the retry resumes from
// the ledger the new producer started instead of archiving the work the GPU
// already did.
func TestAReencodeThatDiedIsFinishedAndResumed(t *testing.T) {
	r := newSparseRig(t)
	old := r.current(t)
	if err := r.run(t, recordOf(old)); !Declined(err) {
		t.Fatalf("setup: %v", err)
	}
	write(t, filepath.Join(r.c.Root, filepath.FromSlash(sparseProducerCrate+"/Cargo.lock")), "version = 3\n# bumped\n")

	t.Setenv(fakeSparseFailEnv, "1")
	if err := r.run(t, recordOf(old)); err == nil || Declined(err) {
		t.Fatalf("the dying re-encode reported %v", err)
	}
	if !r.state(t).Pending {
		t.Fatal("a re-encode that died left no pending mark: the retry would decline over a half-replaced layer")
	}
	if strings.Contains(r.calls(t), "--import-sparse") {
		t.Fatal("the import ran after the GPU pass died")
	}

	t.Setenv(fakeSparseFailEnv, "")
	if err := r.run(t, recordOf(old)); err != nil {
		t.Fatalf("the retry failed: %v\n%s", err, r.calls(t))
	}
	calls := r.calls(t)
	if !strings.Contains(calls, "embedded=2") {
		t.Errorf("the retry did not resume from the postings the dead run wrote (want 2 of 3 embedded):\n%s", calls)
	}
	if !strings.Contains(calls, "--import-sparse-replace") || !strings.Contains(calls, "imported=3") {
		t.Errorf("the retry did not finish the replace:\n%s", calls)
	}
	if archives, _ := filepath.Glob(r.out + ".*.producer.bak"); len(archives) != 0 {
		t.Errorf("the retry archived the ledger the new producer had started: %v", archives)
	}
	if st := r.state(t); st.Pending {
		t.Error("the finished re-encode is still pending")
	}
}

// THE PENDING MARK IS WHAT KEEPS A HALF-REPLACED LAYER FROM BEING DECLINED.
//
// --import-sparse-replace clears the layer before it loads the ledger, so an
// import that dies leaves a corpus with part of the new postings, or none. If the
// producer is then reverted — the commit that bumped the lockfile is backed out —
// the record without a pending mark would name the old producer, which is the
// current one again, and the step would decline over that corpus for good.
func TestAReplaceThatDiedIsFinishedEvenAfterTheProducerIsReverted(t *testing.T) {
	r := newSparseRig(t)
	lock := filepath.Join(r.c.Root, filepath.FromSlash(sparseProducerCrate+"/Cargo.lock"))
	orig, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	old := r.current(t)
	if err := r.run(t, recordOf(old)); !Declined(err) {
		t.Fatalf("setup: %v", err)
	}

	write(t, lock, string(orig)+"# bumped\n")
	t.Setenv(fakeImportFailEnv, "1")
	if err := r.run(t, recordOf(old)); err == nil || Declined(err) {
		t.Fatalf("the dying import reported %v", err)
	}

	write(t, lock, string(orig)) // the bump is reverted
	t.Setenv(fakeImportFailEnv, "")
	if r.current(t).digest() != old.digest() {
		t.Fatal("fixture: the revert did not restore the old producer")
	}
	err = r.run(t, recordOf(old))
	if Declined(err) {
		t.Fatal("the step declined over a layer whose replace died half way: the corpus keeps a partial sparse arm for good")
	}
	if err != nil {
		t.Fatalf("the retry failed: %v\n%s", err, r.calls(t))
	}
	if calls := r.calls(t); !strings.Contains(calls, "--import-sparse-replace") || !strings.Contains(calls, "imported=3") {
		t.Errorf("the retry did not replace the layer:\n%s", calls)
	}
}
