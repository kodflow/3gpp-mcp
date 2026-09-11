package goal

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// repoRootForTest is the checkout, as seen from this package's directory. The
// tests below read the real build script and the real package graph, because the
// property they pin is agreement between this step and the rest of the tree.
func repoRootForTest() string { return filepath.Join("..", "..") }

func publishStep(t *testing.T) *Step {
	t.Helper()
	for _, s := range Pipeline() {
		if s.Name == "publish" {
			return s
		}
	}
	t.Fatal("the pipeline has no `publish` step: the image is an untracked side artefact again, " +
		"and nothing can say whether what consumers pull is the corpus this machine built")
	return nil
}

// TestThePublishIsAStepNotAnEntryPoint. The image spent the project's whole life
// outside the graph, and twice in two days it went out stale or dead while every
// local gate was green. A step without determinants would restore exactly that.
func TestThePublishIsAStepNotAnEntryPoint(t *testing.T) {
	s := publishStep(t)
	if s.Inputs == nil {
		t.Error("publish declares no Inputs: it can never notice a corpus that moved")
	}
	if s.Outputs == nil {
		t.Error("publish declares no Outputs: there is no record of what was published")
	}
	if s.Validate == nil {
		t.Error("publish has no Validate: a truncated record would be trusted because its fingerprint matched")
	}
	if s.Run == nil {
		t.Fatal("publish has no Run")
	}
	if !s.Heavy {
		t.Error("publish is not marked Heavy: it moves tens of gigabytes over the network")
	}
}

// TestPublishRunsAfterBothHalvesAreFrozen — an ETSI corpus whose HNSW is still
// "building" is one the server refuses to serve, so the publish must be ordered
// after that freeze.
//
// THIS TEST WAS RE-DERIVED ON 2026-09-07, at its own instruction. Its control
// used to be that index-etsi is NOT reachable from smoke: while that held,
// publish naming index-etsi directly was the only thing ordering the two, and the
// control kept that dependency load-bearing rather than decorative. It then
// fired, exactly as designed:
//
//	index-etsi is already reachable from smoke — this test no longer proves
//	anything; re-derive what orders the publish after the ETSI freeze
//
// What changed is that `validate` gained a dependency on index-etsi, because the
// strengthened contract asserts the ETSI HNSW is frozen and validate was running
// BEFORE the step that freezes it (build 24 failed on exactly that). smoke
// depends on validate, so the reachability the control forbade is now the very
// thing that guarantees the ordering — and it guarantees it better: the old chain
// merely ran after the freeze, this one runs after a gate that CHECKED it.
//
// So the assertion moves from "publish names index-etsi" to "publish cannot run
// before index-etsi", which is the property that was always meant. The control
// moves with it: the ordering must not rest on publish's own Deps alone, or
// removing the validate dependency would silently restore the build-24 failure
// with this test still green.
func TestPublishRunsAfterBothHalvesAreFrozen(t *testing.T) {
	steps := map[string]*Step{}
	for _, s := range Pipeline() {
		steps[s.Name] = s
	}

	reachFrom := func(start string, skipDirect string) map[string]bool {
		seen := map[string]bool{}
		var reach func(string)
		reach = func(n string) {
			if seen[n] {
				return
			}
			seen[n] = true
			s, ok := steps[n]
			if !ok {
				return
			}
			for _, d := range append(append([]string{}, s.Deps...), s.AnyDeps...) {
				if n == start && d == skipDirect {
					continue
				}
				reach(d)
			}
		}
		reach(start)
		return seen
	}

	if !slices.Contains(steps["publish"].Deps, "smoke") {
		t.Errorf("publish does not depend on smoke — it can ship a corpus nothing exercised (deps: %v)",
			steps["publish"].Deps)
	}
	for _, want := range []string{"index", "index-etsi", "validate", "validate-etsi"} {
		if !reachFrom("publish", "")[want] {
			t.Errorf("publish can run before %s — it would ship a half nothing finished", want)
		}
	}

	// THE CONTROL, re-derived. Ignore publish's own direct edge to index-etsi and
	// the ordering must still hold, through validate. If it does not, this test is
	// green only because of a dependency that the build-24 failure showed is not
	// where the guarantee belongs.
	if !reachFrom("publish", "index-etsi")["index-etsi"] {
		t.Error("the ordering rests only on publish's direct dependency: nothing between " +
			"index-etsi and publish requires the ETSI freeze, so validate could drift back " +
			"before it (build 24 failed exactly that way) with this test still passing")
	}
}

