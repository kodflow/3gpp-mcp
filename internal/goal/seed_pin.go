package goal

// seed_pin.go decides which published snapshot an arm's `seed` step pulls, and
// makes what it pulled part of what the step publishes.
//
// WHY A PIN, AND WHY IN THE TREE. Both arms used to seed from `latest`, a tag the
// publisher moves: two fresh clones of one commit got whatever it named on the day,
// and the step's record could not say which. Measured 2026-09-11 on the registry,
// `3gpp-corpus:latest` and `etsi-corpus:latest` were last moved on 2026-08-30 with
// no dated tag beside them, so the snapshot a clone received could not even be
// named afterwards. The snapshot a build seeds from is now a DIGEST written in
// contracts/corpus-pin.txt, so it is fixed by the commit, reviewed like code, and
// bumped in one place by the one thing that creates a new snapshot:
// scripts/local/publish-corpus.sh, right after it pushes.
//
// WHY THE PIN IS NOT A DETERMINANT OF ANYTHING BUT `seed`. The digest enters the
// step's fingerprint through Extra — per arm, so bumping the ETSI pin leaves the
// 3GPP seed alone — and it reaches the dependants ONLY when a seed really
// happens: `seed` declines whenever a corpus is already on disk, and a decline
// carries the previous provenance forward (runner.go). So a pin bump on a machine
// that holds a corpus costs two declines of about a second each; discover,
// discover-etsi and everything behind them see the same dependency they saw
// before, and no corpus byte moves. On a machine that DOES seed, the manifest
// digest it actually pulled is recorded through Ctx.Produced, which is folded into
// the provenance — so a snapshot that differs from the last one seeded replays
// what stands on it, even under an operator override that names a moving tag.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// corpusPinFile is the versioned pin, relative to the repository root.
const corpusPinFile = "contracts/corpus-pin.txt"

// fetchCorpus is bootstrap.FetchCorpus, behind a variable so the seed step's
// wiring can be tested without a registry. Nothing in the shipped code rewrites it.
var fetchCorpus = bootstrap.FetchCorpus

// corpusPin is one line of the pin file.
type corpusPin struct {
	Owner, Image, Digest string
}

// readCorpusPins parses the pin file: one `ghcr.io/<owner>/<package>@sha256:<hex>`
// per line, blank lines and `#` comments ignored, each package at most once.
//
// Strict on purpose. The file is written by a script and read by a build; a line
// this parser could not read would otherwise leave that arm on no pin at all, and
// the failure would surface as a seed of the wrong corpus on some future fresh
// clone rather than here.
func readCorpusPins(path string) (map[string]corpusPin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	pins := map[string]corpusPin{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text()) // TrimSpace also drops a CRLF checkout's \r
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		owner, image, digest, err := bootstrap.ParseFullRef(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if _, dup := pins[image]; dup {
			return nil, fmt.Errorf("%s:%d: %s is pinned twice", path, n, image)
		}
		pins[image] = corpusPin{Owner: owner, Image: image, Digest: digest}
	}
	return pins, sc.Err()
}

// seedSource resolves the snapshot this arm seeds from WITHOUT touching the
// network, so it can be a determinant: `goal plan` must work offline and without
// a credential.
//
// In order: the operator's explicit choice (bootstrap.RefOverride — the package's
// own variable, then the shared tag), else the pin. The override is kept because
// it is how a staging snapshot is tried; it is recorded in the fingerprint as the
// reference it is, so following a moving tag is at least a visible decision.
func (t corpusTarget) seedSource(c *Ctx) (bootstrap.CorpusSource, string, error) {
	src := t.Snapshot()
	ref, origin, err := bootstrap.RefOverride(src.Image)
	if err != nil {
		return src, "", err
	}
	if ref != "" {
		src.Ref = ref
		return src, origin, nil
	}
	pins, err := readCorpusPins(filepath.Join(c.Root, corpusPinFile))
	if err != nil {
		return src, "", fmt.Errorf("read the corpus pin: %w", err)
	}
	pin, ok := pins[src.Image]
	if !ok {
		return src, "", fmt.Errorf("%s pins no snapshot for %s", corpusPinFile, src.Image)
	}
	// A digest is meaningful inside ONE repository. An owner chosen in the
	// environment that is not the one the pin names would send this digest to a
	// package that has never heard of it.
	if o := strings.TrimSpace(os.Getenv(bootstrap.EnvGHCROwner)); o != "" && o != pin.Owner {
		return src, "", fmt.Errorf("%s=%s, but %s pins ghcr.io/%s/%s — a digest belongs to one repository. "+
			"Choose that package's reference explicitly (%s), or publish its corpus with "+
			"scripts/local/publish-corpus.sh --owner %s, which pins it",
			bootstrap.EnvGHCROwner, o, corpusPinFile, pin.Owner, pin.Image, refEnvName(src.Image), o)
	}
	src.Owner, src.Ref = pin.Owner, pin.Digest
	return src, corpusPinFile, nil
}

func refEnvName(image string) string {
	if image == bootstrap.ImageETSI {
		return bootstrap.EnvCorpusRefETSI
	}
	return bootstrap.EnvCorpusRef3GPP
}

// seedExtra is the determinant the pin contributes to its arm's seed: the
// reference that seed WOULD pull, as configured — never the result of asking the
// registry, which would put a network round trip and a credential into every plan.
func (t corpusTarget) seedExtra(c *Ctx) (map[string]string, error) {
	src, _, err := t.seedSource(c)
	if err != nil {
		return nil, err
	}
	return map[string]string{"snapshot": src.String()}, nil
}
