package goal

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// suppliedByPublish are the variables build-image.sh reads that runPublish never
// lets decide: it passes the value itself, as a flag, and refuses an operator's
// value that disagrees. They are neither imageKnobs (the environment does not pick
// the value) nor notAnImageKnob (the value is a determinant, folded in as the one
// runPublish passes), so the knob tests hand them to the tests in this file.
var suppliedByPublish = map[string]string{
	"EMBED_FLOOR": "runPublish passes --embed-floor, the floor validate applied (embed_floor.go); publish folds that value in as embed_floor and refuses the plan when EMBED_FLOOR is set to anything else",
}

// THE IMAGE IS HELD TO THE FLOOR VALIDATE APPLIED, AND VALIDATE'S CERTIFICATE SAYS
// SO — driven through runPublish itself, with a fake crane and a fake script that
// records what it was handed.
//
// Each case is a way the two gates used to disagree. Before embed_floor.go the
// script got no floor at all and applied its own ${EMBED_FLOOR:-Rel-99}, so the
// contract flag case and the no-floor case both held the image to Rel-99 — a floor
// validate had not applied — and the certificate, written for another floor,
// refused to match: the 8-minute re-run, and a different contract.
func TestPublishHandsTheScriptTheFloorValidateApplied(t *testing.T) {
	crane := buildFakeCrane(t)
	for _, tc := range []struct {
		name, configFloor, contract, want string
	}{
		{"the default", DefaultEmbedFloor, "--require-fts", DefaultEmbedFloor},
		{"a contract carrying its own floor (DATA_EMBED_FLOOR)", "Rel-99", "--require-fts --embed-floor=Rel-15", "Rel-15"},
		{"no floor: every release", "", "--require-fts", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EMBED_FLOOR", "")
			t.Setenv("CORPUS_CONTRACT_CERTIFIED", "")
			t.Setenv("IMAGE_TAG", "registry.invalid/kodflow/3gpp-mcp:probe")
			c, _ := newTestCtx(t)
			c.Config["embed_floor"] = tc.configFloor
			c.Config["contract_flags"] = tc.contract
			for _, f := range certifiedFiles(c) {
				write(t, f, "corpus bytes")
			}
			installFake(t, crane, c.bin("crane"))
			record := filepath.Join(c.Root, "script-args.txt")
			write(t, filepath.Join(c.Root, filepath.FromSlash(buildImageScript)),
				"#!/usr/bin/env bash\n{ printf 'ARG=%s\\n' \"$@\"; printf 'CERT=%s\\n' \"${CORPUS_CONTRACT_CERTIFIED:-}\"; } > \""+
					filepath.ToSlash(record)+"\"\n")

			// validate passes, and certifies, exactly as stepValidate's Run does.
			if err := writeContractCertificate(c, corpus3GPP()); err != nil {
				t.Fatal(err)
			}
			if err := runPublish(c); err != nil {
				t.Fatalf("runPublish: %v", err)
			}

			b, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var args []string
			cert := ""
			for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
				if v, ok := strings.CutPrefix(line, "ARG="); ok {
					args = append(args, v)
				}
				if v, ok := strings.CutPrefix(line, "CERT="); ok {
					cert = v
				}
			}
			got, ok := lastFlagValue(args, "embed-floor")
			if !ok {
				t.Fatalf("runPublish handed build-image.sh %q, with no --embed-floor: the script applies its own "+
					"${EMBED_FLOOR:-Rel-99}, a contract validate did not check", args)
			}
			applied, _ := lastFlagValue(validateArgs(c, corpus3GPP()), "embed-floor")
			if got != tc.want || got != applied {
				t.Fatalf("the image is held to floor %q; validate applied %q (want %q)", got, applied, tc.want)
			}
			if cert == "" {
				t.Fatalf("validate's certificate did not match the floor handed to the script (%q): the publish "+
					"re-runs the 8-minute contract for bytes validate already passed", got)
			}
		})
	}
}

