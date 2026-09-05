package goal

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// This file makes the container image a STEP of the pipeline instead of a second
// entry point that nothing tracked.
//
// # WHAT WAS WRONG
//
// `make publish` ran scripts/local/build-image.sh directly. The script itself is
// careful: it cross-compiles the Linux artefacts, composes the layers, and since
// 2026-09-03 it reads its own published config back and refuses to call a Windows
// path "published". What it could not do is know WHEN it should run. It had no
// fingerprint, no declared inputs and no record, so nothing in this repository
// could answer the one question a consumer's experience depends on — "is the
// image on ghcr.io the corpus this machine just built?" — and the only instrument
// left was somebody remembering.
//
// That instrument failed twice in two days:
//
//   - 2026-09-03. An image was published. Every data gate was green, `prove`
//     drove all seven retrieval arms over real JSON-RPC, and the runbook recorded
//     it as done. The artefact could not start at all: MSYS had rewritten the
//     Linux config into Windows paths. build-image.sh now reads the config back,
//     which fixes THAT bug — but only for a run somebody decided to start.
//   - 2026-09-04. `enrich` and `index` rewrote the corpus AFTER the image had
//     been composed. Nothing was broken and nothing complained; the published
//     image was simply one build behind. What caught it was a human comparing
//     two timestamps by hand.
//
// Both are the same structural hole: the artefact consumers pull was the only
// output of this repository with no determinants.
//
// # WHAT THIS DOES INSTEAD
//
// The image gets inputs, an implementation set and a record like every other
// step, so the planner answers the question before anything runs:
//
//	[SKIP] publish   fingerprint unchanged, outputs present and valid
//	[RUN ] publish   inputs changed: data/3gpp.duckdb
//
// # WHY IT IS IN THE DEFAULT GRAPH
//
// A publish step that has to be asked for is the same instrument as the memory it
// replaces. `make build` has to be allowed to mean "everything this machine ships
// is current", and to a consumer the image is the only thing that ships.
//
// It is not expensive to leave in. Publishing is skipped outright when nothing
// moved, and when something did, the registry dedupes by digest and imgtar stamps
// a fixed timestamp on every member — so a code-only change re-uploads the 70 MB
// binary layer rather than the 40 GB corpus.

// defaultImageTag is what the pipeline publishes when IMAGE_TAG says nothing. It
// matches the Makefile's own default; both are read by build-image.sh, and the
// resolved value folds into the fingerprint below, so retagging is treated as
// what it is — a different artefact that must be published.
const defaultImageTag = "ghcr.io/kodflow/3gpp-mcp:latest"

// rerankModelName is the cross-encoder the image carries. sparseModelName (the
// learned-lexical encoder) is declared next to the sparse step.
//
// These two names and "onnxruntime" are also written in build-image.sh, in the
// `layer 30-ort` and `layer 40-models` lines. TestPublishFingerprintsEveryModelTheImageCarries
// pins the two lists together by reading the script, so a model added to the
// image cannot silently escape this step's fingerprint.
const rerankModelName = "bge-reranker-v2-m3"

// imageTag resolves the tag to publish. IMAGE_TAG is the same override
// build-image.sh honours, so the step cannot publish somewhere other than where
// its own fingerprint says it did.
func imageTag() string {
	if t := strings.TrimSpace(os.Getenv("IMAGE_TAG")); t != "" {
		return t
	}
	return defaultImageTag
}

// registryHost is the part of a tag that carries the credential.
func registryHost(tag string) string {
	h, _, ok := strings.Cut(tag, "/")
	if !ok {
		return ""
	}
	return h
}

// publishedRecord is what the step leaves behind: the tag it pushed, the digest
// the registry answered with, and when. It is the file `make plan` compares, and
// the only place in this repository that states which image belongs to which
// corpus.
type publishedRecord struct {
	Tag         string `json:"tag"`
	Digest      string `json:"digest"`
	PublishedAt string `json:"published_at"`
	// Corpus sizes are diagnostic, not determinant — the fingerprint already
	// covers the files. They are here because the failure being guarded against
	// was noticed by comparing a corpus against an image, and the record should
	// carry enough to do that by eye.
	Corpus3GPPBytes int64 `json:"corpus_3gpp_bytes,omitempty"`
	CorpusETSIBytes int64 `json:"corpus_etsi_bytes,omitempty"`
}

// --------------------------------------------------------------------- publish

