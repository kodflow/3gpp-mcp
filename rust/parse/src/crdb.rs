//! crdb — the 3GPP Change Request database, which is where a changelog actually
//! comes from.
//!
//! WHY THIS EXISTS. The `changes` table had no writer. The Go HTML-ingest write
//! side that once filled it was deleted when the write side moved to Rust
//! (Phase 11b, c635038) and the Rust ingest never reimplemented the change-history
//! parser, so the table sat as a fossil: 61 321 rows over 3 452 specs of which only
//! 311 named anything citable, and 3 026 of them the literal string "Date" — the
//! change-history table's COLUMN HEADER, read positionally like a body row by the
//! parser that is now gone. PR #311 stopped `get_changelog` serving that header; it
//! did not give the table a writer.
//!
//! WHY NOT PARSE THE CHANGE-HISTORY TABLE OUT OF THE SPECS. That is the obvious
//! repair and it is the wrong one, measured. The converted tree on this machine
//! holds 1 410 documents covering 1 038 specs, against 3 568 specs in the corpus:
//! `fetch` downloads a delta and the corpus is the durable record, so a parser
//! wired into ingest would cover whichever third of the archive happened to be on
//! disk. Worse, it would cover none of it — `ingest --resume` skips a
//! (spec, version) the corpus already holds whatever the parser version says, so
//! the rows would only appear after re-fetching all 20 163 versions from 3gpp.org.
//!
//! The change-history table printed in a spec is a RENDERING of this database.
//! 3GPP publishes the database itself, complete, as one file:
//! `/ftp/Information/Databases/Change_Request/CRDB_<date>.zip` — 57 MB holding a
//! single xlsx of 596 696 change requests across every spec and every release. One
//! download, no re-fetch, and it is the authority the documents are generated from
//! rather than a second-hand transcription of them.
//!
//! WHAT THIS REFUSES TO DO. The defect being repaired is a POSITIONAL read: a
//! parser that counted columns and handed row 1 to the corpus as data. So this
//! reader never counts columns. It binds every field to the NAME in the header row
//! and fails loudly when a required name is absent, which puts both halves of that
//! defect out of reach by construction — the header row is CONSUMED as the header
//! and can never be emitted, and a re-ordered or renamed export is refused instead
//! of being silently mis-columned.
//!
//! The same trap has a second form here. Cells of an xlsx are SPARSE: an empty cell
//! is omitted from the file entirely rather than written empty, so the column a
//! value belongs to comes from its `r` reference ("D5" → column D) and never from
//! its position among its siblings. Reading them in order is the identical
//! positional bug wearing a different file format.

use quick_xml::events::{BytesStart, Event};
use quick_xml::Reader;
use std::collections::HashMap;
use std::io::{Cursor, Error, ErrorKind, Read, Result, Seek};

/// One change request, as the CRDB publishes it.
///
/// `tdoc` is the TSG TDoc IDENTIFIER ("SP-180090"), which is what the database
/// carries and what a reader searches by. It is deliberately not turned into a
/// URL: the document's real location encodes the TSG and the meeting
/// (`/ftp/tsg_sa/TSG_SA/TSGS_79/Docs/SP-180090.zip`), neither of which is
/// derivable from the identifier alone, so a synthesised link would be a citation
/// this corpus cannot stand behind.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct CrRow {
    pub spec_id: String,
    pub cr_number: String,
    pub cr_revision: Option<i32>,
    pub summary: String,
    pub meeting: String,
    pub category: String,
    pub release: String,
    pub from_version: String,
    pub to_version: String,
    pub tdoc: String,
    pub tsg_status: String,
    pub wg_status: String,
}

/// The header names this reader binds to, and refuses to run without.
const COL_SPEC: &str = "Spec number";
const COL_CR: &str = "CR number";
const COL_REV: &str = "Revision";
const COL_SUBJECT: &str = "Subject";
const COL_MEETING: &str = "Meeting TSG-level";
const COL_CATEGORY: &str = "Category";
const COL_RELEASE: &str = "Release";
const COL_VCUR: &str = "Version-current";
const COL_VNEW: &str = "Version-new";
const COL_TDOC: &str = "TSG Tdoc";
const COL_TSG_STATUS: &str = "TSG-level status";
const COL_WG_STATUS: &str = "WG-level status";

