package goal

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// notAnImageKnob records the environment variables build-image.sh reads that do
// NOT change the image it produces, each with the reason. It is the ONLY place such
// a variable can be recorded, so excusing one from publish's fingerprint is a
// decision someone typed and a reviewer can see — the same shape as
// countsTestFiles and armShared.
//
// Everything else the script reads from the environment must move publish's
// fingerprint; TestPublishFingerprintsEveryKnobBuildImageReads fails on anything
// that is in neither place, and on an entry here whose variable is gone.
var notAnImageKnob = map[string]string{
	"IMAGE_PUSH_ATTEMPTS":       "how many times a failed push is retried: one that lands pushes the same digests whatever the count (imgtar fixes every timestamp, the registry dedupes), and one that never lands records nothing to skip on",
	"PATH":                      "where go, cargo, curl and crane are found — the script puts its own .local toolchain first — which is a question of toolchain identity, not an override of what the image holds",
	"BASH_SOURCE":               "set by bash to the script's own path; no operator can set it",
	"CORPUS_CONTRACT_CERTIFIED": "decides only whether the script re-runs the contract, never a byte of the image; runPublish always sets it — to a reason only when validate's certificate matches the files (contract_certificate.go), to empty otherwise, which disarms one inherited from the shell",
}

// EVERY VARIABLE build-image.sh READS FROM THE ENVIRONMENT MOVES publish's
// FINGERPRINT, OR IS EXCUSED IN notAnImageKnob WITH A REASON.
//
// THE DEFECT THIS PINS, found by an independent review on 2026-09-10. publish's
// Extra held image_tag alone, while the script also read IMAGE_BASE, ZIG_TARGET,
// ORT_VERSION, EMBED_FLOOR and DATA_CONTRACT out of the environment runPublish
// hands it. Exporting IMAGE_BASE=debian:trixie-slim planned as
//
//	[SKIP] publish   fingerprint unchanged, outputs present and valid
//
// and the image on the registry — the previous base, the previous runtime — was
// reported as the one this machine had just built.
//
// COUNT, DON'T TRUST THE COMMENT. The variables are read out of the script, and
// each is judged by what the fingerprint DOES when it moves, not by whether its
// name appears in imageKnobs: a knob declared there and folded in wrongly would
// pass a membership check and fails this one.
func TestPublishFingerprintsEveryKnobBuildImageReads(t *testing.T) {
	c := publishCtx(t)
	reads := buildImageEnvReads(t)

	for _, name := range sortedKeys(reads) {
		r := reads[name]
		why, excused := notAnImageKnob[name]
		moved := knobMovesPublish(t, c, name)
		switch {
		case !excused && !moved:
			t.Errorf("build-image.sh:%d reads %s from the environment, and publish's fingerprint does "+
				"not move when it changes:\n\t%s\nExport it and the plan says SKIP while the image on the "+
				"registry was built without it. Fold its effective value in through imageKnobs, or record "+
				"in notAnImageKnob why it cannot change the image.", r.Line, name, r.Text)
		case excused && moved:
			t.Errorf("notAnImageKnob excuses %s (%s), yet publish's fingerprint moves with it: either it "+
				"changes the image and the entry is wrong, or the fingerprint counts something that cannot "+
				"change it and republishes 40 GB for nothing", name, why)
		}
	}

	// AN EXCUSE MUST NOT OUTLIVE ITS VARIABLE. The map is consulted by NAME, so a
	// stale entry silently excuses whatever next takes that name — the reason
	// TestNoArmExceptionOutlivesItsStep exists.
	for _, name := range sortedKeys(notAnImageKnob) {
		why := notAnImageKnob[name]
		if strings.TrimSpace(why) == "" {
			t.Errorf("notAnImageKnob excuses %s with an empty reason; the reason IS the entry", name)
		}
		if _, ok := reads[name]; !ok {
			t.Errorf("notAnImageKnob excuses %s (%s), which build-image.sh no longer reads: delete the "+
				"entry, or the next variable to take that name inherits an excuse nobody gave it", name, why)
		}
	}
	// And the same for a determinant: a knob the script no longer reads replays a
	// 40 GB publish whenever somebody exports it, for an image it cannot change.
	for _, k := range imageKnobs {
		if _, ok := reads[k.Env]; !ok {
			t.Errorf("publish fingerprints %s, which build-image.sh no longer reads: exporting it would "+
				"republish an image it cannot change", k.Env)
		}
	}
}