func stepPublish() *Step {
	return &Step{
		Name:    "publish",
		Version: 1,
		Doc:     "compose the OCI image from the finished corpus and push it to the registry",
		// BOTH halves, and the proof that they serve.
		//
		// smoke is the last gate on the 3GPP side and starts the real server over
		// stdio; index-etsi is the last WRITE to the ETSI half. Depending on smoke
		// alone would not be enough: smoke and index-etsi are siblings in the graph
		// (smoke depends on validate, index-etsi on compact), so nothing would order
		// a publish after the ETSI freeze, and the image could carry an ETSI corpus
		// whose HNSW is still "building" — which serve refuses.
		Deps: []string{"smoke", "index-etsi"},
		Impl: append([]string{
			"scripts/local/build-image.sh",
			"scripts/local/imgtar",
			"scripts/local/zigcc",
			"docker-entrypoint.sh",
		}, serverImplPackages()...),
		// The shipped binary is `go build`, which does not compile _test.go. Editing
		// a server test must not re-push an image, for the same reason it must not
		// relink eight binaries.
		ExcludeTests: true,
		Heavy:        true,
		Inputs:       publishInputs,
		Extra: func(c *Ctx) (map[string]string, error) {
			return map[string]string{"image_tag": imageTag()}, nil
		},
		Outputs: func(c *Ctx) []string {
			return []string{c.statePath("published.json")}
		},
		Validate: validatePublished,
		Run:      runPublish,
	}
}

// publishInputs enumerates the FILES the image carries, one by one.
//
// A directory cannot stand in for its contents here. inputsHash records a
// directory as the constant string "dir" (see fingerprint.go), so declaring
// data/models would have produced a fingerprint that is identical for a model
// that never changed and for one that changed and was not noticed — which is the
// failure mode this whole step exists to remove, reintroduced one level down.
func publishInputs(c *Ctx) ([]string, error) {
	in := []string{c.dataPath("3gpp.duckdb")}
	// The ETSI half travels in the same layer and is optional on a machine that
	// has not built it. An absent path fingerprints as "absent", which is honest
	// and changes the moment the file appears.
	if etsi := c.dataPath("etsi.duckdb"); fileNonEmpty(etsi) {
		in = append(in, etsi)
	}
	for _, d := range imageModelDirs() {
		files, err := filesUnder(c.dataPath("models", d))
		if err != nil {
			return nil, err
		}
		in = append(in, files...)
	}
	return in, nil
}

// imageModelDirs are the model directories build-image.sh packs into layers 30
// and 40. Kept in one place so the test that reads the script has something to
// compare against.
func imageModelDirs() []string {
	return []string{"onnxruntime", sparseModelName, rerankModelName}
}

// serverImplPackages is the transitive package closure of cmd/server inside this
// module: everything the binary the image ships is linked from.
//
// WHY NOT JUST "cmd" AND "internal", the way build-go declares itself. Because
// that set contains internal/goal — this file — and editing the orchestrator
// would republish the image. The push would transfer almost nothing (imgtar
// stamps fixed timestamps, so an unchanged layer keeps its digest and the
// registry skips it), but crane still reads every layer twice to compute those
// digests, which is ~8 minutes of streaming 40 GB off disk to discover that
// nothing moved.
//
// WHY NOT the three packages smoke names. Because smoke only has to START the
// server, while this ships it: a fix in internal/store or internal/rerank changes
// what a consumer runs, and under-declaring it would publish an image whose
// binary does not match the tree that claims to have produced it.
//
// The list is written out rather than computed so that reading this step tells
// you what defines it. TestPublishCoversEveryPackageTheServerLinks holds it to
// `go list -deps ./cmd/server`, so a new import fails the build instead of
// silently escaping the fingerprint.
//
// internal/subject covers its own subpackages: Impl walks directories.
//
// internal/embed/models.yaml is deliberately NOT here. The image does not ship
// it — build-image.sh writes its own models.yaml into the rootfs (a heredoc, so
// it is already covered by the script's own hash) and points
// EMBED_MODELS_CONFIG at that copy. The compiled-in default never applies inside
// the container.
func serverImplPackages() []string {
	return []string{
		"cmd/server",
		"internal/bootstrap",
		"internal/embed",
		"internal/mcp",
		"internal/metrics",
		"internal/model",
		"internal/registry",
		"internal/releaseview",
		"internal/rerank",
		"internal/retry",
		"internal/search",
		"internal/store",
		"internal/subject",
	}
}

