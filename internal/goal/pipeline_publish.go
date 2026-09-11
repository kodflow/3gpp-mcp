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
	"regexp"
	"slices"
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

// defaultImageTag is what the pipeline publishes when IMAGE_TAG says nothing. The
// resolved value folds into the fingerprint below, so retagging is treated as
// what it is — a different artefact that must be published.
//
// It is the one image default kept as a Go copy, because runPublish passes it as
// --tag and the script's own `${IMAGE_TAG:-…}` never applies under the pipeline.
// It must still equal that default, or `make image` and `make publish` push to
// different places: TestAnUnsetKnobAndItsDefaultAreOneImage holds the two
// together. (This comment used to say it matched "the Makefile's own default";
// the Makefile has none.)
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
		// BOTH halves, and the proof that they serve — through ONE edge.
		//
		// smoke starts the real server over both stores, and it stands on validate
		// AND validate-etsi, each of which stands on its own arm's index. So smoke
		// is ordered after BOTH freezes, and so is this step. It used to add
		// index-etsi here, from the time smoke depended on the 3GPP gate alone and
		// nothing else ordered a publish after the ETSI freeze (an image carrying an
		// ETSI HNSW still "building" is one serve refuses). validate-etsi closed that
		// gap one level down; naming one arm's index here and not the other's was
		// the last place the product step still told the arms apart.
		Deps: []string{"smoke"},
		Impl: append([]string{
			buildImageScript,
			"scripts/local/imgtar",
			"scripts/local/zigcc",
			"docker-entrypoint.sh",
			// EVERY SCRIPT build-image.sh READS, NOT THE TWO THAT WERE REMEMBERED.
			//
			// Review of #324 found the pin table missing: build-image.sh now reads
			// the ORT sha256 out of fetch-model.sh, so correcting a checksum without
			// changing the version moved nothing here and a published image kept
			// the runtime the old pin let through. Checking the rest of the script
			// found four more it reads and this step did not declare: the contract
			// it gates the corpus with, the loader check, the toolchain fetch that
			// decides which libstdc++ the binary links, and the environment it
			// builds under. TestPublishDeclaresEveryScriptTheImageBuildReads holds
			// the list to the script instead of to this comment.
			"scripts/fetch-model.sh",
			"scripts/data-contract.sh",
			"scripts/local/elfneeded",
			"scripts/local/fetch-linux-toolchain.sh",
			"scripts/local/toolchain-env.sh",
			// The image's server binary is compiled INSIDE this step, by zig, from
			// the module graph — so a dependency bump (DuckDB above all) changes
			// what ships without touching a line of the packages named below.
			"go.mod", "go.sum",
			// SO IS THE QUERY EMBEDDER, and nothing declared it.
			//
			// build-image.sh compiles rust/embed-core --features ort for the Linux
			// target and ships it as /usr/local/lib/libembed_core.so, the cdylib the
			// image's server calls through embed_ffi for every semantic query. This
			// step named none of it. The steps that did cover the crate are all Tools
			// — build-rust hashes the whole of rust/, build-serve and build-sparse
			// the crate — and a dirty Tool invalidates no consumer. So a fix to the
			// embedder, or an ort bump in its Cargo.toml, planned publish as
			// "fingerprint unchanged" and the registry kept the previous cdylib as
			// current.
			//
			// The LOCKFILE is the crate's own, not rust/Cargo.lock: rust/Cargo.toml
			// excludes embed-core from the workspace, so its own Cargo.lock is what
			// decides the ort, tokenizers and ndarray that ship. build-image.sh now
			// builds it --locked, so the file hashed here is the file cargo obeyed.
			// The crate has no build.rs.
			// TestPublishDeclaresEveryCrateTheImageBuildCompiles reads every
			// --manifest-path the script passes and holds this list to it.
			"rust/embed-core/src",
			"rust/embed-core/Cargo.toml",
			"rust/embed-core/Cargo.lock",
		}, append(serverImplPackages(), imageGuardPackages()...)...),
		// The shipped binary is `go build`, which does not compile _test.go. Editing
		// a server test must not re-push an image, for the same reason it must not
		// relink eight binaries.
		ExcludeTests: true,
		Heavy:        true,
		Inputs:       publishInputs,
		// EVERY OVERRIDE THE SCRIPT HONOURS THAT CHANGES THE IMAGE, NOT ONLY THE TAG.
		//
		// This map held image_tag alone, while build-image.sh also reads
		// IMAGE_BASE, ZIG_TARGET, ORT_VERSION, EMBED_FLOOR and DATA_CONTRACT from
		// the environment it inherits — runPublish passes it the tag and nothing
		// else. Exporting any one of them left this step "fingerprint unchanged,
		// outputs present and valid", and the image built from the previous base,
		// runtime or contract stayed on the registry as the current one. See
		// imageKnobs for why each is a determinant, and publish_knobs_test.go for
		// the test that reads the script so the next one cannot be missed.
		Extra: publishExtra,
		Outputs: func(c *Ctx) []string {
			return []string{c.statePath("published.json")}
		},
		Validate: validatePublished,
		Run:      runPublish,
	}
}

