package model

import (
	"fmt"
	"strconv"
	"strings"
)

// 3GPP filename version codes are 3 chars: <major><v2><v3>, each char base-36-ish
// where 0-9 = 0..9 and a-z = 10..35. The major IS the release ordinal:
// f=15, g=16, h=17, i=18, j=19, k=20 ... and major 3 == Rel-99 (Release 1999).
// See CLAUDE.md §8.1 and scripts/corpus.sh decode_char.

// decodeChar maps a single code char to its numeric value, or -1 if invalid.
func decodeChar(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return 10 + int(c-'a')
	case c >= 'A' && c <= 'Z':
		return 10 + int(c-'A')
	default:
		return -1
	}
}

func encodeChar(n int) (byte, bool) {
	switch {
	case n >= 0 && n <= 9:
		return byte('0' + n), true
	case n >= 10 && n <= 35:
		return byte('a' + (n - 10)), true
	default:
		return 0, false
	}
}

// ReleaseFromMajor turns a 3GPP version major into a release label. The real
// release line is: Rel-99 (major 3) → Rel-4 (major 4) → … → Rel-20. There is no
// "Rel-98": after Rel-99 the count jumps to Rel-4. Majors 0/1/2 are pre-Rel-99
// DRAFTS (not releases) — callers floor them out. (GSM "Phase 1/2" specs are the
// separate 4-digit series with their own numbering, not handled here.)
func ReleaseFromMajor(major int) string {
	if major == 3 {
		return "Rel-99"
	}
	return fmt.Sprintf("Rel-%d", major)
}

// ReleaseOrdinal maps a release label to its comparable ordinal (the version
// major): "Rel-99" -> 3, "Rel-4".."Rel-20" -> 4..20. Returns (0,false) for
// drafts / unparseable labels (majors 0/1/2 are pre-Rel-99 drafts, not releases).
// Inverse of ReleaseFromMajor; used to compare releases against a floor.
func ReleaseOrdinal(rel string) (int, bool) {
	if rel == "Rel-99" {
		return 3, true
	}
	s, ok := strings.CutPrefix(rel, "Rel-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 4 {
		return 0, false
	}
	return n, true
}

// VersionMajor returns the integer major of a dotted "X.Y.Z" version, or -1 when
// it cannot be parsed (fail-safe: an unparseable version is never "stable").
func VersionMajor(version string) int {
	s := version
	if i := strings.IndexByte(version, '.'); i >= 0 {
		s = version[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// IsStableVersion reports whether a dotted 3GPP version is STABLE (published)
// rather than a DRAFT. 3GPP drafts carry a major of 0/1/2 (work in progress
// before a spec is first published for a release); major >= 3 is published
// (3 = Release 1999, 4..20 = Rel-4..Rel-20). The server defaults to surfacing
// stable content and labels every citation with this, so a client is never
// silently handed a draft (CLAUDE.md §8 piège #8: frozen ≠ stable).
func IsStableVersion(version string) bool { return VersionMajor(version) >= 3 }

// ReleaseGSM is the coarse, HONEST release bucket for legacy GSM Phase-1/2 specs
// (the 4-digit spec numbers / series 00-13). Their filename version-code scheme is
// reverse-engineered and cannot be decoded to an exact 3GPP release line, so — by an
// explicit architectural decision (CLAUDE.md §0; the cite-exactly rule §1) — they are
// labelled with this single factual bucket instead of a guessed release. spec_id,
// clause, the literal code-derived version, and url all stay exact. ReleaseOrdinal
// returns (0,false) for "GSM", so legacy clauses sit below ANY embed floor: they are
// indexed LEXICALLY but never vectorised (vectors are recent-release-only).
const ReleaseGSM = "GSM"

// IsLegacyGSM reports whether a spec NUMBER (digits only, e.g. "0408") is a legacy
// GSM Phase-1/2 spec: the 4-digit numbers (GSM 04.08), as opposed to the modern
// 5-digit 3GPP specs (TS 23.501 = "23501"). Mirrors corpus.sh's 4-digit gate.
func IsLegacyGSM(specNum string) bool { return len(specNum) == 4 }

// DecodeVersionCode turns a 3-char code ("i60") into its release ("Rel-18")
// and dotted version ("18.6.0"). ok is false when the code is malformed.
func DecodeVersionCode(code string) (release, version string, ok bool) {
	switch len(code) {
	case 3:
		// Compact form: one base-36-ish char per component (h60 = 17.6.0).
		maj, v2, v3 := decodeChar(code[0]), decodeChar(code[1]), decodeChar(code[2])
		if maj < 0 || v2 < 0 || v3 < 0 {
			return "", "", false
		}
		return ReleaseFromMajor(maj), fmt.Sprintf("%d.%d.%d", maj, v2, v3), true
	case 6:
		// 6-digit decimal form for high components: two digits each (083700 = 8.37.0).
		maj, e1 := strconv.Atoi(code[0:2])
		v2, e2 := strconv.Atoi(code[2:4])
		v3, e3 := strconv.Atoi(code[4:6])
		if e1 != nil || e2 != nil || e3 != nil {
			return "", "", false
		}
		return ReleaseFromMajor(maj), fmt.Sprintf("%d.%d.%d", maj, v2, v3), true
	}
	return "", "", false
}

// EncodeVersionCode is the inverse of DecodeVersionCode: "18.6.0" -> "i60".
func EncodeVersionCode(version string) (string, bool) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", false
	}
	var out [3]byte
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", false
		}
		c, ok := encodeChar(n)
		if !ok {
			return "", false
		}
		out[i] = c
	}
	return string(out[:]), true
}