// TestPublishFingerprintsEveryModelTheImageCarries reads build-image.sh and holds
// imageModelDirs to what the script actually packs. A model added to layer 30 or
// 40 without being added here would travel in the image while being invisible to
// the step's fingerprint — the image would be a build behind and nothing would
// say so, which is the failure this step exists to end.
func TestPublishFingerprintsEveryModelTheImageCarries(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootForTest(), "scripts", "local", "build-image.sh"))
	if err != nil {
		t.Fatalf("cannot read build-image.sh: %v", err)
	}
	re := regexp.MustCompile(`data/mcp-3gpp/models/([A-Za-z0-9._-]+)`)
	declared := imageModelDirs()
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		name := m[1]
		// models.yaml is a FILE the script writes itself (a heredoc), so the
		// script's own hash already covers it.
		if name == "models.yaml" {
			continue
		}
		if !slices.Contains(declared, name) {
			t.Errorf("build-image.sh puts data/models/%s in the image, but publishInputs does not "+
				"fingerprint it — a change to that model would not republish", name)
		}
	}
	// And the reverse: a name here that the script no longer ships is a stale
	// determinant that republishes for nothing.
	for _, d := range declared {
		if !strings.Contains(string(b), "data/mcp-3gpp/models/"+d) {
			t.Errorf("publishInputs fingerprints data/models/%s, which build-image.sh no longer ships", d)
		}
	}
}