const REQUIRED: [&str; 12] = [
    COL_SPEC,
    COL_CR,
    COL_REV,
    COL_SUBJECT,
    COL_MEETING,
    COL_CATEGORY,
    COL_RELEASE,
    COL_VCUR,
    COL_VNEW,
    COL_TDOC,
    COL_TSG_STATUS,
    COL_WG_STATUS,
];

fn invalid(msg: impl Into<String>) -> Error {
    Error::new(ErrorKind::InvalidData, msg.into())
}

/// col_of turns a cell reference into a 0-based column index: "D5" → 3, "AB12" → 27.
///
/// None for a reference with no leading letters, which is a malformed cell rather
/// than an empty one — the caller drops it instead of guessing a column.
pub(crate) fn col_of(r: &str) -> Option<usize> {
    let letters = r.chars().take_while(|c| c.is_ascii_alphabetic());
    let mut n = 0usize;
    let mut any = false;
    for c in letters {
        any = true;
        n = n * 26 + ((c.to_ascii_uppercase() as u8 - b'A') as usize + 1);
    }
    any.then(|| n - 1)
}

/// "-" is how the CRDB spells an empty cell, and it is not a value.
///
/// This matters more than it looks. A "-" left in `Version-current` next to a "-"
/// in `CR number` is exactly the shape of an MCC editorial row, and letting the
/// dashes through would make `model.Change.Citable` answer true for a record that
/// names nothing — re-creating from a different direction the un-citable rows this
/// whole change exists to remove.
pub(crate) fn clean(s: &str) -> String {
    let t = s.trim();
    if t == "-" {
        String::new()
    } else {
        t.to_string()
    }
}

/// read_shared_strings materialises `xl/sharedStrings.xml`.
///
/// An `<si>` is either one `<t>` or a run of `<r><t>` fragments; both concatenate
/// to a single string, and ignoring the runs would silently truncate every subject
/// that carries formatting.
fn read_shared_strings(xml: &[u8]) -> Result<Vec<String>> {
    let mut rd = Reader::from_reader(xml);
    rd.config_mut().trim_text(false);
    let mut buf = Vec::new();
    let mut out: Vec<String> = Vec::new();
    let mut cur = String::new();
    let mut in_si = false;
    let mut depth_t = 0usize;
    loop {
        match rd.read_event_into(&mut buf) {
            Ok(Event::Start(e)) => match e.local_name().as_ref() {
                b"si" => {
                    in_si = true;
                    cur.clear();
                }
                b"t" if in_si => depth_t += 1,
                _ => {}
            },
            Ok(Event::Empty(e)) if e.local_name().as_ref() == b"si" => out.push(String::new()),
            Ok(Event::Text(t)) if depth_t > 0 => {
                cur.push_str(&t.unescape().map_err(|e| invalid(e.to_string()))?);
            }
            Ok(Event::End(e)) => match e.local_name().as_ref() {
                b"t" => depth_t = depth_t.saturating_sub(1),
                b"si" => {
                    in_si = false;
                    out.push(std::mem::take(&mut cur));
                }
                _ => {}
            },
            Ok(Event::Eof) => break,
            Err(e) => return Err(invalid(format!("sharedStrings.xml: {e}"))),
            _ => {}
        }
        buf.clear();
    }
    Ok(out)
}

/// The `r` and `t` attributes of a `<c>` element: which column, and how to read it.
fn cell_attrs(e: &BytesStart) -> (Option<usize>, Vec<u8>) {
    let mut col = None;
    let mut kind = Vec::new();
    for a in e.attributes().flatten() {
        match a.key.local_name().as_ref() {
            b"r" => col = col_of(&String::from_utf8_lossy(&a.value)),
            b"t" => kind = a.value.to_vec(),
            _ => {}
        }
    }
    (col, kind)
}