// filesUnder lists every regular file below dir.
//
// A missing directory is not an error: the models are absent on a machine that
// has not fetched them, and the step must still be plannable there. It returns
// nothing in that case, so the fingerprint gains those keys — and changes — the
// moment the files arrive.
func filesUnder(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// validatePublished is deliberately LOCAL and cheap.
//
// The tempting version asks the registry whether the tag still resolves to the
// recorded digest. It is not worth what it costs: Validate runs on EVERY plan,
// including for steps that would be skipped, so `make plan` would need a network
// round trip and a credential to answer a question about local state. What the
// record claims is confirmed at the moment it is written instead — runPublish
// reads the digest back from the registry after build-image.sh has re-read and
// checked the config it just pushed.
func validatePublished(c *Ctx) error {
	b, err := os.ReadFile(c.statePath("published.json"))
	if err != nil {
		return err
	}
	var p publishedRecord
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("published.json does not parse: %w", err)
	}
	if p.Tag == "" {
		return fmt.Errorf("published.json names no tag")
	}
	// "sha256:" plus 64 LOWERCASE hex digits, which is the only shape the OCI
	// spec lets a manifest digest take. A truncated, empty or garbled digest is
	// how a failed push would otherwise be recorded as a successful one — and
	// the record is what the planner reads to decide that runPublish can be
	// SKIPPED, so anything accepted here that no registry could ever serve
	// leaves the image unpublished with the pipeline calling it done.
	//
	// Counting the characters is not enough: it accepts 64 arbitrary bytes.
	// Decode them.
	hexDigits, ok := strings.CutPrefix(p.Digest, "sha256:")
	if !ok || len(hexDigits) != 64 {
		return fmt.Errorf("published.json carries %q, which is not a manifest digest", p.Digest)
	}
	if _, err := hex.DecodeString(hexDigits); err != nil {
		return fmt.Errorf("published.json carries %q, whose digest is not hex", p.Digest)
	}
	if strings.ToLower(hexDigits) != hexDigits {
		return fmt.Errorf("published.json carries %q, whose digest is not lower-case hex", p.Digest)
	}
	return nil
}

// runPublish builds the image and pushes it, then records what the registry
// actually answered.
func runPublish(c *Ctx) error {
	tag := imageTag()
	host := registryHost(tag)
	if host == "" {
		return fmt.Errorf("image tag %q names no registry", tag)
	}

	crane, err := craneBinary(c)
	if err != nil {
		c.Log.Printf("no crane on this machine — NOT publishing. The corpus is built and " +
			"proved; `make image-toolchain` then `make publish` will ship it from a machine that has one.")
		return fmt.Errorf("%w: %v", ErrDeclined, err)
	}

	// DECLINE rather than fail when this machine cannot push.
	//
	// A build machine without a registry credential has still done everything the
	// pipeline asked of it, and failing the goal there would make `make build`
	// unusable for anyone who only wants the corpus. A decline is recorded, shown
	// in the run report, and carries the previous provenance forward — so it says
	// "nothing was published" out loud instead of leaving a green run that quietly
	// shipped nothing.
	if err := craneHasCredential(c, crane, host); err != nil {
		c.Log.Printf("no %s credential in crane's keychain — NOT publishing. "+
			"Log in (crane auth login %s -u <user> -p <token>) and re-run; the corpus is "+
			"unaffected and the push resumes from the blobs already stored.", host, host)
		return fmt.Errorf("%w: no %s credential to push %s", ErrDeclined, host, tag)
	}

	c.Log.Printf("publishing %s — the corpus layer is ~40 GB, and only the blobs the "+
		"registry does not already hold are transferred", tag)
	if err := c.Run(Cmd{
		Name: "bash",
		Args: []string{"scripts/local/build-image.sh", "--tag", tag},
		Echo: true,
	}); err != nil {
		return err
	}

	// READ THE DIGEST BACK FROM THE REGISTRY. build-image.sh has already re-read
	// the config it pushed and refused to call a Windows path published; this asks
	// the remaining question — which manifest the tag now names — of the registry
	// rather than of the process that just wrote it.
	digest, err := craneDigest(c, crane, tag)
	if err != nil {
		return fmt.Errorf("the push reported success but %s cannot be resolved: %w", tag, err)
	}

	rec := publishedRecord{
		Tag:             tag,
		Digest:          digest,
		PublishedAt:     time.Now().UTC().Format(time.RFC3339),
		Corpus3GPPBytes: fileSize(c.dataPath("3gpp.duckdb")),
		CorpusETSIBytes: fileSize(c.dataPath("etsi.duckdb")),
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteAtomic(c.statePath("published.json"), append(b, '\n')); err != nil {
		return err
	}
	c.Log.Printf("published %s at %s", tag, digest)
	return nil
}

// craneBinary finds the crane this repository vendors, falling back to one on
// PATH. build-image.sh resolves it the same way and in the same order.
func craneBinary(c *Ctx) (string, error) {
	p := c.bin("crane")
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p, nil
	}
	if p, err := exec.LookPath("crane"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("crane is not at %s and not on PATH", c.bin("crane"))
}

// craneHasCredential asks crane's OWN keychain, because that is what the push
// will use. Reading ~/.docker/config.json here would be a second implementation
// of credential resolution, and two implementations of the same lookup
// eventually disagree — at which point this step declines on a machine that can
// publish, or attempts a 40 GB push on one that cannot.
//
// The output is discarded and never reaches the step log: `crane auth get`
// prints the secret in clear.
func craneHasCredential(c *Ctx, crane, host string) error {
	cmd := exec.CommandContext(c.Context, crane, "auth", "get", host)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// craneDigest resolves a tag to the manifest digest the registry serves.
func craneDigest(c *Ctx, crane, tag string) (string, error) {
	cmd := exec.CommandContext(c.Context, crane, "digest", tag)
	cmd.Dir = c.Root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// fileSize is 0 for anything that is not a readable regular file.
func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return 0
	}
	return st.Size()
}