// buildImageScript is the script runPublish drives and the file most image
// defaults are read out of.
const buildImageScript = "scripts/local/build-image.sh"

// imageKnob is one environment variable build-image.sh reads that changes the
// image it produces.
type imageKnob struct {
	Env string // what the operator exports
	Key string // the determinant it becomes in publish's record
	// DefaultIn is the script whose `${Env:-default}` decides what an UNSET Env
	// means — the file the default is read out of, so there is never a copy of it
	// here to drift. It is build-image.sh for most knobs and deliberately not for
	// two, because build-image.sh does not decide those defaults itself.
	DefaultIn string
}

// imageKnobs are folded into publish's fingerprint at their EFFECTIVE value: what
// the script will actually use, its own default when the operator set nothing. So
// leaving a knob unset and setting it to its default are one artefact and
// fingerprint the same, and neither replays a 40 GB publish.
//
// WHY EACH ONE CHANGES THE IMAGE:
//
//	IMAGE_BASE     the image's first layers, the /etc/passwd and /etc/group the
//	               script derives from it and re-ships (build-image.sh:176-188),
//	               and the libraries the loader check resolves against (:430).
//	ZIG_TARGET     the glibc floor the server binary and the embed-core cdylib are
//	               linked against — exported for scripts/local/zigcc (:105).
//	ORT_VERSION    the ONNX Runtime layer 30 carries, DOWNLOADED at build time
//	               (:214-220). The ORT publishInputs does fingerprint is
//	               data/models/onnxruntime, which on the machine that publishes is
//	               lib/onnxruntime.dll — the Windows runtime, as the 2026-09-10
//	               record in .local/state/steps/publish.json shows — so the version
//	               that decides the layer was in neither Impl nor Extra. Its
//	               default lives in scripts/fetch-model.sh, which build-image.sh
//	               reads it out of by design ("sourcing the number rather than
//	               copying it"), and which is not in Impl either.
//	DATA_CONTRACT  the contract the corpus must pass before the script bakes it at
//	               all. It changes no byte of an image that passes, and that is
//	               exactly why it belongs here: what this step records is not
//	               "these bytes were pushed" but "this corpus met this contract and
//	               was pushed". DATA_CONTRACT=dense checks neither the sparse layer
//	               nor the ETSI half, so an image published under it must replay
//	               when the gate is restored, not stand on the registry as current
//	               under a contract it never met.
//
// EMBED_FLOOR, the contract's other half, USED TO BE A KNOB HERE and is not any
// more: runPublish passes the script --embed-floor, the floor validate applied, so
// the environment's value never reaches the gate under goal. It is still folded in
// — as embed_floor, at that applied value — and an EMBED_FLOOR that disagrees
// refuses the plan. See embed_floor.go.
//
// DATA_CONTRACT is read by build-image.sh only to label its log. The value that
// decides the gate is INHERITED by scripts/data-contract.sh, which applies its own
// default — hence DefaultIn, and hence a knob whose script-side reads are all
// `${DATA_CONTRACT:-}`.
//
// What build-image.sh reads and is NOT here — the push retry count, PATH — is in
// notAnImageKnob in publish_knobs_test.go, each with its reason, and that test
// fails on any read that is in neither place.
var imageKnobs = []imageKnob{
	{Env: "IMAGE_BASE", Key: "image_base", DefaultIn: buildImageScript},
	{Env: "ZIG_TARGET", Key: "zig_target", DefaultIn: buildImageScript},
	{Env: "ORT_VERSION", Key: "ort_version", DefaultIn: "scripts/fetch-model.sh"},
	{Env: "DATA_CONTRACT", Key: "data_contract", DefaultIn: "scripts/data-contract.sh"},
}

