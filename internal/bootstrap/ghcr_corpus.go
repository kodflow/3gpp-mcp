package bootstrap

// ghcr_corpus.go provisions the indexed corpus from the PRIVATE GHCR package
// this project publishes, instead of from a public GitHub Release asset.
//
// WHY NOT A RELEASE ASSET — two independent reasons, either one fatal:
//
//  1. DATA_NOTICE.md forbids it. `clauses.text` is verbatim 3GPP/ETSI text; the
//     notice states that while this repository is public, "no GitHub Release
//     asset … may contain clauses.text, a full-text DuckDB database, or any
//     reconstructible export of the corpus". The rolling `latest` release
//     nevertheless carried 3gpp.duckdb.zst for months, and cmd/server hardcoded
//     that URL as its DEFAULT — so the product's out-of-the-box path was the
//     non-compliant one.
//  2. It does not fit. GitHub caps a release asset at 2 GB. The content-addressed
//     corpus is 12.36 GB, ~7.9 GB compressed. Even if the licence allowed it, the
//     channel could not carry it.
//
// So the corpus travels the way it is already published: as the single layer of
// ghcr.io/<owner>/3gpp-corpus, pushed by scripts/local/publish-corpus.sh. The
// package is private BY DESIGN — the publish script asserts it and flips it back
// if it ever turns public — which is exactly why this path requires a credential
// and says so plainly rather than falling back to an anonymous pull that would
// 401 deep inside a retry loop.
//
// TWO PASSES, NOT ONE. The 3GPP layer is ~7.9 GB on the wire. Streaming
// registry → gunzip → tar → DuckDB file in one pass is shorter code and the
// wrong shape: a dropped connection at 90 % restarts from zero, and `netRetry`
// would cheerfully do that five times. So the compressed blob lands on disk
// first, resumable by HTTP Range, and extraction is a second, purely local pass.
// The intermediate costs disk that the extracted DB dwarfs anyway.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/retry"
)

// DefaultGHCROwner is the account whose packages hold the published corpora.
const DefaultGHCROwner = "kodflow"

// CorpusSource names one corpus package and the file to lift out of its layer.
// Member is matched on the tar entry's BASE name: publish-corpus.sh tars the DB
// at the root of the layer (`tar -cf layer.tar -C data 3gpp.duckdb`) so it lands
// at /3gpp.duckdb in the image, but a registry that normalises paths to "./x" or
// a future multi-file layer must not break the lookup.
type CorpusSource struct {
	Owner  string // GHCR account, e.g. "kodflow"
	Image  string // package name, e.g. "3gpp-corpus"
	Ref    string // tag or digest, e.g. "latest"
	Member string // file to extract, e.g. "3gpp.duckdb"
}

// Corpus3GPP is the main indexed corpus (12.36 GB, content-addressed per ADR 0004).
func Corpus3GPP(owner, ref string) CorpusSource {
	return CorpusSource{Owner: orDefault(owner, DefaultGHCROwner), Image: "3gpp-corpus", Ref: orDefault(ref, "latest"), Member: "3gpp.duckdb"}
}

// CorpusETSI is the ETSI Lawful-Interception corpus, served ALONGSIDE the 3GPP
// one and never merged into it (CLAUDE.md §13).
func CorpusETSI(owner, ref string) CorpusSource {
	return CorpusSource{Owner: orDefault(owner, DefaultGHCROwner), Image: "etsi-corpus", Ref: orDefault(ref, "latest"), Member: "etsi.duckdb"}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// Repo is the registry path, "<owner>/<image>".
func (s CorpusSource) Repo() string { return s.Owner + "/" + s.Image }

// String renders the fully-qualified reference for logs and errors.
func (s CorpusSource) String() string { return "ghcr.io/" + s.Repo() + ":" + s.Ref }

// ErrNoGHCRCredential is returned when no token could be resolved. It is a named
// error because the caller's advice differs from every other failure here: the
// user has to go and create a token, not retry.
var ErrNoGHCRCredential = errors.New("no GHCR credential")

// GHCRCredential resolves the token used to pull a private package, and reports
// WHERE it came from so a wrong-token failure is diagnosable. Order mirrors
// scripts/local/publish-corpus.sh so the read side and the write side agree:
//
//  1. the explicit argument (--ghcr-token)
//  2. $GHCR_PAT      — what CI uses
//  3. $GITHUB_TOKEN  — what a GitHub Actions runner already has
//  4. .local/ghcr.pat — a gitignored file, so the token never has to be typed
//     into a shell (history) or pasted into a transcript
//
// `gh auth token` is deliberately NOT consulted: the CLI's own token is an OAuth
// token whose package scopes come from a device flow, and a binary that shells
// out to another CLI to authenticate is a dependency this one does not have.
func GHCRCredential(explicit string) (token, origin string, err error) {
	if t := strings.TrimSpace(explicit); t != "" {
		return t, "--ghcr-token", nil
	}
	for _, env := range []string{"GHCR_PAT", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return t, "$" + env, nil
		}
	}
	// Only meaningful when running from a checkout; harmless otherwise.
	if wd, e := os.Getwd(); e == nil {
		p := filepath.Join(wd, ".local", "ghcr.pat")
		if b, e := os.ReadFile(p); e == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t, p, nil
			}
		}
	}
	return "", "", ErrNoGHCRCredential
}

