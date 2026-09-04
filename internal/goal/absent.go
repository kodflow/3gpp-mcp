package goal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The accepted-absent ledger: what 3GPP's status report lists but does not host.
//
// The status report is a CATALOGUE, not an inventory. It names (spec, release)
// pairs at versions whose archive is not on the FTP site at all — drafts that
// never shipped, versions withdrawn after announcement, entries that only ever
// existed as a row in the report. Measured here on 2026-09-03: of the 201 pairs
// the delta called missing or behind, 177 answered FAILDL and 4 more resolved to
// a LOWER version already in the corpus. Not one produced a new file.
//
// Without a memory of that, those 201 are drift forever. `discover` re-reports
// them on every run, `fetch` runs on every run, and because a step that ran
// republishes its provenance, ingest, merge, embed, sparse, compact and index all
// replay behind it — hours of GPU and 22 GB of rewriting, on every build, to
// discover again that upstream has nothing to give. That is not a slow pipeline,
// it is a pipeline that cannot converge.
//
// The read side of this already existed and had never been wired up: rust/discover
// takes --absent-index and build_have() merges it over the corpus index, so a key
// recorded here stops counting as drift. What was missing is the producer. This is
// it: fetch reports what upstream refused, and the next discover stops asking.
//
// The ledger records the VERSION that was absent, never the key alone. If 3GPP
// later publishes something HIGHER, cmp_ver puts it above the ledger entry and it
// comes back as drift, exactly as it should. Only the precise version that was
// proven missing is forgotten.

// verCodeRe matches a 3GPP archive file name: "<num>-<code>.zip", where code is
// the version encoded as three base-36 digits ("a50" == 10.5.0) or, once any
// component passes 35, three zero-padded decimal pairs ("016400" == 1.64.0).
var verCodeRe = regexp.MustCompile(`/([0-9]{2})_series/([^/]+)/[^/]+-([0-9a-z]{3}|[0-9]{6})\.zip`)

// faildlRe and fallbackRe are corpus.sh's two ways of saying "upstream does not
// have the version you asked for". FAILDL is outright absence, after the retry
// ladder has run. FALLBACK is the same absence, discovered by finding a LOWER
// version in the directory and taking that instead — so the requested version is
// just as absent, and the substitute is one the corpus already holds.
var (
	faildlRe   = regexp.MustCompile(`FAILDL (\S+)`)
	fallbackRe = regexp.MustCompile(`FALLBACK (\S+) ->`)
)

// decodeVerCode inverts the 3GPP archive version encoding. It is the exact
// inverse of rust/discover's encode_ver_code, and the two must stay that way: a
// version that decodes to something the status report would not have written is
// a version cmp_ver will read as drift, and the key comes straight back.
func decodeVerCode(code string) (string, bool) {
	digits := make([]int64, 0, 3)
	switch len(code) {
	case 3:
		for _, ch := range code {
			switch {
			case ch >= '0' && ch <= '9':
				digits = append(digits, int64(ch-'0'))
			case ch >= 'a' && ch <= 'z':
				digits = append(digits, int64(ch-'a')+10)
			default:
				return "", false
			}
		}
	case 6:
		for i := 0; i < 6; i += 2 {
			hi, lo := code[i], code[i+1]
			if hi < '0' || hi > '9' || lo < '0' || lo > '9' {
				return "", false
			}
			digits = append(digits, int64(hi-'0')*10+int64(lo-'0'))
		}
	default:
		return "", false
	}
	var b strings.Builder
	for i, d := range digits {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatInt(d, 10))
	}
	return b.String(), true
}


// worklistReleases maps each archive URL to the release the work list asked for
// it under. The release cannot be recovered from the URL — the archive tree is
// laid out by series, not by release — and a ledger key without it would name a
// spec rather than a (spec, release), which is not what the corpus index is
// keyed by.
func worklistReleases(worklist string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(worklist, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		out[f[1]] = f[0]
	}
	return out
}