// AN EMBED_FLOOR THAT DISAGREES REFUSES THE PLAN; ONE THAT AGREES CHANGES NOTHING.
//
// Honouring it would publish under a contract validate never checked; ignoring it
// would drop a setting somebody typed. So it must agree — and agreeing, it must not
// move the fingerprint, or exporting the value already in force replays a
// 22-minute publish.
func TestAnEmbedFloorThatDisagreesRefusesThePlan(t *testing.T) {
	c := publishCtx(t)
	c.Config["embed_floor"] = "Rel-99"
	c.Config["contract_flags"] = "--require-fts"
	s := publishStep(t)

	t.Setenv("EMBED_FLOOR", "")
	unset, err := s.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	if unset["embed_floor"] != "Rel-99" {
		t.Fatalf("publish fingerprints embed_floor=%q, validate applies Rel-99", unset["embed_floor"])
	}

	t.Setenv("EMBED_FLOOR", "Rel-99")
	same, err := s.Extra(c)
	if err != nil {
		t.Fatalf("EMBED_FLOOR set to the floor already in force was refused: %v", err)
	}
	for k, v := range unset {
		if same[k] != v {
			t.Errorf("EMBED_FLOOR at the floor in force moved %s: %q -> %q", k, v, same[k])
		}
	}

	t.Setenv("EMBED_FLOOR", "Rel-17")
	if m, err := s.Extra(c); err == nil {
		t.Fatalf("EMBED_FLOOR=Rel-17 against a pipeline at Rel-99 was accepted (embed_floor=%q): the image "+
			"would be published under a contract validate never checked, or the setting silently ignored", m["embed_floor"])
	} else if !strings.Contains(err.Error(), "GOAL_EMBED_FLOOR") {
		t.Errorf("the refusal does not say which knob to use instead: %v", err)
	}

	// The entries themselves must stay true: read by the script, and nowhere else.
	reads := buildImageEnvReads(t)
	for _, name := range sortedKeys(suppliedByPublish) {
		if _, ok := reads[name]; !ok {
			t.Errorf("suppliedByPublish names %s, which build-image.sh no longer reads", name)
		}
		if _, ok := notAnImageKnob[name]; ok {
			t.Errorf("%s is both supplied by publish and excused in notAnImageKnob", name)
		}
		for _, k := range imageKnobs {
			if k.Env == name {
				t.Errorf("%s is both supplied by publish and an imageKnob whose environment value decides", name)
			}
		}
	}
}

// THE FLOOR REACHES THE SCRIPT'S GATE UNCHANGED. The script's only EMBED_FLOOR
// assignments are its literal default and the --embed-floor flag, both before the
// gate, and the gate hands data-contract.sh exactly that variable — so the value
// runPublish passes is the value the contract applies, an empty one included.
func TestTheScriptsGateAppliesTheFloorItIsGiven(t *testing.T) {
	src := readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(buildImageScript)))
	class := shellClasses(src)
	code := []byte(src)
	for i := range code {
		if code[i] != '\n' && (class[i] == shComment || class[i] == shHeredoc) {
			code[i] = ' '
		}
	}
	text := string(code)

	assign := regexp.MustCompile(`(?m)(?:^|[\s;)])EMBED_FLOOR=("[^"\n]*"|\S*)`)
	var got []string
	for _, m := range assign.FindAllStringSubmatch(text, -1) {
		got = append(got, m[1])
	}
	want := []string{`"${EMBED_FLOOR:-` + DefaultEmbedFloor + `}"`, `"$2"`}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("build-image.sh assigns EMBED_FLOOR as %q, want exactly %q (the default, then the flag): "+
			"any other assignment can replace the floor runPublish passed before the gate reads it", got, want)
	}
	if !regexp.MustCompile(`(?m)^\s*--embed-floor\)\s*EMBED_FLOOR="\$2"; shift 2 ;;`).MatchString(text) {
		t.Fatal("build-image.sh does not read --embed-floor into EMBED_FLOOR in its argument loop")
	}

	data := regexp.MustCompile(`DATA_EMBED_FLOOR=("[^"\n]*"|\S*)`).FindAllStringSubmatch(text, -1)
	if len(data) != 1 || data[0][1] != `"$EMBED_FLOOR"` {
		t.Fatalf("build-image.sh hands data-contract.sh DATA_EMBED_FLOOR as %q, want exactly one `\"$EMBED_FLOOR\"`: "+
			"a default re-applied there (it was ${EMBED_FLOOR:-Rel-99}) turns the empty floor runPublish passes "+
			"into Rel-99", data)
	}
	flag := strings.Index(text, `--embed-floor) EMBED_FLOOR="$2"`)
	gate := strings.Index(text, `DATA_EMBED_FLOOR="$EMBED_FLOOR"`)
	if flag < 0 || gate < 0 || flag > gate {
		t.Fatalf("the flag (at %d) must be read before the gate (at %d)", flag, gate)
	}
}

// ONE DEFAULT. What an unset floor means is written in four places: goal's
// --embed-floor (the pipeline), build-image.sh (a standalone `make image`),
// corpus-local.sh and publish-corpus.sh. They were equal by coincidence; this
// holds each of them to DefaultEmbedFloor, which cmd/goal's own test holds its
// flag to.
func TestEveryEmbedFloorDefaultIsThePipelines(t *testing.T) {
	for _, rel := range []string{buildImageScript, "scripts/local/corpus-local.sh", "scripts/local/publish-corpus.sh"} {
		src := readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(rel)))
		defaults := literalDefaultsOf(src, "EMBED_FLOOR")
		if len(defaults) == 0 {
			t.Errorf("%s writes no literal ${EMBED_FLOOR:-…}: this test checks nothing there", rel)
		}
		for _, d := range defaults {
			if d.Value != DefaultEmbedFloor {
				t.Errorf("%s:%d defaults EMBED_FLOOR to %q, the pipeline to %q: the same unset floor would hold "+
					"the corpus to two contracts", rel, d.Line, d.Value, DefaultEmbedFloor)
			}
		}
	}
	// And the reader publish fingerprints with agrees.
	if d, err := shellDefault(repoRootForTest(), buildImageScript, "EMBED_FLOOR"); err != nil || d != DefaultEmbedFloor {
		t.Errorf("shellDefault(build-image.sh, EMBED_FLOOR) = %q, %v", d, err)
	}
}