// publishExtra is publish's Extra: the tag, every imageKnob at the value the script
// will use, the embed floor runPublish passes it (embed_floor.go), and the version
// of every tool that writes the image (image_toolchain.go).
func publishExtra(c *Ctx) (map[string]string, error) {
	m := map[string]string{"image_tag": imageTag()}
	for _, k := range imageKnobs {
		v, err := k.effective(c.Root)
		if err != nil {
			return nil, err
		}
		m[k.Key] = v
	}
	floor, err := imageEmbedFloor(c)
	if err != nil {
		return nil, err
	}
	m["embed_floor"] = floor
	if err := addImageToolchain(c, m); err != nil {
		return nil, err
	}
	return m, nil
}

// effective is `${Env:-default}` evaluated the way bash evaluates it.
//
// Empty means unset, because that is what `:-` means. The value is NOT trimmed,
// unlike imageTag: the script passes it on verbatim — to crane, zig, curl and
// data-contract.sh — so a value that differs only in whitespace is a different
// argument, and a fingerprint that trimmed it would record something the build
// never used. The default is read
// only when it is needed, as bash does — a knob the operator set does not depend
// on a file that is not consulted.
func (k imageKnob) effective(root string) (string, error) {
	if v := os.Getenv(k.Env); v != "" {
		return v, nil
	}
	return shellDefault(root, k.DefaultIn, k.Env)
}

