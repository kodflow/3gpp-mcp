package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ------------------------------------------------- sparse producer identity
//
// WHICH BUILD WROTE THE POSTINGS, and why the corpus alone cannot say.
//
// A dense vector carries its producer: clause_hash folds the EmbedIdentity in, so
// `embed-io --export-worklist --embed-identity X` finds every vector made by
// another embedder and re-embeds it. A sparse posting carries nothing of the
// kind. clause_sparse is (chunk_id, term_id, weight), and the only question the
// sparse work list could ask was "does this clause have ANY posting". So once
// every clause had one, runSparse declined whatever produced them: a new ort, a
// `cargo update` of tokenizers, a fix to the pooling in embed-core, even a new
// sparse model — the step replayed, exported a work list of 0, declined in 158 s,
// and carried its provenance forward over postings from the previous producer.
// The model case did not even stay silent in a useful way: the import is what
// stamps schema_meta.sparse_model, a decline never imports, so validate
// --require-sparse would then refuse the corpus on every build with nothing able
// to repair it.
//
// So the producer's identity is recorded beside the postings it wrote, and a
// different one makes runSparse RE-ENCODE every clause and REPLACE the layer.
//
// # WHERE IT IS RECORDED: .local/state, NOT the corpus
//
// schema_meta would travel with the corpus — a snapshot seeded from the registry
// would say who wrote its postings. But writing one key into a DuckDB file moves
// its bytes, and each corpus is ONE layer of the image: stamping the identity on
// the first build after this change would re-push 23 GB and 19.5 GB to record
// something nothing had changed. The state file costs nothing and moves no layer.
//
// What it gives up is exactly the seeded case, and that case has no better answer
// available anyway: a corpus pulled from the registry arrives with postings from
// whichever producer the publisher had, which no record here describes. It is
// ADOPTED, the same call the seed contract makes for its vectors ("the expensive
// half declines against the restored vectors"); re-encoding every seeded corpus
// on arrival is hours of GPU per fresh clone for postings that are almost always
// current. A state file left behind by an EARLIER corpus is never worse than that
// adoption: when it matches the current producer the step declines, which is what
// adoption would do, and when it does not the step re-encodes, the safe direction.

// sparseProducerCrate is the crate embed-core-sparse is compiled from.
const sparseProducerCrate = "rust/embed-core"

// sparseProducerBin is that binary's root source file, as its [[bin]] entry in
// rust/embed-core/Cargo.toml names it. TestTheSparseProducerIsTheClosureCargoCompiles
// reads the manifest, so a moved binary fails there rather than here.
const sparseProducerBin = sparseProducerCrate + "/src/bin/sparse_embed.rs"

// sparseProducerImpl is what cargo compiles into embed-core-sparse and what it
// resolves the compilation from: the manifest (dependency versions and features),
// the crate's OWN lockfile (rust/Cargo.toml excludes embed-core from the
// workspace, so rust/Cargo.lock decides nothing here), and the sources. The crate
// has no build.rs and no path dependency; the test above fails if it grows either.
//
// stepSparse declares these, so a change makes the step eligible to run at all,
// and sparseProducerIdentity hashes them, so the run knows whether the postings it
// finds were written by what it would write them with.
var sparseProducerImpl = []string{
	sparseProducerCrate + "/Cargo.toml",
	sparseProducerCrate + "/Cargo.lock",
	sparseProducerCrate + "/src",
}

// sparseProducer is the identity of what writes clause_sparse: the model the
// postings come from and, file by file, the code that computes them.
type sparseProducer struct {
	// Model is the sparse identity (cmd/embedid --sparse): model family, weights
	// and tokenizer revision, and the ONNX head. It is what the import stamps into
	// schema_meta.sparse_model.
	Model string `json:"sparse_identity"`
	// Impl is the per-file content hash of the producer's closure.
	Impl map[string]string `json:"implementation"`
}

