package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The corpus snapshot is TWO files that are one artefact.
//
// `3gpp.duckdb.zst` is the corpus; `corpus-index.json` is the delta anchor that
// describes it. Published as independent objects they can be replaced
// independently, and a consumer can end up pairing generation N of one with
// generation N+1 of the other. Nothing downstream would notice: discover would
// happily skip specs on the authority of an anchor that describes a corpus this
// machine does not have.
//
// `seedAnchor` already refuses to adopt an anchor unless the local DB hashes to
// the published DB — that half was covered. The uncovered half is the anchor
// itself, which shipped with no checksum of any kind. The manifest closes it by
// making both hashes assertions of the SAME document.
//
// Note what this does NOT fix: the 56 known holes are a content defect (catalogue
// rows with no clause text) in a DB and anchor that provably come from the same
// generation — anchorcheck reports zero divergence against spec_versions. A
// manifest would never have caught them. Different failure, also worth closing.

// corpusManifestName is the published object joining the two.
const corpusManifestName = "corpus-manifest.json"

// CorpusManifest is the published description of one snapshot generation.
type CorpusManifest struct {
	Schema int        `json:"schema"`
	DB     FileDigest `json:"db"`
	Anchor FileDigest `json:"anchor"`
	// EmbeddingModel is the identity the corpus vectors were built with. A
	// consumer whose query embedder disagrees cannot use the vectors at all, so
	// knowing it BEFORE downloading 670 MB is worth the line.
	EmbeddingModel string         `json:"embedding_model,omitempty"`
	Stats          map[string]int `json:"corpus_stats,omitempty"`
}

// FileDigest identifies one published file.
type FileDigest struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// WriteCorpusManifest builds the manifest for a snapshot about to be published.
// It hashes what is on disk rather than trusting whatever produced it, because a
// manifest derived from the same assumptions as the artefact proves nothing.
func WriteCorpusManifest(out, dbPath, anchorPath, embeddingModel string, stats map[string]int) error {
	db, err := digest(dbPath)
	if err != nil {
		return err
	}
	anchor, err := digest(anchorPath)
	if err != nil {
		return err
	}
	m := CorpusManifest{Schema: 1, DB: db, Anchor: anchor, EmbeddingModel: embeddingModel, Stats: stats}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(out, append(b, '\n'))
}

func digest(path string) (FileDigest, error) {
	st, err := os.Stat(path)
	if err != nil {
		return FileDigest{}, err
	}
	sum, err := sha256File(path)
	if err != nil {
		return FileDigest{}, err
	}
	return FileDigest{SHA256: strings.ToLower(sum), Size: st.Size()}, nil
}

// VerifyAgainstManifest checks a local file against the digest the manifest
// declares for it. A size mismatch is reported separately from a hash mismatch:
// a truncated download and a genuinely different generation need different
// reactions, and "checksum failed" hides which one happened.
func VerifyAgainstManifest(path string, want FileDigest) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if want.Size > 0 && st.Size() != want.Size {
		return fmt.Errorf("%s is %d bytes, the manifest declares %d — the download is incomplete",
			filepath.Base(path), st.Size(), want.Size)
	}
	got, err := sha256File(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want.SHA256) {
		return fmt.Errorf("%s hashes to %s, the manifest declares %s — this file is from a different generation",
			filepath.Base(path), short(got), short(want.SHA256))
	}
	return nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// verifyAnchorAgainstManifest checks a freshly downloaded anchor against the
// published manifest.
//
// A MISSING manifest is not a failure: publishes predating it exist, and refusing
// them would break seeding for everyone to close a hole nobody has hit. A PRESENT
// manifest that disagrees IS a failure — that is the case it was added for.
func verifyAnchorAgainstManifest(c *Ctx, anchorFile string) error {
	const manifestURL = "https://github.com/kodflow/3gpp-mcp/releases/download/latest/" + corpusManifestName

	raw, err := c.Output(Cmd{Name: "curl", Args: []string{"-fsSL", "--max-time", "60", manifestURL}})
	if err != nil || strings.TrimSpace(raw) == "" {
		c.Log.Printf("no published %s — the anchor's integrity is UNVERIFIED (legacy publish)", corpusManifestName)
		return nil
	}
	var m CorpusManifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		c.Log.Printf("published %s is not valid JSON — treating the anchor as unverified", corpusManifestName)
		return nil
	}
	if m.Anchor.SHA256 == "" {
		c.Log.Printf("published %s declares no anchor digest — unverified", corpusManifestName)
		return nil
	}
	if err := VerifyAgainstManifest(anchorFile, m.Anchor); err != nil {
		return err
	}
	c.Log.Printf("anchor verified against %s (%s)", corpusManifestName, short(m.Anchor.SHA256))
	if m.EmbeddingModel != "" {
		c.Log.Printf("published corpus was vectorised with embedding model %s", m.EmbeddingModel)
	}
	return nil
}