// LEAVING A KNOB UNSET AND SETTING IT TO ITS DEFAULT ARE ONE IMAGE, AND MUST BE
// ONE FINGERPRINT.
//
// Otherwise exporting a knob at the value it already has replays a publish that
// streams 40 GB off disk to rediscover identical digests; and a fingerprint that
// folds in what the operator TYPED rather than what the script USES records a
// determinant the build never had.
//
// The defaults are read here by this file's own reader, not by shellDefault, and
// EVERY literal default build-image.sh writes for a fingerprinted variable is held
// to the fingerprint — the ones in log lines too. That is what makes a stale copy
// fail rather than drift: build-image.sh labelled its gate `${DATA_CONTRACT:-dense}`
// through the seven publishes after data-contract.sh's default had become
// dense+sparse+etsi.
func TestAnUnsetKnobAndItsDefaultAreOneImage(t *testing.T) {
	c := publishCtx(t)
	script := readRepoFile(t, buildImageScript)

	type literal struct{ where, value string }
	defaults := map[string][]literal{}
	for name := range buildImageEnvReads(t) {
		if _, excused := notAnImageKnob[name]; excused {
			continue
		}
		for _, d := range literalDefaultsOf(script, name) {
			defaults[name] = append(defaults[name], literal{fmt.Sprintf("%s:%d", buildImageScript, d.Line), d.Value})
		}
	}
	// The knobs whose default build-image.sh does not decide are followed to the
	// file that does — once it is established that the script really goes there.
	// Otherwise the fingerprint would track a default the build never consults.
	for _, k := range imageKnobs {
		if k.DefaultIn == buildImageScript {
			continue
		}
		if !usesFile(script, k.DefaultIn) {
			t.Errorf("imageKnobs reads the %s default out of %s, but build-image.sh never uses that "+
				"file outside a comment or a message: the fingerprint follows a default the build does not",
				k.Env, k.DefaultIn)
			continue
		}
		for _, d := range literalDefaultsOf(readRepoFile(t, k.DefaultIn), k.Env) {
			defaults[k.Env] = append(defaults[k.Env], literal{fmt.Sprintf("%s:%d", k.DefaultIn, d.Line), d.Value})
		}
	}
	// Every knob must have been given a default to check, or this test says
	// nothing about it.
	for _, k := range imageKnobs {
		if len(defaults[k.Env]) == 0 {
			t.Errorf("found no literal default for %s where imageKnobs says it lives (%s): this test "+
				"cannot check it, and shellDefault cannot read it either", k.Env, k.DefaultIn)
		}
	}

	for _, name := range sortedKeys(defaults) {
		unset := publishExtraWith(t, c, name, "")
		for _, d := range defaults[name] {
			set := publishExtraWith(t, c, name, d.value)
			if !reflect.DeepEqual(unset, set) {
				t.Errorf("%s writes the %s default as %q, but %s=%q fingerprints differently from leaving "+
					"it unset:\n\tunset %v\n\tset   %v\nEither the fingerprint folds in the operator's "+
					"spelling instead of the value the script uses, or %s is a second copy of a default "+
					"that has drifted from the one that applies.", d.where, name, d.value, name, d.value,
					unset, set, d.where)
			}
		}
	}
}

// shellDefault DECIDES A FINGERPRINT, SO IT MUST REFUSE TO GUESS. Every error
// below is a case where a lenient reader would record a default the script does
// not use, and the plan would SKIP on it.
func TestShellDefaultRefusesToGuess(t *testing.T) {
	root := t.TempDir()
	read := func(body string) (string, error) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "s.sh"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return shellDefault(root, "s.sh", "KNOB")
	}
	for _, tc := range []struct {
		name, body, want string
	}{
		{"a literal", `X="${KNOB:-lit}"`, "lit"},
		{"a comment is not the script", "# ${KNOB:-commented}\nX=\"${KNOB:-lit}\"", "lit"},
		{"${KNOB:-} is set -u, not a default", "echo \"${KNOB:-}\"\nX=\"${KNOB:-lit}\"", "lit"},
		{"copies that agree are one default", "X=\"${KNOB:-a}\"\necho \"${KNOB:-a}\"", "a"},
	} {
		if got, err := read(tc.body); err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v — want %q", tc.name, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, body string }{
		{"a computed default", `X="${KNOB:-$(cat f)}"`},
		{"a quoted default", `X=${KNOB:-"lit"}`},
		{"two defaults that disagree", "X=\"${KNOB:-a}\"\necho \"${KNOB:-b}\""},
		{"no default at all", `X="$KNOB"`},
	} {
		if got, err := read(tc.body); err == nil {
			t.Errorf("%s: shellDefault answered %q instead of refusing — the fingerprint would record a "+
				"default the script does not use", tc.name, got)
		}
	}
}