// THE CERTIFICATE NAMES THE FLOOR cmd/validate WAS GIVEN. A contract carrying its
// own --embed-floor wins over the config floor in validateArgs; a certificate that
// recorded the config floor would vouch for a check that never ran, and match an
// image held to that other floor.
func TestTheCertificateNamesTheFloorValidateWasGiven(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["embed_floor"] = "Rel-99"
	c.Config["contract_flags"] = "--require-fts --embed-floor Rel-15"
	for _, f := range certifiedFiles(c) {
		write(t, f, "corpus bytes")
	}
	if err := writeContractCertificate(c, corpus3GPP()); err != nil {
		t.Fatal(err)
	}
	if why := contractCertified(c, "Rel-99"); why != "" {
		t.Fatalf("a contract run at Rel-15 certified the Rel-99 floor: %s", why)
	}
	if contractCertified(c, "Rel-15") == "" {
		t.Fatal("the certificate does not vouch for the floor validate was given")
	}
}

func TestLastFlagValueReadsEverySpellingTheFlagPackageAccepts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		ok   bool
	}{
		{[]string{"--embed-floor", "Rel-15"}, "Rel-15", true},
		{[]string{"-embed-floor", "Rel-15"}, "Rel-15", true},
		{[]string{"--embed-floor=Rel-15"}, "Rel-15", true},
		{[]string{"-embed-floor=Rel-15"}, "Rel-15", true},
		{[]string{"--embed-floor="}, "", true},
		{[]string{"--embed-floor", ""}, "", true},
		{[]string{"--embed-floor", "Rel-15", "--embed-floor", "Rel-18"}, "Rel-18", true},
		{[]string{"--embed-floor", "Rel-15", "--", "--embed-floor", "Rel-18"}, "Rel-15", true},
		{[]string{"--", "--embed-floor=Rel-18"}, "", false},
		{[]string{"--embed-floor-extra", "x"}, "", false},
		{[]string{"--db", "--embed-floor"}, "", false},
		{nil, "", false},
	} {
		if got, ok := lastFlagValue(tc.args, "embed-floor"); got != tc.want || ok != tc.ok {
			t.Errorf("lastFlagValue(%q) = %q, %v; want %q, %v", tc.args, got, ok, tc.want, tc.ok)
		}
	}
}

// THE FLOOR THIS PACKAGE CALLS APPLIED IS THE ONE THE FLAG PACKAGE APPLIES, over the
// argument lists validateArgs actually builds — parsed here by a real flag.FlagSet,
// the parser cmd/validate runs its arguments through. The terminator cases are the
// ones review of #330 found: a floor after "--" is a positional argument, never
// applied.
func TestLastFlagValueAgreesWithTheFlagPackage(t *testing.T) {
	for _, tc := range []struct{ configFloor, contract string }{
		{"Rel-99", "--require-fts --require-hnsw"},
		{"Rel-99", "--require-fts --embed-floor Rel-15"},
		{"Rel-99", "--require-fts -embed-floor=Rel-15 --embed-floor Rel-17"},
		{"", "--require-fts"},
		{"Rel-99", "--require-fts -- --embed-floor=Rel-15"},
		{"Rel-99", "--require-fts --embed-floor Rel-15 -- --embed-floor Rel-17"},
	} {
		c, _ := newTestCtx(t)
		c.Config["embed_floor"] = tc.configFloor
		c.Config["contract_flags"] = tc.contract
		args := validateArgs(c, corpus3GPP())

		fs := flag.NewFlagSet("validate", flag.ContinueOnError)
		fs.String("db", "", "")
		fs.String("report", "", "")
		floor := fs.String("embed-floor", "", "")
		fs.Bool("require-fts", false, "")
		fs.Bool("require-hnsw", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if got := appliedEmbedFloor(c, corpus3GPP()); got != *floor {
			t.Errorf("validate runs %q, which the flag package reads as floor %q; appliedEmbedFloor says %q — "+
				"the certificate, the fingerprint and the image would name a floor the gate did not apply",
				args, *floor, got)
		}
	}
}

// buildFakeCrane compiles a crane that answers `auth get` and `digest` and nothing
// else, so runPublish can be driven with no registry — and with no path to one.
func buildFakeCrane(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "digest" {
		fmt.Println("sha256:" + strings.Repeat("a", 64))
		return
	}
	os.Exit(9)
}
`
	write(t, filepath.Join(dir, "main.go"), src)
	out := filepath.Join(dir, "crane.exe")
	cmd := exec.Command("go", "build", "-o", out, "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fake crane: %v\n%s", err, b)
	}
	return out
}

func installFake(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}
