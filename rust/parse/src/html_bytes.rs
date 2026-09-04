//! html_bytes — reading a converted HTML file whatever encoding it actually carries.
//!
//! Lifted out of the ingest binary so every pass over the converted corpus decodes
//! identically. A second reader that only accepted UTF-8 would silently skip the
//! same files this exists to rescue, and the repo has already paid for a helper
//! copied instead of shared.

/// read_html reads a converted document as text, accepting the encodings LibreOffice
/// actually emits rather than the one we would prefer.
///
/// LibreOffice's HTML export keeps the SOURCE document's charset. A .doc written in
/// Western Europe comes out as windows-1252, not UTF-8, and `read_to_string` refuses
/// it outright: "stream did not contain valid UTF-8". ingest then logs a skip line to
/// stderr and reports SUCCESS for the series, so the spec is fetched, converted, and
/// silently never indexed. All six releases of TS 34.123-1 were lost that way on the
/// 2026-08-25 repair — acquired three times over, dropped three times over, and
/// counted as a corpus hole nobody could explain.
///
/// So: use UTF-8 when the bytes are UTF-8, and otherwise decode as windows-1252,
/// which is what those files are. The mapping is Latin-1 for 0xA0-0xFF and the
/// CP1252 punctuation block for 0x80-0x9F — the quotes and dashes Word litters
/// through prose. Nothing is dropped and no byte can fail to decode, which is the
/// point: a conformance spec must not be discarded over a typographic apostrophe.
pub fn read_html(path: &str) -> std::io::Result<String> {
    let bytes = std::fs::read(path)?;
    match String::from_utf8(bytes) {
        Ok(s) => Ok(s),
        Err(e) => {
            let bytes = e.into_bytes();
            eprintln!("parse: {path}: not UTF-8, decoding as windows-1252");
            Ok(bytes.iter().map(|&b| cp1252_char(b)).collect())
        }
    }
}

/// cp1252_char maps one windows-1252 byte to its Unicode character. 0x00-0x7F and
/// 0xA0-0xFF are Latin-1, i.e. identical to the code point; 0x80-0x9F is the CP1252
/// punctuation block, which Latin-1 leaves undefined and Word uses constantly.
fn cp1252_char(b: u8) -> char {
    const HIGH: [char; 32] = [
        '\u{20AC}', '\u{FFFD}', '\u{201A}', '\u{0192}', '\u{201E}', '\u{2026}', '\u{2020}',
        '\u{2021}', '\u{02C6}', '\u{2030}', '\u{0160}', '\u{2039}', '\u{0152}', '\u{FFFD}',
        '\u{017D}', '\u{FFFD}', '\u{FFFD}', '\u{2018}', '\u{2019}', '\u{201C}', '\u{201D}',
        '\u{2022}', '\u{2013}', '\u{2014}', '\u{02DC}', '\u{2122}', '\u{0161}', '\u{203A}',
        '\u{0153}', '\u{FFFD}', '\u{017E}', '\u{0178}',
    ];
    if (0x80..=0x9F).contains(&b) {
        HIGH[(b - 0x80) as usize]
    } else {
        b as char
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // A converted document must not be discarded over its punctuation.
    //
    // LibreOffice's HTML export keeps the SOURCE document's charset, so a .doc
    // written in Western Europe comes out as windows-1252 and `read_to_string`
    // refuses it: "stream did not contain valid UTF-8". ingest logged a skip line to
    // stderr and reported SUCCESS for the series — so all six releases of
    // TS 34.123-1 were fetched, converted, and silently never indexed, then counted
    // as corpus holes nobody could explain.
    #[test]
    fn a_windows_1252_document_is_read_rather_than_discarded() {
        let dir = std::env::temp_dir().join(format!("ingest-cp1252-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("doc.html");

        // 0xE9 is 'é' in windows-1252 and an invalid lone byte in UTF-8; 0x92 is the
        // right single quote Word inserts for every apostrophe it touches.
        let mut bytes = b"<html><body><p>proc".to_vec();
        bytes.push(0xE9); // é
        bytes.extend_from_slice(b"dure d");
        bytes.push(0x92); // ’
        bytes.extend_from_slice(b"essai</p></body></html>");
        std::fs::write(&p, &bytes).unwrap();

        let got = read_html(p.to_str().unwrap()).expect("a non-UTF-8 document must still be read");
        assert!(
            got.contains("procédure"),
            "Latin-1 accents must survive: {got}"
        );
        assert!(
            got.contains('\u{2019}'),
            "the CP1252 quote must decode, not vanish: {got}"
        );
        assert!(
            !got.contains('\u{FFFD}'),
            "nothing may be replaced by U+FFFD: {got}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    // Valid UTF-8 must go through untouched — the fallback exists for the files that
    // need it, and must not become a lossy path for the files that do not.
    #[test]
    fn a_utf8_document_is_untouched() {
        let dir = std::env::temp_dir().join(format!("ingest-utf8-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("doc.html");
        let want = "<p>procédure d’essai — 日本語</p>";
        std::fs::write(&p, want.as_bytes()).unwrap();

        let got = read_html(p.to_str().unwrap()).unwrap();
        assert_eq!(got, want, "valid UTF-8 must be returned byte for byte");

        let _ = std::fs::remove_dir_all(&dir);
    }

    // The CP1252 punctuation block is the whole reason a plain Latin-1 decode is not
    // enough: Latin-1 leaves 0x80-0x9F undefined, and Word fills it constantly.
    #[test]
    fn the_cp1252_punctuation_block_decodes() {
        for (byte, want) in [
            (0x80u8, '\u{20AC}'), // €
            (0x93, '\u{201C}'),   // “
            (0x94, '\u{201D}'),   // ”
            (0x96, '\u{2013}'),   // –
            (0x97, '\u{2014}'),   // —
        ] {
            assert_eq!(cp1252_char(byte), want, "byte {byte:#04x}");
        }
        // Outside that block the byte IS the code point.
        assert_eq!(cp1252_char(0x41), 'A');
        assert_eq!(cp1252_char(0xE9), 'é');
    }
}
