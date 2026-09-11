package bootstrap

// corpus_ref.go decides WHICH published corpus a caller pulls, and how that
// choice is spelled.
//
// A TAG IS A POINTER THAT SOMEONE ELSE MOVES. Both corpus packages were only ever
// pulled by tag, and `latest` by default, so two fresh clones of the same commit
// seeded whatever `latest` happened to name on the day — and nothing recorded
// which one that was. Measured 2026-09-11: `3gpp-corpus:latest` and
// `etsi-corpus:latest` were last moved on 2026-08-30, WITHOUT a dated tag beside
// them, so the snapshot a clone gets could not even be named after the fact.
//
// A DIGEST IS THE THING ITSELF. `sha256:<hex>` names one manifest, and the
// manifest names its layers by digest, so a pull by digest is reproducible byte
// for byte — provided the client checks what the registry sends against the
// digest it asked for, which ghcrManifest does. Without that check a digest pin
// is a request, not a guarantee.
//
// One environment variable could not carry a digest for BOTH packages: a digest
// names one manifest of one package. So each package has its own reference
// variable, and the shared MCP3GPP_CORPUS_TAG stays what its name says — a tag
// applied to both — and refuses a digest instead of silently pinning one package
// with the other's identity.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// The two corpus packages this project publishes.
const (
	Image3GPP = "3gpp-corpus"
	ImageETSI = "etsi-corpus"
)

// The environment a deployment (or the pipeline's operator) repoints the corpus
// with, without a rebuild.
const (
	// EnvGHCROwner is the GHCR account holding the packages (a fork).
	EnvGHCROwner = "MCP3GPP_GHCR_OWNER"
	// EnvCorpusTag is ONE TAG applied to both packages. A digest is refused here.
	EnvCorpusTag = "MCP3GPP_CORPUS_TAG"
	// EnvCorpusRef3GPP / EnvCorpusRefETSI choose the reference of ONE package: a
	// tag, or a digest (`sha256:<hex>`, `@sha256:<hex>` accepted).
	EnvCorpusRef3GPP = "MCP3GPP_CORPUS_REF_3GPP"
	EnvCorpusRefETSI = "MCP3GPP_CORPUS_REF_ETSI"
)

// refEnvOf maps a package to its own reference variable.
var refEnvOf = map[string]string{
	Image3GPP: EnvCorpusRef3GPP,
	ImageETSI: EnvCorpusRefETSI,
}

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// The OCI distribution spec's tag grammar. A tag can never contain ':' or
	// '@', which is what makes a digest and a tag impossible to confuse.
	tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// IsDigest reports whether ref names a manifest by content ("sha256:" + 64 hex).
func IsDigest(ref string) bool { return digestRe.MatchString(ref) }

// NormalizeRef accepts the two spellings of a digest reference — `sha256:…` and
// the `@sha256:…` suffix people copy from `crane digest --full-ref` — and returns
// the form the registry API wants in /manifests/<ref>.
func NormalizeRef(ref string) string {
	return strings.TrimPrefix(strings.TrimSpace(ref), "@")
}

// ValidateRef refuses anything that is neither a tag nor a digest. A malformed
// digest is not a tag either: "sha256:abc" would otherwise travel to the
// registry and come back as a 404 that names nothing the operator typed.
func ValidateRef(ref string) error {
	r := NormalizeRef(ref)
	switch {
	case IsDigest(r), tagRe.MatchString(r):
		return nil
	case strings.HasPrefix(r, "sha256:"):
		return fmt.Errorf("%q is not a digest: want sha256: followed by 64 lowercase hex characters", ref)
	default:
		return fmt.Errorf("%q is neither a tag nor a sha256 digest", ref)
	}
}

// RefOverride returns the reference the ENVIRONMENT chose for one package, and
// the variable that chose it — or "" when the environment chose nothing, which
// leaves the decision to the caller (the pipeline's pin file, the server's
// rolling `latest`).
//
// The package's own variable wins over the shared tag: it is the more specific
// statement, and the only one that can carry a digest.
func RefOverride(image string) (ref, origin string, err error) {
	if env, ok := refEnvOf[image]; ok {
		if v := NormalizeRef(os.Getenv(env)); v != "" {
			if err := ValidateRef(v); err != nil {
				return "", "", fmt.Errorf("%s: %w", env, err)
			}
			return v, "$" + env, nil
		}
	}
	v := NormalizeRef(os.Getenv(EnvCorpusTag))
	if v == "" {
		return "", "", nil
	}
	if IsDigest(v) || strings.HasPrefix(v, "sha256:") {
		return "", "", fmt.Errorf("%s=%s is a digest, but that variable applies to BOTH %s and %s, "+
			"and a digest names one manifest of ONE package — set %s and/or %s instead",
			EnvCorpusTag, v, Image3GPP, ImageETSI, EnvCorpusRef3GPP, EnvCorpusRefETSI)
	}
	if err := ValidateRef(v); err != nil {
		return "", "", fmt.Errorf("%s: %w", EnvCorpusTag, err)
	}
	return v, "$" + EnvCorpusTag, nil
}

// FullRef renders "ghcr.io/<owner>/<image>@<digest>", the one spelling that names
// a pulled snapshot unambiguously — what the pipeline records and what the pin
// file holds.
func FullRef(s CorpusSource, digest string) string {
	return "ghcr.io/" + s.Repo() + "@" + digest
}

// ParseFullRef is the inverse of FullRef, for the pin file: it accepts only a
// digest reference on ghcr.io, because a pin that names a tag pins nothing.
func ParseFullRef(ref string) (owner, image, digest string, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(ref), "ghcr.io/")
	if !ok {
		return "", "", "", fmt.Errorf("%q is not a ghcr.io reference", ref)
	}
	repo, dg, ok := strings.Cut(rest, "@")
	if !ok || !IsDigest(dg) {
		return "", "", "", fmt.Errorf("%q does not end in @sha256:<64 hex> — a pin must name a digest, a tag pins nothing", ref)
	}
	owner, image, ok = strings.Cut(repo, "/")
	if !ok || owner == "" || image == "" || strings.Contains(image, "/") {
		return "", "", "", fmt.Errorf("%q is not ghcr.io/<owner>/<package>@<digest>", ref)
	}
	return owner, image, dg, nil
}