// ArchiveURL reconstructs the canonical 3GPP archive ZIP URL for a spec
// version, e.g. ("33.128","18.6.0") ->
// https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/33128-i60.zip
func ArchiveURL(specID, version string) string {
	series := SeriesOf(specID)
	num := strings.ReplaceAll(specID, ".", "")
	code, ok := EncodeVersionCode(version)
	if series == "" || num == "" || !ok {
		return ""
	}
	return fmt.Sprintf("https://www.3gpp.org/ftp/Specs/archive/%s_series/%s/%s-%s.zip",
		series, specID, num, code)
}

// ForgeRawURL builds the immutable 3GPP Forge raw-file URL for a 5G_APIs YAML
// pinned to a commit SHA, e.g. ("TS29518_Namf_Communication.yaml","4e19b0…") ->
// https://forge.3gpp.org/rep/all/5G_APIs/-/raw/4e19b0…/TS29518_Namf_Communication.yaml
// An optional schema fragment appends #/components/schemas/<name>. Pinning to a
// SHA (not a branch) is what makes the citation reproducible (CLAUDE.md §1).
func ForgeRawURL(file, sha, schemaFragment string) string {
	if file == "" || sha == "" {
		return ""
	}
	u := fmt.Sprintf("https://forge.3gpp.org/rep/all/5G_APIs/-/raw/%s/%s", sha, file)
	if schemaFragment != "" {
		u += "#/components/schemas/" + schemaFragment
	}
	return u
}

// SeriesOf returns the 2-digit series of a spec id ("33.128" -> "33").
func SeriesOf(specID string) string {
	if i := strings.IndexByte(specID, '.'); i > 0 {
		return specID[:i]
	}
	return ""
}

// seriesWG maps a series to its owning 3GPP working group. Best-effort: only
// the well-known ones; unknown series yield "".
var seriesWG = map[string]string{
	"21": "SA", "22": "SA1", "23": "SA2", "24": "CT1", "25": "RAN",
	"26": "SA4", "27": "CT", "28": "SA5", "29": "CT3", "31": "CT6",
	"32": "SA5", "33": "SA3", "35": "SA3", "36": "RAN", "37": "RAN", "38": "RAN",
}

// WorkingGroupForSeries returns the owning WG for a series, or "".
func WorkingGroupForSeries(series string) string { return seriesWG[series] }