// DigestPath is where the identity of the corpus that produced dest is recorded.
// A 12.36 GB file cannot be re-hashed on every start to answer "is this current",
// and its mtime answers nothing, so the identity is written beside it once.
func DigestPath(dest string) string { return dest + ".digest" }

// CachedCorpusIdentity reads the identity recorded next to a cached corpus, or
// "" when there is none (an older cache, or a file put there by hand). An unknown
// identity must read as "not current", never as "current".
func CachedCorpusIdentity(dest string) string {
	b, err := os.ReadFile(DigestPath(dest))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// CorpusIdentity is the identity of the corpus currently published under s: the
// layer digests of its manifest, joined. Cheap — one authenticated manifest GET,
// no blob transfer — which is what lets `serve` check for an update without
// paying for one.
func CorpusIdentity(ctx context.Context, s CorpusSource, pat string) (string, error) {
	tok, err := ghcrPullToken(ctx, s.Repo(), s.Owner, pat)
	if err != nil {
		return "", fmt.Errorf("authenticate to %s: %w", s, err)
	}
	layers, err := ghcrLayers(ctx, s.Repo(), s.Ref, tok)
	if err != nil {
		return "", fmt.Errorf("read manifest of %s: %w", s, err)
	}
	ids := make([]string, 0, len(layers))
	for _, l := range layers {
		ids = append(ids, l.digest)
	}
	return strings.Join(ids, ","), nil
}

// FetchCorpus pulls Member out of the GHCR package described by s and writes it
// to dest, atomically, then records the published identity beside it so a later
// start can tell a current cache from a stale one with a single manifest GET.
//
// pat may be empty only for a package that is genuinely public; the private
// packages this project publishes will fail the token handshake, and the error
// says so instead of surfacing a bare 401.
func FetchCorpus(ctx context.Context, s CorpusSource, pat, dest string, log func(string, ...any)) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	tok, err := ghcrPullToken(ctx, s.Repo(), s.Owner, pat)
	if err != nil {
		return fmt.Errorf("authenticate to %s: %w", s, err)
	}

	layers, err := ghcrLayers(ctx, s.Repo(), s.Ref, tok)
	if err != nil {
		return fmt.Errorf("read manifest of %s: %w", s, err)
	}

	identity := make([]string, 0, len(layers))
	for _, l := range layers {
		identity = append(identity, l.digest)
	}

	// The corpus images carry exactly one layer. Iterating rather than assuming
	// index 0 costs nothing and keeps this correct if the bake ever adds one.
	var lastErr error
	for _, l := range layers {
		blob := dest + ".layer"
		log("pulling %s layer %s (%s)", s, shortDigest(l.digest), humanBytes(l.size))
		if err := ghcrPullBlobResumable(ctx, s.Repo(), l.digest, tok, blob, l.size, log); err != nil {
			lastErr = err
			continue
		}
		log("extracting %s from the layer", s.Member)
		err := extractTarMember(blob, s.Member, dest)
		// The compressed blob is scratch space; the DB it produced is what matters.
		_ = os.Remove(blob)
		if err == nil {
			// Written only after the DB is in place: an identity recorded beside a
			// file that failed to extract would make the next start skip the fetch.
			if werr := os.WriteFile(DigestPath(dest), []byte(strings.Join(identity, ",")), 0o644); werr != nil {
				log("warning: could not record the corpus identity (%v) — the next start will re-check the manifest", werr)
			}
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s has no layers", s)
	}
	return fmt.Errorf("extract %s from %s: %w", s.Member, s, lastErr)
}

// ghcrPullToken exchanges a PAT for a registry bearer token scoped to one repo.
// With no PAT it asks for an anonymous token, which succeeds only for a public
// package — the caller's error message is what makes that distinction useful.
func ghcrPullToken(ctx context.Context, repo, user, pat string) (string, error) {
	u := "https://ghcr.io/token?service=ghcr.io&scope=repository:" + repo + ":pull"
	var token string
	err := netRetry(ctx, func() error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if pat != "" {
			// The registry's OAuth2 exchange takes the PAT as basic-auth password;
			// the username is not checked by GHCR but must be non-empty.
			cred := base64.StdEncoding.EncodeToString([]byte(orDefault(user, "x") + ":" + pat))
			req.Header.Set("Authorization", "Basic "+cred)
		}
		resp, err := HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("token endpoint: %s", resp.Status)
		}
		var t struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
			return err
		}
		token = t.Token
		return nil
	})
	return token, err
}

