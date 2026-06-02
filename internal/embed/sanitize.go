package embed

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// sanitizeForTokenizer normalises clause text into something the XLM-RoBERTa
// SentencePiece tokenizer (github.com/sugarme/tokenizer v0.3.0) handles without
// its Metaspace/Precompiled pretokenizer tripping a "slice bounds out of range
// [2n+1:2n]" panic, AND that carries only linguistic signal. Two artefact
// classes in the 3GPP LibreOffice-HTML corpus motivate it:
//
//   - Private Use Area glyphs (U+E000–U+F8FF and the two supplementary PUA
//     planes) — Wingdings/Symbol font hacks emitted by the .doc→HTML convert.
//     They carry zero meaning and are a prime panic trigger.
//   - Massive whitespace runs — a single rendered-table clause can hold 2000+
//     tabs of layout indentation, which both bloat the sequence (padding every
//     batch-mate) and stress the pretokenizer.
//
// PUA runes are dropped; every run of whitespace/controls collapses to one
// space; invalid UTF-8 is dropped. The output is the real text the clause
// carries. This is the tokenizer INPUT only — ClauseHash still keys on the
// original EmbedText, so the resume/skip contract is unchanged (a clause is not
// re-embedded just because sanitisation is deterministic and stable).
func sanitizeForTokenizer(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	emitSpace := func() {
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	for _, r := range s {
		switch {
		case r == utf8.RuneError, isPUA(r):
			emitSpace()
		case unicode.IsSpace(r), unicode.IsControl(r):
			emitSpace()
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// isPUA reports whether r is in a Unicode Private Use Area (BMP or either
// supplementary plane). PUA codepoints have no standard meaning — in this corpus
// they are font-glyph artefacts of the document conversion.
func isPUA(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) || // BMP PUA
		(r >= 0xF0000 && r <= 0xFFFFD) || // Supplementary PUA-A
		(r >= 0x100000 && r <= 0x10FFFD) // Supplementary PUA-B
}