// THE READER, CHECKED ON A SCRIPT WHOSE ANSWER IS KNOWN. The first two tests in
// this file are only as good as shellEnvReads, and each rule it follows is here
// with the case that would break if it stopped: every name in mustRead is a way
// to miss a knob, every name in mustNotRead a way to demand an excuse for nothing.
func TestShellEnvReadsTellsAReadFromAnAssignment(t *testing.T) {
	src := strings.Join([]string{
		`#!/usr/bin/env bash`,
		`# a comment naming $IN_A_COMMENT reads nothing`,
		`set -euo pipefail`,
		`ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"`,
		`cd "$ROOT"`,
		`TAG="${IMAGE_TAG:-x}"`,
		`export TARGET="${TARGET:-y}"`,
		`echo "one string; FAKE=1 assigns nothing"`,
		`USE="${FAKE:-0}"`,
		`for f in a b; do echo "$f"; done`,
		`while IFS= read -r line; do echo "$line"; done <<<"$TAG"`,
		`g() { local k="$1" v; v="$k"; echo "$v"; }`,
		`cat <<'EOF'`,
		`$QUOTED_HEREDOC`,
		`EOF`,
		`cat <<EOF`,
		`$PLAIN_HEREDOC`,
		`EOF`,
		`echo "${LATE:-}"; LATE=1`,
		`echo "$LATE" \$ESCAPED "${#LENGTH_OF}" '$SINGLE'`,
		`echo "$USE $TARGET"`,
		``,
	}, "\n")
	got := shellEnvReads(src)

	mustRead := map[string]string{
		"BASH_SOURCE":   "read inside quotes nested in a command substitution",
		"IMAGE_TAG":     "the plain ${VAR:-default} knob",
		"TARGET":        "X=\"${X:-d}\" reads X before the assignment takes effect — the shape of ZIG_TARGET and ORT_VERSION",
		"FAKE":          "an assignment inside a string assigns nothing, so the later read comes from the environment",
		"LATE":          "read before the assignment that follows it on the same line",
		"PLAIN_HEREDOC": "an unquoted heredoc expands",
		"LENGTH_OF":     "${#NAME} reads NAME",
		"SINGLE":        "single-quoted text is reported although bash does not expand it: nested quotes are where a reader goes wrong, and the wrong answer must be the loud one",
	}
	mustNotRead := map[string]string{
		"ROOT":           "assigned before it is read",
		"TAG":            "assigned, then read by a here-string",
		"USE":            "assigned before it is read",
		"f":              "a for variable is assigned before its body reads it",
		"line":           "read -r assigns it before the loop body reads it",
		"k":              "declared by local",
		"v":              "declared by local without a value",
		"IN_A_COMMENT":   "a comment is not a read",
		"QUOTED_HEREDOC": "a quoted heredoc does not expand",
		"ESCAPED":        `\$ is not a read`,
	}
	for _, name := range sortedKeys(mustRead) {
		if _, ok := got[name]; !ok {
			t.Errorf("the reader missed %s — %s. A missed read is a knob that escapes publish's fingerprint.",
				name, mustRead[name])
		}
	}
	for _, name := range sortedKeys(mustNotRead) {
		if r, ok := got[name]; ok {
			t.Errorf("the reader reported %s as an environment read at line %d (%s) — %s",
				name, r.Line, r.Text, mustNotRead[name])
		}
	}
	for name, r := range got {
		if _, known := mustRead[name]; !known {
			if _, known := mustNotRead[name]; !known {
				t.Errorf("the reader reported %s (line %d: %s), which this script does not read from the "+
					"environment at all", name, r.Line, r.Text)
			}
		}
	}
}