// digest folds the whole identity into one value, for the postings ledger's
// sidecar, where one line has to say which producer appended to it.
func (p sparseProducer) digest() string {
	keys := make([]string, 0, len(p.Impl))
	for k := range p.Impl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	x := newHasher()
	x.add("sparse_identity", p.Model)
	for _, k := range keys {
		x.add(k, p.Impl[k])
	}
	return x.sum()
}

// sparseProducerIdentity hashes the producer's closure exactly as the fingerprint
// hashes an Impl, then drops the crate's OTHER binaries.
//
// src/bin holds one root file per binary, and embed-core-sparse compiles its own
// and not embed-core-check's. The step may watch the whole directory — a spurious
// replay there costs a two-minute work-list export and a decline — but THIS
// identity decides whether both corpora are re-encoded on the GPU and both layers
// re-pushed, so a false positive costs hours. A module the library grows under src/
// is still counted: it is compiled into every binary of the crate.
func sparseProducerIdentity(c *Ctx, model string) (sparseProducer, error) {
	_, per, err := implHash(c.Root, sparseProducerImpl, true)
	if err != nil {
		return sparseProducer{}, fmt.Errorf("the sparse producer's closure: %w", err)
	}
	for k := range per {
		if strings.HasPrefix(k, sparseProducerCrate+"/src/bin/") && k != sparseProducerBin {
			delete(per, k)
		}
	}
	return sparseProducer{Model: model, Impl: per}, nil
}

// sparseProducerState is what .local/state/sparse-producer<suffix>.json records.
type sparseProducerState struct {
	// Pending means a re-encode started and was not seen to complete: the layer may
	// hold postings from two producers, or none, and only finishing it repairs that.
	Pending bool `json:"pending,omitempty"`
	// Producer wrote every posting the corpus holds (at the last completed import,
	// or by adoption). Kept while Pending, so the log can say what was replaced.
	Producer *sparseProducer `json:"producer,omitempty"`
	// Adopted says how a record that no encode wrote came to exist. Informational:
	// no decision reads it.
	Adopted string `json:"adopted,omitempty"`
}

func sparseProducerStatePath(c *Ctx, t corpusTarget) string {
	return c.statePath("sparse-producer" + t.Suffix + ".json")
}