// absentFromLog reads a fetch transcript and returns the (spec|release ->
// version) pairs upstream did not serve.
//
// It reports only what the work list asked for: an entry whose URL is not in the
// work list came from somewhere else and its release is unknown, and guessing one
// would write a key that silences the wrong spec.
func absentFromLog(worklist, logText string) map[string]string {
	releases := worklistReleases(worklist)
	out := map[string]string{}
	for _, re := range []*regexp.Regexp{faildlRe, fallbackRe} {
		for _, m := range re.FindAllStringSubmatch(logText, -1) {
			url := m[1]
			rel, ok := releases[url]
			if !ok {
				continue
			}
			p := verCodeRe.FindStringSubmatch(url)
			if p == nil {
				continue
			}
			ver, ok := decodeVerCode(p[3])
			if !ok {
				continue
			}
			// p[2] is the spec directory ("36.571-3"), which is the authority on
			// where a spec id ends. Deriving it from the file name instead is how
			// 34.123-1 gets parsed wrong.
			key := p[2] + "|" + rel
			if cmpVerTriple(ver, out[key]) > 0 {
				out[key] = ver
			}
		}
	}
	return out
}

// cmpVerTriple orders two dotted versions the way rust/discover's cmp_ver does:
// on the first three components, missing ones reading as zero. Anything else and
// the two sides disagree about what "newer" means, which is the whole contract
// between this ledger and the delta.
func cmpVerTriple(a, b string) int {
	ta, tb := triple(a), triple(b)
	for i := 0; i < 3; i++ {
		if ta[i] != tb[i] {
			if ta[i] < tb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func triple(s string) [3]int64 {
	var t [3]int64
	if s == "" {
		return t
	}
	for i, p := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		var v int64
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				v = 0
				break
			}
			v = v*10 + int64(ch-'0')
		}
		t[i] = v
	}
	return t
}

// absentIndexPath is where the ledger lives. It sits beside corpus-index.json
// because it is read with it and means nothing without it: together they are
// "what the corpus holds, and what upstream has proven it cannot give".
func absentIndexPath(c *Ctx) string { return filepath.Join(c.Local, "absent-index.json") }

// recordAbsent folds this fetch's refusals into the ledger.
//
// The transcript is the evidence, because corpus.sh reports FAILDL and FALLBACK
// on stderr and writes no machine-readable list of its own. Reading the step's
// own log keeps the producer next to the run that proved the absence, rather than
// asking a later step to re-derive it from a file nobody maintains.
func recordAbsent(c *Ctx, worklistPath string) error {
	wl, err := os.ReadFile(worklistPath)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(c.Log.Path())
	if err != nil {
		return err
	}
	found := absentFromLog(string(wl), string(b))
	if len(found) == 0 {
		return nil
	}
	added, err := mergeAbsentIndex(absentIndexPath(c), found)
	if err != nil {
		return err
	}
	c.Log.Printf("upstream did not serve %d of the requested (spec, release) versions; %d newly accepted as absent",
		len(found), added)
	return nil
}

// mergeAbsentIndex folds newly proven absences into the ledger at path and
// reports how many keys it gained.
//
// It only ever raises a version, never lowers or removes one. A ledger that could
// shrink would let a transient network failure un-accept a key that a previous
// run had proven absent, and the pipeline would start asking for it again — which
// is the loop this exists to close. A key genuinely worth retrying comes back on
// its own, because upstream publishes a HIGHER version and the delta outranks the
// ledger entry.
func mergeAbsentIndex(path string, found map[string]string) (added int, err error) {
	ledger := map[string]string{}
	if b, readErr := os.ReadFile(path); readErr == nil {
		_ = json.Unmarshal(b, &ledger)
	}
	for k, v := range found {
		if cmpVerTriple(v, ledger[k]) > 0 {
			if _, seen := ledger[k]; !seen {
				added++
			}
			ledger[k] = v
		}
	}
	b, err := json.MarshalIndent(ledger, "", " ")
	if err != nil {
		return 0, err
	}
	return added, WriteAtomic(path, append(b, '\n'))
}
