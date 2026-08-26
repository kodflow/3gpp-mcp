package goal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManifestPairsTheTwoFilesOfOneArtefact: the corpus and its anchor are one
// artefact published as two objects. The manifest exists so a consumer cannot
// pair generation N of one with generation N+1 of the other.
func TestManifestPairsTheTwoFilesOfOneArtefact(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "3gpp.duckdb")
	anchor := filepath.Join(dir, "corpus-index.json")
	write(t, db, "corpus generation N")
	write(t, anchor, `{"23.501|Rel-19":"19.5.0"}`)

	out := filepath.Join(dir, corpusManifestName)
	if err := WriteCorpusManifest(out, db, anchor, "61ba446c0814", map[string]int{"specs": 1}); err != nil {
		t.Fatal(err)
	}
	var m CorpusManifest
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.DB.SHA256 == "" || m.Anchor.SHA256 == "" {
		t.Fatal("both files must be digested; a manifest covering one of them proves nothing")
	}
	if m.DB.SHA256 == m.Anchor.SHA256 {
		t.Fatal("the two digests are identical — the wrong file was hashed twice")
	}
	if m.EmbeddingModel != "61ba446c0814" {
		t.Errorf("embedding model = %q; a consumer must learn it before downloading 670 MB", m.EmbeddingModel)
	}

	// The matching pair verifies.
	if err := VerifyAgainstManifest(db, m.DB); err != nil {
		t.Fatalf("the file the manifest describes must verify: %v", err)
	}
	if err := VerifyAgainstManifest(anchor, m.Anchor); err != nil {
		t.Fatalf("the anchor the manifest describes must verify: %v", err)
	}

	// THE case this exists for: an anchor republished after the DB.
	write(t, anchor, `{"23.501|Rel-19":"19.6.0"}`)
	err = VerifyAgainstManifest(anchor, m.Anchor)
	if err == nil {
		t.Fatal("an anchor from another generation was accepted")
	}
	if !strings.Contains(err.Error(), "different generation") {
		t.Errorf("error %q should name the actual problem, not just 'checksum failed'", err)
	}
}

// TestTruncatedDownloadIsDistinguishedFromAWrongGeneration: they need different
// reactions — retry versus stop and investigate — and one message for both hides
// which happened.
func TestTruncatedDownloadIsDistinguishedFromAWrongGeneration(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "corpus-index.json")
	write(t, f, "the complete file")
	d, err := digest(f)
	if err != nil {
		t.Fatal(err)
	}
	write(t, f, "the complete")
	err = VerifyAgainstManifest(f, d)
	if err == nil {
		t.Fatal("a truncated file was accepted")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error %q should say the download is incomplete", err)
	}
}