/// header_map turns row 1 into name → column index, or refuses the workbook.
fn header_map(row: &HashMap<usize, String>) -> Result<HashMap<String, usize>> {
    let mut m = HashMap::new();
    for (ci, name) in row.iter() {
        m.insert(name.trim().to_string(), *ci);
    }
    let missing: Vec<&str> = REQUIRED
        .iter()
        .copied()
        .filter(|c| !m.contains_key(*c))
        .collect();
    if !missing.is_empty() {
        let mut got: Vec<&str> = m.keys().map(|s| s.as_str()).collect();
        got.sort_unstable();
        return Err(invalid(format!(
            "CRDB header does not carry [{}] — refusing to read it positionally (it carries: {})",
            missing.join(", "),
            got.join(", ")
        )));
    }
    Ok(m)
}

/// for_each_cr streams every change request of a CRDB archive to `f`, and returns
/// how many it emitted.
///
/// `src` is either the published `CRDB_<date>.zip` or the `.xlsx` inside it; both
/// are accepted because both are what an operator ends up holding.
///
/// Streaming rather than collecting is deliberate. The sheet is 480 MB of XML for
/// 596 696 rows, and this repository has an OOM history in exactly that shape
/// (embed-io, builds 20 and 21): a Vec of every row would hold tens of megabytes
/// of Strings while the caller has done nothing with them yet, for no gain over
/// handing each row over as it is read.
pub fn for_each_cr<R, F>(src: R, mut f: F) -> Result<usize>
where
    R: Read + Seek,
    F: FnMut(CrRow),
{
    let mut zip = zip::ZipArchive::new(src).map_err(|e| invalid(format!("not a zip: {e}")))?;

    // The published archive wraps the workbook. Unwrap it once, into memory: the
    // inner reader has to be seekable to be read as a zip of its own, and 58 MB is
    // small beside the 480 MB sheet it contains.
    let inner = (0..zip.len()).find_map(|i| {
        let n = zip.by_index(i).ok()?.name().to_string();
        n.to_ascii_lowercase().ends_with(".xlsx").then_some(n)
    });
    if let Some(name) = inner {
        let mut bytes = Vec::new();
        zip.by_name(&name)
            .map_err(|e| invalid(format!("{name}: {e}")))?
            .read_to_end(&mut bytes)?;
        return for_each_cr(Cursor::new(bytes), f);
    }

    let mut shared_xml = Vec::new();
    zip.by_name("xl/sharedStrings.xml")
        .map_err(|e| invalid(format!("xl/sharedStrings.xml: {e}")))?
        .read_to_end(&mut shared_xml)?;
    let shared = read_shared_strings(&shared_xml)?;
    drop(shared_xml);

    let sheet = zip
        .by_name("xl/worksheets/sheet1.xml")
        .map_err(|e| invalid(format!("xl/worksheets/sheet1.xml: {e}")))?;
    let mut rd = Reader::from_reader(std::io::BufReader::with_capacity(1 << 20, sheet));
    rd.config_mut().trim_text(false);

    let mut buf = Vec::new();
    let mut row: HashMap<usize, String> = HashMap::new();
    let mut col: Option<usize> = None;
    let mut kind: Vec<u8> = Vec::new();
    let mut val = String::new();
    let mut in_value = 0usize;
    let mut header: Option<HashMap<String, usize>> = None;
    let mut emitted = 0usize;
    // A TRUNCATED SHEET MUST NOT LOOK LIKE A SHORT ONE.
    //
    // quick-xml reports end-of-input as Eof whether or not the document closed, so
    // a structurally valid zip whose worksheet stops mid-row parses as a SUCCESSFUL
    // PREFIX. That is the one input that turns replace_changes — which DELETEs
    // before it INSERTs — into a way to lose the changelog: a half-downloaded
    // export would replace 256 471 records with however many rows arrived, and
    // report success. So the close is tracked and its absence is an error.
    let mut in_row = false;
    let mut closed = false;

    loop {
        match rd.read_event_into(&mut buf) {
            Ok(Event::Start(e)) => match e.local_name().as_ref() {
                b"row" => {
                    row.clear();
                    in_row = true;
                }
                b"c" => {
                    let (c, k) = cell_attrs(&e);
                    col = c;
                    kind = k;
                    val.clear();
                }
                // <v> holds a number or a shared-string index; <t> inside <is> holds
                // an inline string. Both are "the text of this cell".
                b"v" | b"t" => in_value += 1,
                _ => {}
            },
            // A self-closing <c r="D5"/> is a styled but EMPTY cell: it carries no
            // text and must not leave the previous cell's value behind it.
            Ok(Event::Empty(e)) if e.local_name().as_ref() == b"c" => {
                col = None;
                kind.clear();
                val.clear();
            }
            Ok(Event::Text(t)) if in_value > 0 => {
                val.push_str(&t.unescape().map_err(|e| invalid(e.to_string()))?);
            }
            Ok(Event::End(e)) => match e.local_name().as_ref() {
                b"v" | b"t" => in_value = in_value.saturating_sub(1),
                b"c" => {
                    if let Some(ci) = col {
                        let text = if kind == b"s" {
                            // A shared-string cell holds an INDEX, and one this table
                            // cannot resolve is a CORRUPT WORKBOOK, not an empty cell.
                            //
                            // Reading it as empty is the quiet failure: an unresolved
                            // Spec number, TSG-level status or Version-new silently
                            // drops the record, and replace_changes then commits the
                            // short set as a complete replacement. The corpus would
                            // lose changes and every counter would agree with itself.
                            // So it fails, naming the cell and the index.
                            let raw = val.trim();
                            let idx = raw.parse::<usize>().map_err(|_| {
                                invalid(format!(
                                    "sheet1.xml: cell in column {ci} holds a non-numeric shared-string index {raw:?}"
                                ))
                            })?;
                            shared
                                .get(idx)
                                .cloned()
                                .ok_or_else(|| {
                                    invalid(format!(
                                        "sheet1.xml: cell in column {ci} names shared string {idx}, but the table holds {}",
                                        shared.len()
                                    ))
                                })?
                        } else {
                            val.clone()
                        };
                        if !text.is_empty() {
                            row.insert(ci, text);
                        }
                    }
                    val.clear();
                    col = None;
                    kind.clear();
                }
                b"row" => {
                    match header.as_ref() {
                        // ROW 1 IS THE HEADER AND IS CONSUMED HERE. It becomes the
                        // column map and is never handed to the caller, which is the
                        // structural reason "Date" cannot come back.
                        None => header = Some(header_map(&row)?),
                        Some(h) => {
                            let get = |name: &str| -> String {
                                h.get(name)
                                    .and_then(|i| row.get(i))
                                    .map(|s| clean(s))
                                    .unwrap_or_default()
                            };
                            let spec_id = get(COL_SPEC);
                            if !spec_id.is_empty() {
                                f(CrRow {
                                    spec_id,
                                    cr_number: get(COL_CR),
                                    cr_revision: get(COL_REV).parse::<i32>().ok(),
                                    summary: get(COL_SUBJECT),
                                    meeting: get(COL_MEETING),
                                    category: get(COL_CATEGORY),
                                    release: get(COL_RELEASE),
                                    from_version: get(COL_VCUR),
                                    to_version: get(COL_VNEW),
                                    tdoc: get(COL_TDOC),
                                    tsg_status: get(COL_TSG_STATUS),
                                    wg_status: get(COL_WG_STATUS),
                                });
                                emitted += 1;
                            }
                        }
                    }
                    row.clear();
                    in_row = false;
                }
                // sheetData closing is the file SAYING it is complete. Anything that
                // stops before it is truncated, however well-formed the prefix looks.
                b"sheetData" => closed = true,
                _ => {}
            },
            Ok(Event::Eof) => break,
            Err(e) => return Err(invalid(format!("sheet1.xml: {e}"))),
            _ => {}
        }
        buf.clear();
    }

    if header.is_none() {
        return Err(invalid("CRDB sheet carried no rows at all"));
    }
    if !closed || in_row || in_value > 0 {
        return Err(invalid(format!(
            "sheet1.xml ended before it closed ({emitted} row(s) read, in_row={in_row}, \
             open value element(s)={in_value}) — the export is truncated, and writing a \
             prefix would replace the changelog with part of itself"
        )));
    }
    Ok(emitted)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    /// A minimal but REAL xlsx: the reader must work on the file format, not on a
    /// convenient stand-in, or the test proves nothing about the export it will meet.
    fn xlsx(shared: &[&str], sheet_rows: &str) -> Cursor<Vec<u8>> {
        let mut buf = Vec::new();
        {
            let mut z = zip::ZipWriter::new(Cursor::new(&mut buf));
            let opts: zip::write::FileOptions<()> = zip::write::FileOptions::default();
            let mut sst = String::from("<?xml version=\"1.0\"?><sst>");
            for s in shared {
                sst.push_str(&format!("<si><t>{s}</t></si>"));
            }
            sst.push_str("</sst>");
            z.start_file("xl/sharedStrings.xml", opts).unwrap();
            z.write_all(sst.as_bytes()).unwrap();
            z.start_file("xl/worksheets/sheet1.xml", opts).unwrap();
            z.write_all(
                format!(
                    "<?xml version=\"1.0\"?><worksheet><sheetData>{sheet_rows}</sheetData></worksheet>"
                )
                .as_bytes(),
            )
            .unwrap();
            z.finish().unwrap();
        }
        Cursor::new(buf)
    }

    /// The ten header names, in the order the published export carries them, as
    /// shared strings 0..9. Every test builds its rows against these.
    const HDR: [&str; 12] = [
        COL_SPEC,
        COL_CR,
        COL_REV,
        COL_SUBJECT,
        COL_MEETING,
        COL_CATEGORY,
        COL_RELEASE,
        COL_VCUR,
        COL_VNEW,
        COL_TDOC,
        COL_TSG_STATUS,
        COL_WG_STATUS,
    ];

    fn header_row() -> String {
        let mut s = String::from("<row r=\"1\">");
        for (i, col) in ["A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"]
            .iter()
            .enumerate()
        {
            s.push_str(&format!("<c r=\"{col}1\" t=\"s\"><v>{i}</v></c>"));
        }
        s.push_str("</row>");
        s
    }

    fn collect(src: Cursor<Vec<u8>>) -> Result<Vec<CrRow>> {
        let mut out = Vec::new();
        for_each_cr(src, |r| out.push(r))?;
        Ok(out)
    }

    /// THE DEFECT THIS PINS. The parser that produced the fossil read columns by
    /// POSITION, so row 1 — the header — became a record whose Subject was the word
    /// "Date". 3 026 of those reached the published corpus.
    ///
    /// Binding by name makes it unreachable rather than filtered: row 1 is consumed
    /// AS the header, and no code path can emit it.
    #[test]
    fn the_header_row_is_never_emitted_as_a_record() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.extend_from_slice(&["23.501", "0002", "2", "Using NRF for UPF discovery"]);
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c><c r=\"B2\" t=\"s\"><v>13</v></c><c r=\"C2\" t=\"s\"><v>14</v></c><c r=\"D2\" t=\"s\"><v>15</v></c></row>"
        );
        let got = collect(xlsx(&shared, &rows)).unwrap();
        assert_eq!(got.len(), 1, "the header must not become a record");
        assert_eq!(got[0].spec_id, "23.501");
        assert_eq!(got[0].summary, "Using NRF for UPF discovery");
        assert_eq!(got[0].cr_revision, Some(2));
        assert!(
            !got.iter()
                .any(|r| r.summary == "Subject" || r.spec_id == "Spec number"),
            "a header cell was served as data: {got:?}"
        );
    }

    /// A renamed or re-ordered export must be REFUSED, not read positionally.
    ///
    /// This is the falsification of the fix: if the reader ever falls back to
    /// counting columns, this is the test that stops passing. An export that
    /// dropped "Version-new" and kept its neighbours would otherwise file every
    /// TDoc identifier as a target version.
    #[test]
    fn a_missing_column_is_refused_rather_than_guessed() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared[8] = "Version-latest"; // renamed upstream
        shared.push("23.501");
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c></row>"
        );
        let err = collect(xlsx(&shared, &rows)).unwrap_err();
        let msg = err.to_string();
        assert!(
            msg.contains("Version-new"),
            "must name what is missing: {msg}"
        );
        assert!(
            msg.contains("Version-latest"),
            "must show what it found instead, or the operator cannot fix it: {msg}"
        );
    }

    /// AN EMPTY CELL IS OMITTED FROM AN XLSX, NOT WRITTEN EMPTY. Reading cells in
    /// sibling order therefore shifts every value after the first gap — the same
    /// positional defect as the header row, wearing a different file format.
    ///
    /// Here B through E are absent: Category must stay "F" and must not slide into
    /// cr_number.
    #[test]
    fn an_omitted_cell_does_not_shift_the_columns() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.extend_from_slice(&["23.501", "F", "15.1.0"]);
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c><c r=\"F2\" t=\"s\"><v>13</v></c><c r=\"I2\" t=\"s\"><v>14</v></c></row>"
        );
        let got = collect(xlsx(&shared, &rows)).unwrap();
        assert_eq!(got.len(), 1);
        assert_eq!(got[0].spec_id, "23.501");
        assert_eq!(got[0].category, "F", "column F must be read as Category");
        assert_eq!(got[0].to_version, "15.1.0", "column I must be Version-new");
        assert_eq!(got[0].cr_number, "", "B was absent and must stay absent");
    }

    /// A self-closing <c/> is a styled but EMPTY cell. It must not leave the
    /// previous cell's text behind it — that would put a Subject in a Category.
    #[test]
    fn a_self_closing_cell_carries_nothing_over() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.extend_from_slice(&["23.501", "a subject"]);
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c><c r=\"D2\" t=\"s\"><v>13</v></c><c r=\"F2\" s=\"3\"/><c r=\"I2\" t=\"s\"><v>12</v></c></row>"
        );
        let got = collect(xlsx(&shared, &rows)).unwrap();
        assert_eq!(got[0].summary, "a subject");
        assert_eq!(got[0].category, "", "an empty cell must read empty");
    }

    /// "-" is how the CRDB spells an empty cell. Letting it through would make
    /// model.Change.Citable answer true for a record naming nothing — re-creating
    /// the un-citable rows from the other direction.
    #[test]
    fn a_dash_is_an_empty_cell_not_a_value() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.extend_from_slice(&["23.501", "-", "-"]);
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c><c r=\"B2\" t=\"s\"><v>13</v></c><c r=\"H2\" t=\"s\"><v>14</v></c></row>"
        );
        let got = collect(xlsx(&shared, &rows)).unwrap();
        assert_eq!(got[0].cr_number, "");
        assert_eq!(got[0].from_version, "");
    }

    /// A shared string may be a RUN of fragments when the cell carries formatting.
    /// Taking only the first <t> truncates the subject silently — and a truncated
    /// subject still looks like a perfectly valid record.
    #[test]
    fn a_formatted_subject_is_not_truncated() {
        let sst = format!(
            "<?xml version=\"1.0\"?><sst>{}<si><r><t>Using NRF </t></r><r><t>for UPF discovery</t></r></si><si><t>23.501</t></si></sst>",
            HDR.iter()
                .map(|h| format!("<si><t>{h}</t></si>"))
                .collect::<String>()
        );
        let mut buf = Vec::new();
        {
            let mut z = zip::ZipWriter::new(Cursor::new(&mut buf));
            let opts: zip::write::FileOptions<()> = zip::write::FileOptions::default();
            z.start_file("xl/sharedStrings.xml", opts).unwrap();
            z.write_all(sst.as_bytes()).unwrap();
            z.start_file("xl/worksheets/sheet1.xml", opts).unwrap();
            z.write_all(
                format!(
                    "<?xml version=\"1.0\"?><worksheet><sheetData>{}<row r=\"2\"><c r=\"A2\" t=\"s\"><v>13</v></c><c r=\"D2\" t=\"s\"><v>12</v></c></row></sheetData></worksheet>",
                    header_row()
                )
                .as_bytes(),
            )
            .unwrap();
            z.finish().unwrap();
        }
        let got = collect(Cursor::new(buf)).unwrap();
        assert_eq!(got[0].summary, "Using NRF for UPF discovery");
    }

    /// A row with no Spec number names no document and is dropped: the corpus
    /// cannot file a change against a spec that is not identified.
    #[test]
    fn a_row_without_a_spec_is_dropped() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.push("0002");
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"B2\" t=\"s\"><v>12</v></c></row>"
        );
        assert!(collect(xlsx(&shared, &rows)).unwrap().is_empty());
    }

    #[test]
    fn column_references_decode_past_z() {
        assert_eq!(col_of("A1"), Some(0));
        assert_eq!(col_of("D5"), Some(3));
        assert_eq!(col_of("Z9"), Some(25));
        assert_eq!(col_of("AA1"), Some(26));
        assert_eq!(col_of("AB12"), Some(27));
        assert_eq!(col_of("12"), None);
    }

    #[test]
    fn clean_trims_and_empties_a_dash() {
        assert_eq!(clean("  15.1.0 "), "15.1.0");
        assert_eq!(clean("-"), "");
        assert_eq!(clean(" - "), "");
        assert_eq!(clean("F"), "F");
    }

    /// Like `xlsx`, but the worksheet is written WITHOUT its closing tags — a
    /// structurally valid zip holding a truncated sheet, which is what a
    /// half-finished download or a clipped export actually looks like.
    fn xlsx_unclosed(shared: &[&str], sheet_rows: &str) -> Cursor<Vec<u8>> {
        let mut buf = Vec::new();
        {
            let mut z = zip::ZipWriter::new(Cursor::new(&mut buf));
            let opts: zip::write::FileOptions<()> = zip::write::FileOptions::default();
            let mut sst = String::from("<?xml version=\"1.0\"?><sst>");
            for s in shared {
                sst.push_str(&format!("<si><t>{s}</t></si>"));
            }
            sst.push_str("</sst>");
            z.start_file("xl/sharedStrings.xml", opts).unwrap();
            z.write_all(sst.as_bytes()).unwrap();
            z.start_file("xl/worksheets/sheet1.xml", opts).unwrap();
            z.write_all(
                format!("<?xml version=\"1.0\"?><worksheet><sheetData>{sheet_rows}").as_bytes(),
            )
            .unwrap();
            z.finish().unwrap();
        }
        Cursor::new(buf)
    }

    /// A TRUNCATED EXPORT MUST NOT LOOK LIKE A SHORT ONE.
    ///
    /// quick-xml reports end-of-input as Eof whether or not the document closed, so
    /// a structurally valid zip whose worksheet stops mid-row parses as a
    /// successful PREFIX. Paired with replace_changes — which DELETEs before it
    /// INSERTs — that is the one input that can lose the changelog: a
    /// half-downloaded export would replace 256 471 records with however many rows
    /// arrived, and report success.
    #[test]
    fn a_truncated_sheet_is_an_error_not_a_short_read() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.push("23.501");
        // A complete first data row, then a second that stops inside its cell, and
        // no </sheetData>.
        let rows = format!(
            "{}{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c></row>",
            "<row r=\"3\"><c r=\"A3\" t=\"s\"><v>12"
        );
        let err = collect(xlsx_unclosed(&shared, &rows)).unwrap_err();
        let msg = err.to_string();
        assert!(
            msg.contains("ended before it closed"),
            "a truncated sheet must say so: {msg}"
        );
    }

    /// The well-formed case must still pass, or the check above is only a way of
    /// rejecting every export.
    #[test]
    fn a_complete_sheet_is_still_accepted() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.push("23.501");
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>12</v></c></row>"
        );
        assert_eq!(collect(xlsx(&shared, &rows)).unwrap().len(), 1);
    }

    /// AN UNRESOLVABLE SHARED-STRING INDEX IS A CORRUPT WORKBOOK, NOT AN EMPTY CELL.
    ///
    /// Read as empty, an unresolved Spec number silently drops the record and
    /// replace_changes commits the short set as a complete replacement — the corpus
    /// loses changes and every counter agrees with itself.
    #[test]
    fn an_out_of_range_shared_string_aborts_the_parse() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.push("23.501");
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>999</v></c></row>"
        );
        let err = collect(xlsx(&shared, &rows)).unwrap_err();
        let msg = err.to_string();
        assert!(msg.contains("999"), "must name the index: {msg}");
        assert!(
            msg.contains("shared string"),
            "must say which kind of reference failed: {msg}"
        );
    }

    #[test]
    fn a_non_numeric_shared_string_index_aborts_the_parse() {
        let mut shared: Vec<&str> = HDR.to_vec();
        shared.push("23.501");
        let rows = format!(
            "{}{}",
            header_row(),
            "<row r=\"2\"><c r=\"A2\" t=\"s\"><v>not-a-number</v></c></row>"
        );
        let err = collect(xlsx(&shared, &rows)).unwrap_err();
        assert!(err.to_string().contains("non-numeric"), "{err}");
    }
}