// shellDefault is the literal default the script at rel gives name in its
// `${name:-default}` expansions.
//
// READ OUT OF THE SCRIPT, NOT COPIED INTO GO. A second copy of a default is a
// second default, and this repository has already shipped one: build-image.sh
// labelled its gate `${DATA_CONTRACT:-dense}` after scripts/data-contract.sh's
// default had become dense+sparse+etsi, so all seven publishes from 2026-09-07 to
// 2026-09-10 logged "corpus contract (dense)" above --require-sparse and
// --require-etsi (.local/logs/*-publish.log). A Go constant would have drifted
// the same way and with worse consequences: the fingerprint would record the stale
// copy while the script built from the real one.
//
// Strict on purpose, because every way this can be wrong is silent otherwise. A
// default that is computed rather than written (a $, a backtick, a quote) is not
// a literal this can read; two different literals for one name mean the answer
// depends on which line runs; and no literal at all means this is looking in the
// wrong file. Each is an error, so the plan stops instead of fingerprinting a
// guess. `${name:-}` is not a default — it is how a `set -u` script reads a
// variable that may be unset — and is skipped.
func shellDefault(root, rel, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("cannot read the %s default out of %s: %w", name, rel, err)
	}
	re := regexp.MustCompile(`\$\{` + regexp.QuoteMeta(name) + `:-([^}]*)\}`)
	var found []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			d := m[1]
			if d == "" || strings.ContainsAny(d, "$`\"'\\") {
				continue
			}
			if !slices.Contains(found, d) {
				found = append(found, d)
			}
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s gives %s no literal `${%s:-default}`, so what an unset %s means "+
			"cannot be read — and cannot be fingerprinted", rel, name, name, name)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%s gives %s %d different defaults (%s): which one applies depends on "+
			"the line that runs", rel, name, len(found), strings.Join(found, ", "))
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
// WHY NOT a narrower list. A fix in internal/store or internal/rerank changes
// what a consumer runs, and under-declaring it would publish an image whose binary
// does not match the tree that claims to have produced it.
//
// SMOKE DECLARES THIS SAME LIST, by calling this function, and that is the gate's
// half of the same argument. It used to name five of these packages, on the
// reasoning that smoke only has to START the server while this step ships it. But
// smoke runs every probe through the server it starts, and build-go — which
// rebuilds that server — is a Tool dep that invalidates no consumer. So an edit in
// one of the other eight (internal/subject, behind resolve_term, among them)
// skipped the gate that would have caught it, and this step, whose fingerprint did
// move, published it recorded as gated. One function, two callers:
// TestSmokeJudgesEveryPackagePublishShips fails the day they part.
//
// The list is written out rather than computed so that reading this step tells
// you what defines it. TestPublishCoversEveryPackageTheServerLinks holds it to
// `go list -deps ./cmd/server` in both directions, so a new import fails the build
// instead of silently escaping the fingerprint, and a stale entry fails it too.
//
// THE CLOSURE OF THE SERVER THE IMAGE SHIPS, NOT OF THE ONE `go build` MAKES HERE.
// build-image.sh compiles cmd/server `-tags "onnx,embed_ffi"` for GOOS=linux, and
// under `onnx` internal/rerank/rerank_onnx.go imports internal/onnxrt: the
// process-wide ONNX Runtime initialisation the image's reranker runs through, and
// LibPath, the library path it loads. The test asked for the untagged, host graph
// until 2026-09-11, which does not contain that package, so it was missing here
// and the test agreed. An edit there could keep the reranker from starting in the
// image while publish reported the previous image as current. The test now reads
// the tags and the target out of the script.
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
		"internal/onnxrt", // linked only under `onnx`, which is how the image builds it
		"internal/registry",
		"internal/releaseview",
		"internal/rerank",
		"internal/retry",
		"internal/search",
		"internal/store",
		"internal/subject",
	}
}

// imageGuardPackages are the commands build-image.sh runs on this machine to decide
// whether the image may be published at all. Its identity guards (step 6) take the
// dense and sparse identities the baked registry resolves to from cmd/embedid, and
// the ones the corpus carries from cmd/dbcount, and refuse the push when they
// differ or when the corpus states none.
//
// NOT DECLARED UNTIL 2026-09-11. No test read the script's Go commands: the
// closure test asked `go list` about ./cmd/server alone, and these two run as
// `go run` inside $( ) substitutions, a shape the script's only command reader
// (the cargo one) could not see either. Reading every `go build` and `go run` the
// script performs, under its own tags, found them the first time it ran. An edit
// to either (the field dbcount prints, how embedid resolves the sparse head)
// changed what this step lets through while its fingerprint stood still. The
// packages they link (internal/embed, internal/model, internal/store) are server
// packages and were already here.
//
// They ship nothing, which is why they are a list of their own:
// TestSmokeJudgesEveryPackagePublishShips requires smoke to fingerprint what the
// image carries, and smoke runs neither of these.
// TestPublishCoversEveryPackageTheImageBuildCompiles holds the list to the `go
// build` and `go run` commands the script performs.
func imageGuardPackages() []string {
	return []string{"cmd/dbcount", "cmd/embedid"}
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
	// THE SCRIPT IS HELD TO THE FLOOR VALIDATE APPLIED, passed as a flag so that its
	// own ${EMBED_FLOOR:-…} never decides under the pipeline (embed_floor.go).
	floor, err := imageEmbedFloor(c)
	if err != nil {
		return err
	}
	// SKIP THE SCRIPT'S RE-RUN OF THE CONTRACT only when `validate` certified these
	// exact bytes under the flags and floor the script will use. See
	// contract_certificate.go; every doubt leaves the re-run in place.
	env := contractEnv(c, floor)
	if err := c.Run(Cmd{
		Name: "bash",
		Args: []string{buildImageScript, "--tag", tag, "--embed-floor", floor},
		Env:  env,
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