// ---------------------------------------------------------------- helpers

// publishCtx is a test context rooted at this checkout, because publish's Extra
// reads its defaults out of the real scripts.
func publishCtx(t *testing.T) *Ctx {
	t.Helper()
	c, _ := newTestCtx(t)
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}
	c.Root = root
	return c
}

// publishExtraWith is publish's Extra with name set to value — "" being unset, as
// far as `${name:-…}` can tell — and the environment put back afterwards, so one
// probe never leaks into the next.
func publishExtraWith(t *testing.T, c *Ctx, name, value string) map[string]string {
	t.Helper()
	orig, had := os.LookupEnv(name)
	t.Setenv(name, value)
	defer func() {
		if had {
			os.Setenv(name, orig)
		} else {
			os.Unsetenv(name)
		}
	}()
	m, err := publishStep(t).Extra(c)
	if err != nil {
		t.Fatalf("publish's Extra failed with %s=%q: %v", name, value, err)
	}
	return m
}

// knobMovesPublish reports whether setting name, and nothing else, changes what
// publish folds into its fingerprint.
func knobMovesPublish(t *testing.T, c *Ctx, name string) bool {
	t.Helper()
	unset := publishExtraWith(t, c, name, "")
	probe := publishExtraWith(t, c, name, "probe-"+strings.ToLower(name))
	return !reflect.DeepEqual(unset, probe)
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRootForTest(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("cannot read %s, so this test cannot know what it says: %v", rel, err)
	}
	return string(b)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildImageEnvReads is shellEnvReads over the real build-image.sh, with the guard
// every reader in this package carries.
func buildImageEnvReads(t *testing.T) map[string]envRead {
	t.Helper()
	reads := shellEnvReads(readRepoFile(t, buildImageScript))
	if len(reads) < 3 {
		t.Fatalf("only %d environment read(s) found in build-image.sh — the reader is broken, and a broken "+
			"reader makes this test pass for the wrong reason: %v", len(reads), sortedKeys(reads))
	}
	return reads
}

// envRead is where a script first reads a variable from the environment.
type envRead struct {
	Line int    // 1-based
	Text string // that line, trimmed, for the failure message
}

// shellEnvReads returns every variable a bash script reads before it has
// assigned it itself — which, for a script, is what reading the environment
// means — at the first place it does so.
//
// It is a reader, not a parser, and it is biased on purpose: where it cannot
// tell, it reports a read. A false read fails loudly and costs one line in
// notAnImageKnob; a missed read is the defect these tests exist to catch.
//
//   - A read is `$NAME` or `${NAME…}` in any form (`${NAME:-x}`, `${#NAME}`, …)
//     anywhere but a comment or a quoted heredoc — single-quoted text included,
//     although bash does not expand it, because nested quotes are where a reader
//     goes wrong and the wrong answer must be the loud one. `\$` is not a read.
//   - An assignment counts only in code, never inside a string: `echo "…; FOO=1"`
//     assigns nothing, and counting it would hide a later read of FOO.
//   - `NAME=value`, and any name `local`/`export` declares, takes effect at the
//     END of its statement — the next ;, &, | or newline outside a string — so
//     its right-hand side reads the environment: `X="${X:-default}"` is a read,
//     which is the shape of ZIG_TARGET and ORT_VERSION. `for NAME in` and
//     `read NAME` take effect where they stand, so the bodies they introduce read
//     an assigned variable.
//
// Not seen: a bare NAME inside $(( )), and a name the script only hands a child
// as `NAME=value cmd` and then reads itself, which is taken for an assignment.
// Neither occurs in build-image.sh.
func shellEnvReads(src string) map[string]envRead {
	class := shellClasses(src)

	assignText := []byte(src)
	readText := []byte(src)
	for i := range src {
		if src[i] == '\n' {
			continue
		}
		if class[i] != shCode {
			assignText[i] = ' '
		}
		if class[i] == shComment || class[i] == shHeredoc {
			readText[i] = ' '
		}
	}

	firstRead := map[string]int{}
	for i := 0; i < len(readText); i++ {
		if readText[i] != '$' {
			continue
		}
		escapes := 0
		for j := i - 1; j >= 0 && readText[j] == '\\'; j-- {
			escapes++
		}
		if escapes%2 == 1 {
			continue
		}
		j := i + 1
		if j < len(readText) && readText[j] == '{' {
			j++
			if j < len(readText) && (readText[j] == '#' || readText[j] == '!') {
				j++
			}
		}
		k := j
		for k < len(readText) && isShellNameByte(readText[k], k == j) {
			k++
		}
		if k == j {
			continue
		}
		if name := string(readText[j:k]); !hasKey(firstRead, name) {
			firstRead[name] = i
		}
	}

	firstAssign := map[string]int{}
	assigned := func(name string, at int) {
		if prev, ok := firstAssign[name]; !ok || at < prev {
			firstAssign[name] = at
		}
	}
	text := string(assignText)
	// Strings are blanked in text, so a separator found here is a real one.
	endOfStatement := func(off int) int {
		if n := strings.IndexAny(text[off:], ";&|\n"); n >= 0 {
			return off + n
		}
		return len(text)
	}
	for _, m := range shAssign.FindAllStringSubmatchIndex(text, -1) {
		assigned(text[m[2]:m[3]], endOfStatement(m[1]))
	}
	// The declared list stops at the separator, so its end is the statement's.
	for _, m := range shDeclare.FindAllStringSubmatchIndex(text, -1) {
		for _, tok := range strings.Fields(text[m[2]:m[3]]) {
			if strings.HasPrefix(tok, "-") {
				continue
			}
			name, _, _ := strings.Cut(tok, "=")
			if shName.MatchString(name) {
				assigned(name, m[3])
			}
		}
	}
	for _, m := range shFor.FindAllStringSubmatchIndex(text, -1) {
		assigned(text[m[2]:m[3]], m[1])
	}
	for _, m := range shRead.FindAllStringSubmatchIndex(text, -1) {
		for _, name := range strings.Fields(text[m[2]:m[3]]) {
			assigned(name, m[1])
		}
	}

	lines := strings.Split(src, "\n")
	out := map[string]envRead{}
	for name, at := range firstRead {
		if as, ok := firstAssign[name]; ok && as < at {
			continue
		}
		line := strings.Count(src[:at], "\n") + 1
		out[name] = envRead{Line: line, Text: strings.TrimSpace(lines[line-1])}
	}
	return out
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

func isShellNameByte(b byte, first bool) bool {
	switch {
	case b == '_', b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return !first
	}
	return false
}

// What each byte of a script is, for the reader above.
const (
	shCode         byte = iota
	shDouble            // inside "…"
	shSingle            // inside '…'
	shComment           // from a word-initial # to the end of the line
	shHeredoc           // the body of <<'EOF': never expanded
	shHeredocPlain      // the body of <<EOF: expanded, never assigns
)

var (
	shName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// Where a command, and so an assignment, can begin.
	shStmt = `(?:^|[;&|{)]|\bthen\b|\bdo\b|\belse\b)[ \t]*`
	// A condition can begin with `read` too: `while IFS= read -r line`.
	shCond    = `(?:^|[;&|{)!]|\bthen\b|\bdo\b|\belse\b|\bwhile\b|\buntil\b|\bif\b|\belif\b)[ \t]*`
	shAssign  = regexp.MustCompile(`(?m)` + shStmt + `(?:(?:export|local|readonly|declare|typeset)[ \t]+(?:-[A-Za-z]+[ \t]+)*)?([A-Za-z_][A-Za-z0-9_]*)\+?=`)
	shDeclare = regexp.MustCompile(`(?m)` + shStmt + `(?:export|local|readonly|declare|typeset)[ \t]+([^;&|\n]*)`)
	shFor     = regexp.MustCompile(`(?m)` + shStmt + `for[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+in\b`)
	shRead    = regexp.MustCompile(`(?m)` + shCond + `(?:[A-Za-z_][A-Za-z0-9_]*=[^ \t\n]*[ \t]+)*read(?:[ \t]+-[A-Za-z]+)*((?:[ \t]+[A-Za-z_][A-Za-z0-9_]*)+)`)
	shHereOp  = regexp.MustCompile(`^<<(-?)[ \t]*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?`)
)

// shellClasses classifies every byte of a script as code, a string, a comment or
// a heredoc body.
//
// Quotes are tracked without nesting, which is wrong inside "$(cmd "arg")" and
// harmlessly so: bash quotes nest in balanced pairs, so the reader is back in
// step at the end of the construct, and the text it misfiles in between is
// scanned for reads either way.
func shellClasses(src string) []byte {
	class := make([]byte, len(src))
	type heredoc struct {
		delim         string
		quoted, strip bool
	}
	var pending []heredoc
	state := shCode
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch state {
		case shCode:
			class[i] = shCode
			switch {
			case c == '\\':
				if i+1 < len(src) {
					i++
					class[i] = shCode
				}
			case c == '\'':
				state = shSingle
			case c == '"':
				state = shDouble
			case c == '#' && (i == 0 || strings.IndexByte(" \t\n;", src[i-1]) >= 0):
				state = shComment
				class[i] = shComment
			case c == '<' && strings.HasPrefix(src[i:], "<<") && !strings.HasPrefix(src[i:], "<<<") &&
				(i == 0 || src[i-1] != '<'):
				if m := shHereOp.FindStringSubmatch(src[i:]); m != nil {
					pending = append(pending, heredoc{delim: m[3], quoted: m[2] != "", strip: m[1] != ""})
				}
			case c == '\n' && len(pending) > 0:
				// The bodies start on the next line, one after another, each ending
				// at a line that is exactly its delimiter.
				j := i + 1
				for _, h := range pending {
					kind := shHeredocPlain
					if h.quoted {
						kind = shHeredoc
					}
					for j < len(src) {
						end := strings.IndexByte(src[j:], '\n')
						if end < 0 {
							end = len(src) - j
						}
						line := src[j : j+end]
						if h.strip {
							line = strings.TrimLeft(line, "\t")
						}
						for k := j; k < j+end; k++ {
							class[k] = kind
						}
						j += end + 1
						if line == h.delim {
							break
						}
					}
				}
				pending = nil
				i = j - 1
			}
		case shDouble:
			class[i] = shDouble
			if c == '\\' && i+1 < len(src) {
				i++
				class[i] = shDouble
				continue
			}
			if c == '"' {
				state = shCode
			}
		case shSingle:
			class[i] = shSingle
			if c == '\'' {
				state = shCode
			}
		case shComment:
			if c == '\n' {
				// Hand the newline back to code: a heredoc opened on a line that
				// ends in a comment still starts on the next one.
				state = shCode
				i--
				continue
			}
			class[i] = shComment
		}
	}
	return class
}

// literalDefault is one `${NAME:-value}` a script writes.
type literalDefault struct {
	Line  int
	Value string
}

// literalDefaultsOf lists every literal default a script writes for name, outside
// comments. `${name:-}` is not a default — it is how a `set -u` script reads a
// variable that may be unset — and a computed one ($, backtick, quote) is not a
// literal; both are skipped. This is deliberately a second reader, independent of
// shellDefault: the test must not grade the production reader with itself.
func literalDefaultsOf(src, name string) []literalDefault {
	class := shellClasses(src)
	re := regexp.MustCompile(`\$\{` + regexp.QuoteMeta(name) + `:-([^}]*)\}`)
	var out []literalDefault
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		if class[m[0]] == shComment || class[m[0]] == shHeredoc {
			continue
		}
		v := src[m[2]:m[3]]
		if v == "" || strings.ContainsAny(v, "$`\"'\\") {
			continue
		}
		out = append(out, literalDefault{Line: strings.Count(src[:m[0]], "\n") + 1, Value: v})
	}
	return out
}

// usesFile reports whether a script names rel on a line that is neither a
// comment nor a message — a use of the file rather than a mention of it.
func usesFile(src, rel string) bool {
	class := shellClasses(src)
	message := regexp.MustCompile(`\b(?:die|say|echo|printf)\b`)
	off := 0
	for _, line := range strings.SplitAfter(src, "\n") {
		if at := strings.Index(line, rel); at >= 0 && class[off+at] != shComment &&
			!message.MatchString(line[:at]) {
			return true
		}
		off += len(line)
	}
	return false
}