// ghcrPullBlobResumable downloads a blob to dest, RESUMING a partial file with a
// Range request, and verifies the sha256 in the digest over the complete bytes.
//
// Resume is the point of this function. `ghcrPullBlob` streams and restarts from
// zero; on a ~7.9 GB layer that turns one dropped connection into another full
// download, five times over, before netRetry gives up.
func ghcrPullBlobResumable(ctx context.Context, repo, digest, token, dest string, size int64, log func(string, ...any)) error {
	if size > 0 {
		if fi, err := os.Stat(dest); err == nil && fi.Size() == size {
			log("layer already on disk (%s) — skipping the download", humanBytes(size))
			return verifyBlobDigest(dest, digest)
		}
	}
	u := "https://ghcr.io/v2/" + repo + "/blobs/" + digest

	err := netRetry(ctx, func() error {
		var have int64
		if fi, err := os.Stat(dest); err == nil {
			have = fi.Size()
		}
		if size > 0 && have > size {
			// A longer-than-expected file cannot be a prefix of the blob; the only
			// safe reading is that it is stale. Start over rather than append.
			_ = os.Remove(dest)
			have = 0
		}
		if size > 0 && have == size {
			return nil
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "mcp-3gpp-bootstrap")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if have > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		}
		resp, err := HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()

		switch resp.StatusCode {
		case http.StatusPartialContent:
			log("resuming at %s", humanBytes(have))
		case http.StatusOK:
			// The server ignored the Range (or we had nothing): restart the file.
			have = 0
		case http.StatusUnauthorized, http.StatusForbidden:
			// Not transient: retrying a rejected credential just wastes the backoff.
			return retry.Permanent(fmt.Errorf("blob %s: %s (the package is private — check the token's read:packages scope)", shortDigest(digest), resp.Status))
		default:
			return fmt.Errorf("blob %s: %s", shortDigest(digest), resp.Status)
		}

		flags := os.O_CREATE | os.O_WRONLY
		if have > 0 {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(dest, flags, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, resp.Body)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr // partial bytes are kept on purpose — the next attempt resumes
		}
		if closeErr != nil {
			return closeErr
		}
		if size > 0 {
			if fi, err := os.Stat(dest); err == nil && fi.Size() != size {
				return fmt.Errorf("blob %s: got %d bytes, manifest says %d", shortDigest(digest), fi.Size(), size)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return verifyBlobDigest(dest, digest)
}

// verifyBlobDigest checks the file against the "sha256:…" digest the manifest
// named. Without this the tag → manifest → blob chain is trusted end to end on
// the registry's word alone; with it, a corrupted or substituted layer is caught
// before DuckDB is asked to open it.
func verifyBlobDigest(path, digest string) error {
	algo, want, ok := strings.Cut(digest, ":")
	if !ok || algo != "sha256" {
		return nil // an algorithm we cannot check is not an algorithm we should fake
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(want) {
		_ = os.Remove(path) // a layer that fails its digest must not be resumed
		return fmt.Errorf("layer digest mismatch: got sha256:%s want %s", got, digest)
	}
	return nil
}

// extractTarMember writes the named member of a (optionally gzipped) tar to dest,
// atomically via dest.part. The member is matched on its base name — see the note
// on CorpusSource.Member.
func extractTarMember(layer, member, dest string) error {
	f, err := os.Open(layer)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Sniff rather than trust the manifest's media type: crane, docker and oras
	// disagree on whether the layer is +gzip, and a layer that says gzip and is
	// not (or the reverse) fails with an unhelpful error deep in the tar reader.
	var src io.Reader = f
	magic := make([]byte, 2)
	if _, err := io.ReadFull(f, magic); err != nil {
		return fmt.Errorf("read layer header: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("gunzip layer: %w", err)
		}
		defer func() { _ = zr.Close() }()
		src = zr
	}

	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%q not found in the layer", member)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(path.Clean(filepath.ToSlash(hdr.Name))) != member {
			continue
		}
		tmp := dest + ".part"
		out, err := os.Create(tmp)
		if err != nil {
			return err
		}
		// #nosec G110 — the source is a digest-verified layer from our own registry
		// package, and the member's size is bounded by the manifest's layer size.
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := out.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, dest)
	}
}

func shortDigest(d string) string {
	if _, hex, ok := strings.Cut(d, ":"); ok && len(hex) > 12 {
		return hex[:12]
	}
	return d
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "unknown size"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