// TestPublishFingerprintsFilesNotDirectories. inputsHash records a directory as
// the constant string "dir", so a directory input is a fingerprint that can never
// change. Declaring data/models instead of its contents would have looked correct
// and detected nothing.
func TestPublishFingerprintsFilesNotDirectories(t *testing.T) {
	c, _ := newTestCtx(t)
	write := func(parts ...string) {
		p := filepath.Join(append([]string{c.Data}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("3gpp.duckdb")
	write("etsi.duckdb")
	for _, d := range imageModelDirs() {
		write("models", d, "model.onnx")
		write("models", d, "tokenizer.json")
	}

	ins, err := publishInputs(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range ins {
		st, err := os.Stat(in)
		if err != nil {
			continue // an absent path fingerprints as "absent", which is honest
		}
		if st.IsDir() {
			t.Errorf("publish declares the directory %s as an input: inputsHash records it as "+
				"the constant \"dir\", so it can never invalidate anything", in)
		}
	}
	// The model files must actually be in there, or the loop above is vacuous.
	var models int
	for _, in := range ins {
		if strings.Contains(filepath.ToSlash(in), "/models/") {
			models++
		}
	}
	if want := 2 * len(imageModelDirs()); models != want {
		t.Errorf("publish fingerprints %d model files, want %d — the walk is not reaching them", models, want)
	}
}

// TestPublishCoversEveryPackageTheServerLinks holds serverImplPackages to the
// package graph of the server the image ships, built the way build-image.sh builds
// it — tags and target read out of the script — in both directions.
//
// THE DEFECT THIS PINS, found by review on 2026-09-11. This test ran
// `go list -deps ./cmd/server` with no tags, for the host. The image's server is
// built `-tags "onnx,embed_ffi"` for GOOS=linux, and under `onnx`
// internal/rerank/rerank_onnx.go imports internal/onnxrt, which the untagged graph
// does not contain. serverImplPackages left it out and this test agreed, so an
// edit to the ONNX Runtime binding the image's reranker runs through planned
// publish as "fingerprint unchanged".
//
// BOTH DIRECTIONS, and the second is what pins the tags. Every package the image's
// server links must be declared, and every declared package must be one it links:
// read without the tags, the graph loses internal/onnxrt and this test names it as
// declared but not linked. A reader that silently dropped -tags fails here instead
// of passing on a smaller graph.
func TestPublishCoversEveryPackageTheServerLinks(t *testing.T) {
	requireGo(t)
	server := imageServerBuild(t)
	checkDeclaresWhatItsBinariesLink(t, "serverImplPackages", serverImplPackages(), []goBuildSpec{server},
		"a change there would ship in the image without republishing it")
}

// TestPublishCoversEveryPackageTheImageBuildCompiles holds publish's whole Impl to
// every Go build build-image.sh runs, each under its own tags and target: the
// server it ships and the host tools that build the image (zigcc, elfneeded,
// imgtar). A tool that grew an import of this module would change what the image
// is built with and sit outside every fingerprint.
func TestPublishCoversEveryPackageTheImageBuildCompiles(t *testing.T) {
	requireGo(t)
	checkDeclaresWhatItsBinariesLink(t, "publish", stepPublish().Impl, buildImageGoBuilds(t),
		"a change there changes what build-image.sh builds while publish reports the previous image as current")
}

// TestValidatePublishedRejectsWhatIsNotADigest, with the positive control that a
// well-formed record passes. A push that failed after writing the record is the
// case this catches.
func TestValidatePublishedRejectsWhatIsNotADigest(t *testing.T) {
	c, _ := newTestCtx(t)
	if err := os.MkdirAll(c.statePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(body string) {
		if err := os.WriteFile(c.statePath("published.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The positive controls are REAL digests — the one this repository last
	// published, and 64 identical letters — because a check tightened until it
	// rejects what a registry actually serves is worse than the loose one it
	// replaced: it would republish 40 GB on every build.
	for _, good := range []string{
		`{"tag":"ghcr.io/kodflow/3gpp-mcp:latest","digest":"sha256:` + strings.Repeat("a", 64) + `"}`,
		`{"tag":"ghcr.io/kodflow/3gpp-mcp:latest","digest":"sha256:0fcce82b4d3f93b977c776a0b3b7e4d89ab3fd09e14f28c9bdb1cbc245413b57"}`,
	} {
		put(good)
		if err := validatePublished(c); err != nil {
			t.Fatalf("a well-formed record was rejected: %v", err)
		}
	}
	for name, body := range map[string]string{
		"not json":       `{`,
		"no tag":         `{"digest":"sha256:` + strings.Repeat("a", 64) + `"}`,
		"empty digest":   `{"tag":"t","digest":""}`,
		"short digest":   `{"tag":"t","digest":"sha256:abc"}`,
		"unprefixed":     `{"tag":"t","digest":"` + strings.Repeat("a", 64) + `"}`,
		"digest missing": `{"tag":"t"}`,
		// 64 characters that are not hex. Counting them is what a length check
		// does, and a registry can never serve this — but a record carrying it
		// would make the planner skip the push.
		"non-hex": `{"tag":"t","digest":"sha256:` + strings.Repeat("z", 64) + `"}`,
		// Hex, but the wrong case. The OCI spec is lower-case only, and a digest
		// is compared as a string, so an upper-case one names nothing.
		"upper-case hex": `{"tag":"t","digest":"sha256:` + strings.Repeat("A", 64) + `"}`,
	} {
		put(body)
		if err := validatePublished(c); err == nil {
			t.Errorf("%s: validatePublished accepted it", name)
		}
	}
}

// TestTheImageTagFoldsIntoTheFingerprint. Publishing the same corpus to a
// different tag is a different artefact, and a step that ignored the tag would
// report the new one as already published.
func TestTheImageTagFoldsIntoTheFingerprint(t *testing.T) {
	c := publishCtx(t)
	s := publishStep(t)

	t.Setenv("IMAGE_TAG", "")
	base, err := s.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	if base["image_tag"] != defaultImageTag {
		t.Errorf("image_tag = %q with IMAGE_TAG unset, want the default %q", base["image_tag"], defaultImageTag)
	}

	t.Setenv("IMAGE_TAG", "ghcr.io/someone/other:v2")
	other, err := s.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	if other["image_tag"] == base["image_tag"] {
		t.Error("IMAGE_TAG does not reach the fingerprint: publishing elsewhere would be reported as already done")
	}
	if got := registryHost(other["image_tag"]); got != "ghcr.io" {
		t.Errorf("registryHost = %q, want ghcr.io — the credential probe would ask the wrong registry", got)
	}
}