func saveSparseProducerState(c *Ctx, t corpusTarget, st sparseProducerState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	p := sparseProducerStatePath(c, t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return WriteAtomic(p, append(b, '\n'))
}

// loadSparseProducerState reads the record, or ADOPTS one on the first run that
// has none — the run after this mechanism landed, or a fresh clone.
//
// prev is the step's own record as it stood before this attempt. Adoption is
// refused only when that record CONTRADICTS the current producer: a sparse model
// or a producer source file that differs from what the last run saw. Everything
// short of a contradiction is adopted, which is what the step did before this
// existed and the only thing it can do about postings no record describes:
//
//   - prev == nil: the step never ran here, so the corpus is a seeded snapshot or
//     nothing at all (see the note on where the identity is recorded);
//   - a file prev never hashed: Cargo.toml and Cargo.lock, which no sparse record
//     carried before this change. They are taken as they are, and said so.
//
// A record nobody can read proves nothing, and returns nil: re-encode.
func loadSparseProducerState(c *Ctx, t corpusTarget, cur sparseProducer, prev *Record) (*sparseProducerState, error) {
	p := sparseProducerStatePath(c, t)
	b, err := os.ReadFile(p)
	if err == nil {
		var st sparseProducerState
		if err := json.Unmarshal(b, &st); err != nil || (!st.Pending && st.Producer == nil) {
			c.Log.Printf("%s is unreadable (%v) — re-encoding to re-establish which producer wrote %s",
				filepath.Base(p), err, t.DB)
			return nil, nil
		}
		return &st, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	how, unrecorded, ok := sparseAdoption(prev, cur)
	if !ok {
		return nil, nil
	}
	if len(unrecorded) > 0 {
		c.Log.Printf("sparse producer: %s never hashed %s — adopted as they are",
			how, strings.Join(unrecorded, ", "))
	}
	st := sparseProducerState{Producer: &cur, Adopted: how}
	if err := saveSparseProducerState(c, t, st); err != nil {
		return nil, err
	}
	c.Log.Printf("sparse producer adopted for %s (%s): the postings it holds are taken as the "+
		"output of the current producer, and nothing is re-encoded", t.DB, how)
	return &st, nil
}

// sparseAdoption is the adoption decision without its I/O. It returns how the
// record was adopted, which producer files the evidence never hashed, and whether
// adopting is allowed at all.
func sparseAdoption(prev *Record, cur sparseProducer) (how string, unrecorded []string, ok bool) {
	if prev == nil {
		return "no earlier sparse run on this machine (fresh clone or seeded corpus)", nil, true
	}
	how = fmt.Sprintf("the last sparse run (%s, %s)", prev.Status, prev.StartedAt.UTC().Format(time.RFC3339))
	if prev.Environment["sparse_identity"] != cur.Model {
		return how, nil, false
	}
	// The record must describe the producer at all: its binary's own source.
	if _, hashed := prev.Impl[sparseProducerBin]; !hashed {
		return how, nil, false
	}
	for k, v := range cur.Impl {
		old, hashed := prev.Impl[k]
		switch {
		case !hashed:
			unrecorded = append(unrecorded, k)
		case old != v:
			return how, nil, false
		}
	}
	sort.Strings(unrecorded)
	return how, unrecorded, true
}

// sparseReencodeReason says why every posting must be recomputed, or "" when the
// postings on record were written by the current producer. It is the whole
// decision, kept free of I/O so every case is testable.
func sparseReencodeReason(st *sparseProducerState, cur sparseProducer) string {
	switch {
	case st == nil:
		return "no record says which producer wrote the postings, and the last run's does not vouch for this one"
	case st.Pending:
		return "the previous re-encode did not complete"
	case st.Producer == nil:
		return "the record names no producer"
	case st.Producer.Model != cur.Model:
		return fmt.Sprintf("the sparse model changed (%s -> %s)", st.Producer.Model, cur.Model)
	case !sameMap(st.Producer.Impl, cur.Impl):
		return "the producer changed: " + summarise(diffKeys(st.Producer.Impl, cur.Impl))
	}
	return ""
}

// sparseLedgerProducerPath is the postings ledger's sidecar: which producer the
// lines in it were appended by. The dense ledger has had the same thing
// (ledger.jsonl.identity) since its identity could change.
func sparseLedgerProducerPath(ledger string) string { return ledger + ".producer" }

// ensureSparseLedgerProducer makes the postings ledger the current producer's
// before embed-core-sparse appends to it.
//
// embed-core-sparse resumes on (chunk_id, text hash): a line already in the
// ledger for the same text is trusted and never recomputed. Correct for a killed
// run, and exactly wrong for a ledger written by another producer — a re-encode
// over it would recompute nothing and import the old postings under the new
// stamp. So a ledger from another producer is archived, as the dense arm archives
// a ledger from another identity.
//
// A ledger with NO sidecar predates this mechanism. adopt says what that means:
// on the incremental path it is the adopted producer's (the step appended to it
// under the producer it now records), on the re-encode path it is not evidence of
// anything and is archived.
func ensureSparseLedgerProducer(c *Ctx, ledger string, cur sparseProducer, adopt bool) error {
	want := cur.digest()
	side := sparseLedgerProducerPath(ledger)
	had := ""
	if b, err := os.ReadFile(side); err == nil {
		had = strings.TrimSpace(string(b))
	}
	stale := had != want && (had != "" || !adopt)
	if stale && fileNonEmpty(ledger) {
		tag := had
		if tag == "" {
			tag = "unknown"
		}
		archive := fmt.Sprintf("%s.%s.%s.producer.bak", ledger, tag, time.Now().UTC().Format("20060102T150405Z"))
		c.Log.Printf("the sparse postings ledger was written by another producer (%s, now %s) — "+
			"archiving it to %s so every clause is recomputed", tag, want, filepath.Base(archive))
		if err := os.Rename(ledger, archive); err != nil {
			return err
		}
	}
	return WriteAtomic(side, []byte(want+"\n"))
}
