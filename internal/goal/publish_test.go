package goal

import (
	"os"
	"os/exec"
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

// TestPublishRunsAfterBothHalvesAreFrozen, with the negative control that makes
// the second dependency load-bearing: index-etsi is NOT reachable from smoke, so
// depending on smoke alone would leave the publish unordered against the ETSI
// freeze — and an ETSI corpus whose HNSW is still "building" is one the server
// refuses to serve.
func TestPublishRunsAfterBothHalvesAreFrozen(t *testing.T) {
	steps := map[string]*Step{}
	for _, s := range Pipeline() {
		steps[s.Name] = s
	}
	pub := steps["publish"]
	for _, want := range []string{"smoke", "index-etsi"} {
		if !slices.Contains(pub.Deps, want) {
			t.Errorf("publish does not depend on %s — it can ship a half nothing finished (deps: %v)", want, pub.Deps)
		}
	}

	// The control: if smoke already ordered index-etsi, naming it would be
	// decoration and this test would pass for the wrong reason.
	seen := map[string]bool{}
	var reach func(string)
	reach = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		if s, ok := steps[n]; ok {
			for _, d := range append(append([]string{}, s.Deps...), s.AnyDeps...) {
				reach(d)
			}
		}
	}
	reach("smoke")
	if seen["index-etsi"] {
		t.Error("index-etsi is already reachable from smoke — this test no longer proves anything; " +
			"re-derive what orders the publish after the ETSI freeze")
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
// real package graph. A new import in cmd/server would otherwise ship in the
// image while being invisible to the step that publishes it.
func TestPublishCoversEveryPackageTheServerLinks(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH — this test compares against `go list -deps ./cmd/server`")
	}
	// From the repository root, not this package's directory: `./cmd/server` is
	// relative, and running it from here made the test SKIP itself — a guard that
	// silently stops guarding is worse than no guard.
	cmd := exec.Command("go", "list", "-deps", "./cmd/server")
	cmd.Dir = repoRootForTest()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/server failed in %s: %v", cmd.Dir, err)
	}
	const mod = "github.com/kodflow/3gpp-mcp/"
	declared := serverImplPackages()
	for _, line := range strings.Split(string(out), "\n") {
		pkg := strings.TrimSpace(line)
		if !strings.HasPrefix(pkg, mod) {
			continue
		}
		rel := strings.TrimPrefix(pkg, mod)
		covered := false
		for _, d := range declared {
			// A declared directory covers its subpackages: Impl walks directories.
			if rel == d || strings.HasPrefix(rel, d+"/") {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("cmd/server links %s, which publish does not name in Impl — a change there "+
				"would ship in the image without republishing it", rel)
		}
	}
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
	good := `{"tag":"ghcr.io/kodflow/3gpp-mcp:latest","digest":"sha256:` + strings.Repeat("a", 64) + `"}`
	put(good)
	if err := validatePublished(c); err != nil {
		t.Fatalf("a well-formed record was rejected: %v", err)
	}
	for name, body := range map[string]string{
		"not json":       `{`,
		"no tag":         `{"digest":"sha256:` + strings.Repeat("a", 64) + `"}`,
		"empty digest":   `{"tag":"t","digest":""}`,
		"short digest":   `{"tag":"t","digest":"sha256:abc"}`,
		"unprefixed":     `{"tag":"t","digest":"` + strings.Repeat("a", 64) + `"}`,
		"digest missing": `{"tag":"t"}`,
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
	c, _ := newTestCtx(t)
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
