//! ingest-etsi-changes — give the ETSI half's `changes` table its first writer,
//! from the only change records ETSI publishes: the change-history annex some
//! deliverables print about themselves.
//!
//! WHAT ETSI PUBLISHES, measured on the converted archive (11 822 versions of 5 142
//! deliverables, 2026-09-11) before a line of this was written:
//!
//!   - no change-request database. 3GPP's changelog comes from one export covering
//!     every spec (ingest-crs); ETSI has nothing equivalent to download.
//!   - the Work Programme (portal.etsi.org, public, no login) holds one "scope" text
//!     per work item. It is free prose, one work item per version, and has to be
//!     fetched page by page. It CONFIRMS what this writer reads rather than
//!     supplying it: TS 103 221-1's work item for V1.23.1 (RTS/LI-00310-1) says
//!     "Improvement of references in Table 6.2.1.2-2 TargetIdentifier Formats;
//!     Selective IRI Delivery Provisioning" — CR077 and CR078, which this writer
//!     dates to 1.23.1 — and TS 103 120's for V1.20.1 (RTS/LI-00290) lists eleven
//!     items, which are CR090 to CR101 (no CR092), all dated here to 1.20.1.
//!   - a "Change history" annex in 954 deliverables (3 856 versions), in at least
//!     six layouts. Most are the 3GPP-style table (Date | Meeting | Doc | CR | Rev |
//!     Cat | Subject | Old | New), which `pdftotext -layout` flattens into lines
//!     whose cells belong to different rows: TS 102 127's annex dates SCP-31 to 2018,
//!     and TS 102 412's puts a CR on one line and its resulting version on the next.
//!     Those are not read. A change record that names the wrong transition is worse
//!     than no record.
//!
//! WHAT IS READ. The layout TC LI uses (and a few others that copied it): the
//! Remarks cell names each change request on a line of its own, number, revision
//! and category together, subject after them —
//!
//! ```text
//! CR027r2 (cat B) Generic object mechanism
//! TS102232CR002r1 (cat B) HI1 notifications transport via ETSI TS 102 232
//! CR004, LI(19)P50012r1 (Cat B) CR to support 3GPP 5G work
//! ```
//!
//! One line carries one CR's identity, and nothing else on that line belongs to
//! another row: the Remarks column is the last one, so whatever the flattening put
//! BEFORE the CR (a date, a version) is ignored and everything after it is the CR's.
//!
//! WHAT IS NOT READ FROM THE TABLE: THE VERSION. The version cell is centred
//! vertically in its row, so in the flattened text it lands on the first, middle or
//! last line of the row depending on how many CRs the row holds — "October 2017
//! 1.2.1 Included Change Request:" and "1.5.1 This CR was approved by TC LI#50" are
//! the same table. Pairing a CR with the nearest version would be exactly the
//! positional guess this module refuses.
//!
//! THE VERSION COMES FROM THE ARCHIVE INSTEAD. The corpus holds every published
//! version of each deliverable, and each annex lists every CR up to and including
//! its own version. So a CR that is absent from version P's annex and present in the
//! next version V's annex was included in V. That is a set difference between two
//! published documents, and it needs no layout at all. Three guards keep it true:
//!
//!   1. P must CARRY an annex. An annex that first appears in V lists the CRs of
//!      every earlier version too, and "absent from P" would then only mean "P had
//!      no annex". TS 103 221-1's first annex is in V1.4.1, so CR001-CR003 are not
//!      dated by this writer at all.
//!   2. "Absent" is judged LOOSELY, over the whole text of EVERY earlier version:
//!      any "CR" followed by the number, in any spelling. The strict reader below
//!      missed TS 101 671's "TS10167CR001" (a typo in the spec id, in fourteen
//!      versions) in the prototype, and all 78 CRs of that annex then looked brand
//!      new in V3.1.1 — 78 records dated to the wrong version. Reading presence
//!      loosely and absence strictly can only make this writer SILENT, never wrong.
//!   3. No published version may sit between P and V. The newest version's own
//!      "History" page lists every publication; if one between P and V is missing
//!      from the archive, the CR could belong to it, and the pair is skipped.
//!
//! The result is small, and every record is a line a reader can find in the PDF
//! the answer cites. On this archive (2026-09-11): 943 deliverables print an annex,
//! 30 of them in this layout, and 653 records are dated over 29 deliverables —
//! declining 40 CRs listed in the first version held and 44 whose previous version
//! has no annex.
//!
//! Usage: ingest-etsi-changes --convert <dir> [--db <etsi.duckdb>] [--dry-run]
use anyhow::{Context, Result};
use clap::Parser;
use std::collections::{BTreeMap, HashMap, HashSet};
use store_rs::{ChangeRow, Store};

#[derive(Parser)]
#[command(
    name = "ingest-etsi-changes",
    about = "Rewrite the ETSI changes table from the change-history annexes the deliverables print"
)]
struct Args {
    /// Converted-corpus root holding the ETSI HTML (data/sources/convert-etsi).
    #[arg(long)]
    convert: String,
    /// DuckDB to write into (the ETSI half). Optional with --dry-run.
    #[arg(long, default_value = "")]
    db: String,
    /// Mine and print the records as TSV on stdout, without writing.
    #[arg(long, default_value_t = false)]
    dry_run: bool,
}

/// The provenance stamp `get_changelog` shows beside the records. It names the RULE,
/// not a date: the same archive read by the same rule must produce the same stamp,
/// or the idempotence guard in replace_changes would rewrite 19.5 GB to record a
/// timestamp. Bump the version when the rule changes what it writes.
const SOURCE: &str = "ingest-etsi-changes v1";

/// version_key orders "1.10.1" after "1.9.1".
fn version_key(v: &str) -> (u32, u32, u32) {
    let mut it = v.split('.').map(|p| p.trim().parse::<u32>().unwrap_or(0));
    (
        it.next().unwrap_or(0),
        it.next().unwrap_or(0),
        it.next().unwrap_or(0),
    )
}

/// decode_entities undoes the handful of escapes the converter writes.
fn decode_entities(s: &str) -> String {
    if !s.contains('&') {
        return s.to_string();
    }
    let mut out = String::with_capacity(s.len());
    let mut rest = s;
    while let Some(i) = rest.find('&') {
        out.push_str(&rest[..i]);
        let tail = &rest[i..];
        let Some(end) = tail.find(';').filter(|&e| e <= 10) else {
            out.push('&');
            rest = &tail[1..];
            continue;
        };
        let name = &tail[1..end];
        let ch = match name {
            "amp" => Some('&'),
            "lt" => Some('<'),
            "gt" => Some('>'),
            "quot" => Some('"'),
            "apos" => Some('\''),
            "nbsp" => Some(' '),
            _ if name.starts_with("#x") || name.starts_with("#X") => {
                u32::from_str_radix(&name[2..], 16)
                    .ok()
                    .and_then(char::from_u32)
            }
            _ if name.starts_with('#') => name[1..].parse::<u32>().ok().and_then(char::from_u32),
            _ => None,
        };
        match ch {
            Some(c) => {
                out.push(c);
                rest = &tail[end + 1..];
            }
            None => {
                out.push('&');
                rest = &tail[1..];
            }
        }
    }
    out.push_str(rest);
    out
}

/// text_lines turns the converter's one-element-per-line HTML back into the lines
/// pdftotext printed: tags removed, entities decoded, tabs and runs of blanks
/// collapsed, empty lines dropped.
fn text_lines(html: &str) -> Vec<String> {
    let mut out = Vec::new();
    for raw in html.lines() {
        let mut s = String::with_capacity(raw.len());
        let mut in_tag = false;
        for c in raw.chars() {
            match c {
                '<' => in_tag = true,
                '>' if in_tag => in_tag = false,
                _ if !in_tag => s.push(c),
                _ => {}
            }
        }
        let s = decode_entities(&s);
        let s = s.split_whitespace().collect::<Vec<_>>().join(" ");
        if !s.is_empty() {
            out.push(s);
        }
    }
    out
}

/// is_annex_heading recognises the heading of a change-history annex, in the
/// spellings the archive uses: "Change history", "Change History", "Change Request
/// history", "Document change history", each optionally after "Annex X
/// (informative):".
fn is_annex_heading(line: &str) -> bool {
    let l = line.trim().to_ascii_lowercase();
    let mut s = l.as_str();
    if let Some(rest) = s.strip_prefix("annex ") {
        // "annex h (informative): change history"
        if let Some(i) = rest.find("(informative)") {
            s = rest[i + "(informative)".len()..]
                .trim_start_matches([':', ' '])
                .trim();
        } else {
            return false;
        }
    }
    let s = s.strip_prefix("document ").unwrap_or(s);
    matches!(s, "change history" | "change request history")
}

/// annex_section returns the lines of the LAST change-history annex, up to the
/// "History" page that ends every deliverable. The last, because the table of
/// contents names the annex first.
fn annex_section(lines: &[String]) -> Option<&[String]> {
    let start = lines.iter().rposition(|l| is_annex_heading(l))?;
    let body = &lines[start + 1..];
    let end = body
        .iter()
        .position(|l| l == "History")
        .unwrap_or(body.len());
    Some(&body[..end])
}

/// doc_history returns the versions the deliverable's own "History" page lists
/// ("V1.9.1 July 2021 Publication"), from the LAST "History" line on.
fn doc_history(lines: &[String]) -> Vec<String> {
    let Some(start) = lines.iter().rposition(|l| l == "History") else {
        return Vec::new();
    };
    let mut out = Vec::new();
    for l in &lines[start + 1..] {
        let Some(rest) = l.strip_prefix('V') else {
            continue;
        };
        let v: String = rest
            .chars()
            .take_while(|c| c.is_ascii_digit() || *c == '.')
            .collect();
        if v.split('.').count() == 3 && v.split('.').all(|p| !p.is_empty()) {
            out.push(v);
        }
    }
    out
}

/// One change request as its annex line states it.
#[derive(Clone, Debug, PartialEq)]
struct CrLine {
    number: u32,
    revision: Option<i32>,
    tdoc: String,
    category: char,
    subject: String,
}

/// cr_starts yields each position where "CR" begins a CR reference: not preceded by
/// a letter ("TS102232CR002" is, "SCR12" is not).
fn cr_starts(line: &str, case_insensitive: bool) -> Vec<usize> {
    let b = line.as_bytes();
    let mut out = Vec::new();
    for i in 0..b.len().saturating_sub(1) {
        let (c0, c1) = (b[i], b[i + 1]);
        let hit = if case_insensitive {
            c0.eq_ignore_ascii_case(&b'C') && c1.eq_ignore_ascii_case(&b'R')
        } else {
            c0 == b'C' && c1 == b'R'
        };
        if hit && (i == 0 || !b[i - 1].is_ascii_alphabetic()) {
            out.push(i);
        }
    }
    out
}

/// cr_number_at reads "CR", optional blanks or '#', then 1-4 digits not followed by
/// another digit. Returns the number and the index after it.
fn cr_number_at(line: &str, i: usize) -> Option<(u32, usize)> {
    let b = line.as_bytes();
    let mut j = i + 2;
    while j < b.len() && (b[j] == b' ' || b[j] == b'#') {
        j += 1;
    }
    let ds = j;
    while j < b.len() && b[j].is_ascii_digit() {
        j += 1;
    }
    if j == ds || j - ds > 4 {
        return None;
    }
    Some((line[ds..j].parse().ok()?, j))
}

fn skip_blanks(b: &[u8], mut k: usize) -> usize {
    while k < b.len() && b[k] == b' ' {
        k += 1;
    }
    k
}

/// parse_cr_line is the STRICT reader: the whole identity of one CR on one line —
/// number, optional revision, optional TC LI tdoc, and the category in parentheses —
/// or nothing. The category is what makes a line a CR entry rather than a mention:
/// prose that cites "CR 12" never follows it with "(cat F)".
fn parse_cr_line(line: &str) -> Option<CrLine> {
    let b = line.as_bytes();
    'next: for i in cr_starts(line, false) {
        let Some((number, mut k)) = cr_number_at(line, i) else {
            continue;
        };
        // Revision: "r2", " rev1", " rev 2", " rev.2", " revision 2".
        let mut revision = None;
        {
            let save = k;
            let mut r = skip_blanks(b, k);
            let lower = line[r..].to_ascii_lowercase();
            let prefix = ["revision", "rev.", "rev", "r"]
                .into_iter()
                .find(|p| lower.starts_with(p) && (*p != "r" || r == k));
            if let Some(p) = prefix {
                r = skip_blanks(b, r + p.len());
                let ds = r;
                while r < b.len() && b[r].is_ascii_digit() && r - ds < 2 {
                    r += 1;
                }
                if r > ds && (r >= b.len() || !b[r].is_ascii_digit()) {
                    revision = line[ds..r].parse::<i32>().ok();
                    k = r;
                } else {
                    k = save;
                }
            }
        }
        k = skip_blanks(b, k);
        if k < b.len() && (b[k] == b',' || b[k] == b'.') {
            k = skip_blanks(b, k + 1);
        }
        // TC LI tdoc: "LI(19)P50012r1".
        let mut tdoc = String::new();
        if line[k..].starts_with("LI(") {
            let end = line[k..].find(' ').map_or(b.len(), |e| k + e);
            tdoc = line[k..end].trim_end_matches(',').to_string();
            k = skip_blanks(b, end);
        }
        // Category: "(cat F)", "(Cat. F)", "(category D)".
        if k >= b.len() || b[k] != b'(' {
            continue 'next;
        }
        k += 1;
        let lower = line[k..].to_ascii_lowercase();
        let word = if lower.starts_with("category") {
            8
        } else if lower.starts_with("cat") {
            3
        } else {
            continue 'next;
        };
        k += word;
        if k < b.len() && b[k] == b'.' {
            k += 1;
        }
        k = skip_blanks(b, k);
        if k + 1 >= b.len() || !b[k].is_ascii_alphabetic() || b[k + 1] != b')' {
            continue 'next;
        }
        let category = (b[k] as char).to_ascii_uppercase();
        if !('A'..='F').contains(&category) {
            continue 'next;
        }
        let mut subject = line[k + 2..].trim();
        if subject.len() >= 3 && subject[..3].eq_ignore_ascii_case("on ") {
            subject = subject[3..].trim_start();
        }
        return Some(CrLine {
            number,
            revision,
            tdoc,
            category,
            subject: subject.to_string(),
        });
    }
    None
}

/// loose_numbers is the SUPPRESSOR: every CR number a document mentions anywhere,
/// in any spelling, category or not. A number in this set for any earlier version
/// means "not new here" — see guard 2 in the module docs.
fn loose_numbers(lines: &[String]) -> HashSet<u32> {
    let mut out = HashSet::new();
    for l in lines {
        for i in cr_starts(l, true) {
            if let Some((n, _)) = cr_number_at(l, i) {
                out.insert(n);
            }
        }
    }
    out
}

/// What one published version contributes.
struct VersionScan {
    version: String,
    has_annex: bool,
    strict: BTreeMap<u32, CrLine>,
    loose: HashSet<u32>,
    history: Vec<String>,
}

fn scan_version(version: &str, html: &str) -> VersionScan {
    let lines = text_lines(html);
    let annex = annex_section(&lines);
    let mut strict = BTreeMap::new();
    if let Some(sec) = annex {
        for l in sec {
            if let Some(cr) = parse_cr_line(l) {
                strict.entry(cr.number).or_insert(cr);
            }
        }
    }
    VersionScan {
        version: version.to_string(),
        has_annex: annex.is_some(),
        strict,
        loose: loose_numbers(&lines),
        history: doc_history(&lines),
    }
}

/// Why a first appearance was not written — counted, so the run says what it
/// declined as loudly as what it wrote.
#[derive(Default, Debug)]
struct Declined {
    first_version: usize,
    prev_without_annex: usize,
    version_gap: usize,
}

/// date_deliverable applies the first-appearance rule and its three guards to the
/// versions of ONE deliverable.
fn date_deliverable(
    spec_id: &str,
    mut versions: Vec<VersionScan>,
    declined: &mut Declined,
) -> Vec<ChangeRow> {
    versions.sort_by_key(|v| version_key(&v.version));
    let held: HashSet<&str> = versions.iter().map(|v| v.version.as_str()).collect();
    // The newest version's History page lists every publication before it.
    let published: Vec<String> = versions
        .last()
        .map(|v| v.history.clone())
        .unwrap_or_default();

    let mut rows = Vec::new();
    let mut seen: HashSet<u32> = HashSet::new();
    for (i, v) in versions.iter().enumerate() {
        if i == 0 {
            declined.first_version += v.strict.keys().filter(|n| !seen.contains(n)).count();
        } else {
            let p = &versions[i - 1];
            let (pk, vk) = (version_key(&p.version), version_key(&v.version));
            let gap = published.iter().any(|h| {
                let hk = version_key(h);
                hk > pk && hk < vk && !held.contains(h.as_str())
            });
            for (n, cr) in &v.strict {
                if seen.contains(n) {
                    continue;
                }
                if !p.has_annex {
                    declined.prev_without_annex += 1;
                    continue;
                }
                if gap {
                    declined.version_gap += 1;
                    continue;
                }
                rows.push(ChangeRow {
                    spec_id: spec_id.to_string(),
                    cr_number: format!("CR{n:03}"),
                    cr_revision: cr.revision,
                    summary: cr.subject.clone(),
                    // The approval meeting is printed in the same cell, but BEFORE
                    // the CR list in some annexes and AFTER it in others — the
                    // positional guess this writer exists to avoid.
                    meeting: String::new(),
                    category: cr.category.to_string(),
                    from_version: p.version.clone(),
                    to_version: v.version.clone(),
                    tdoc: cr.tdoc.clone(),
                });
            }
        }
        seen.extend(v.loose.iter().copied());
        seen.extend(v.strict.keys().copied());
    }
    rows
}

fn collect_html(root: &str) -> Result<Vec<String>> {
    let mut out = Vec::new();
    let mut stack = vec![std::path::PathBuf::from(root)];
    while let Some(dir) = stack.pop() {
        let rd = std::fs::read_dir(&dir).with_context(|| format!("read_dir {}", dir.display()))?;
        for e in rd {
            let p = e?.path();
            if p.is_dir() {
                stack.push(p);
            } else if p.extension().and_then(|x| x.to_str()) == Some("html") {
                if let Some(s) = p.to_str() {
                    out.push(s.to_string());
                }
            }
        }
    }
    out.sort();
    Ok(out)
}

/// corpus_versions reads the (spec_id, version) pairs the corpus holds. A record is
/// written only when BOTH its versions are among them: a version the corpus does
/// not hold has no text behind it here, and cite-or-silent does not stretch to it.
/// A scan error is propagated — a shorter list would silently drop records.
fn corpus_versions(store: &Store) -> Result<HashSet<(String, String)>> {
    let mut st = store
        .raw()
        .prepare("SELECT spec_id, version FROM spec_versions")?;
    let it = st.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?;
    let mut out = HashSet::new();
    for r in it {
        out.insert(r.context("read the versions the changelog is filtered against")?);
    }
    Ok(out)
}

fn main() -> Result<()> {
    let args = Args::parse();
    if args.db.is_empty() && !args.dry_run {
        anyhow::bail!("--db is required unless --dry-run");
    }

    let files = collect_html(&args.convert)?;
    eprintln!("ingest-etsi-changes: {} converted version(s)", files.len());

    // Every version of every deliverable: the rule compares consecutive versions.
    let mut by_spec: HashMap<String, Vec<VersionScan>> = HashMap::new();
    for (i, f) in files.iter().enumerate() {
        let html = parse3gpp::html_bytes::read_html(f).with_context(|| format!("read {f}"))?;
        let Some(meta) = parse3gpp::etsi::parse_etsi_meta(&html) else {
            continue; // no provenance header: not an ETSI deliverable
        };
        let scan = scan_version(&meta.version, &html);
        by_spec.entry(meta.spec_id).or_default().push(scan);
        if (i + 1) % 2000 == 0 {
            eprintln!(
                "ingest-etsi-changes: {}/{} version(s) read",
                i + 1,
                files.len()
            );
        }
    }

    let with_annex = by_spec
        .values()
        .filter(|vs| vs.iter().any(|v| v.has_annex))
        .count();
    let with_lines = by_spec
        .values()
        .filter(|vs| vs.iter().any(|v| !v.strict.is_empty()))
        .count();

    let mut declined = Declined::default();
    let mut specs: Vec<String> = by_spec.keys().cloned().collect();
    specs.sort();
    let mut rows = Vec::new();
    for s in specs {
        let vs = by_spec.remove(&s).unwrap_or_default();
        rows.extend(date_deliverable(&s, vs, &mut declined));
    }

    // Cite-or-silent against the corpus, when there is one to write into.
    let store = if args.db.is_empty() {
        None
    } else {
        Some(Store::open_rw(&args.db)?)
    };
    let mut uncitable = String::from("not checked (no --db)");
    if let Some(st) = &store {
        let held = corpus_versions(st)?;
        let before = rows.len();
        rows.retain(|r| {
            held.contains(&(r.spec_id.clone(), r.from_version.clone()))
                && held.contains(&(r.spec_id.clone(), r.to_version.clone()))
        });
        uncitable = (before - rows.len()).to_string();
    }

    let per_spec: BTreeMap<&str, usize> = rows.iter().fold(BTreeMap::new(), |mut m, r| {
        *m.entry(r.spec_id.as_str()).or_insert(0) += 1;
        m
    });
    eprintln!(
        "ingest-etsi-changes: {with_annex} deliverable(s) print a change-history annex, {with_lines} of them \
         in the one-CR-per-line layout; {} record(s) dated over {} deliverable(s). Declined: {} listed in \
         the first version held, {} whose previous version carries no annex, {} across a missing \
         publication, {uncitable} naming a version the corpus does not hold",
        rows.len(),
        per_spec.len(),
        declined.first_version,
        declined.prev_without_annex,
        declined.version_gap
    );
    for (s, n) in &per_spec {
        eprintln!("ingest-etsi-changes:   {s}: {n}");
    }

    if args.dry_run {
        println!(
            "spec_id\tcr_number\tcr_revision\tcategory\tfrom_version\tto_version\ttdoc\tsummary"
        );
        for r in &rows {
            println!(
                "{}\t{}\t{}\t{}\t{}\t{}\t{}\t{}",
                r.spec_id,
                r.cr_number,
                r.cr_revision.map_or(String::new(), |v| v.to_string()),
                r.category,
                r.from_version,
                r.to_version,
                r.tdoc,
                r.summary
            );
        }
        return Ok(());
    }

    let Some(store) = store else {
        return Ok(());
    };
    match write_changelog(&store, &rows)? {
        Outcome::LeftEmpty { pairs } => eprintln!(
            "ingest-etsi-changes: no record to date — {pairs} deliverable(s) hold two versions or more, and the changelog was already empty: corpus untouched"
        ),
        Outcome::Untouched { written } => eprintln!(
            "ingest-etsi-changes: the changelog already carries these {written} row(s) from {SOURCE}, corpus untouched"
        ),
        Outcome::Written { written, skipped } => eprintln!(
            "ingest-etsi-changes: {written} written, {skipped} skipped (deliverable not in this corpus)"
        ),
    }
    Ok(())
}

/// What write_changelog did to the corpus.
#[derive(Debug, PartialEq)]
enum Outcome {
    /// Nothing to write and nothing to lose: the file was not opened for a write.
    LeftEmpty {
        pairs: usize,
    },
    /// The table already holds exactly these rows: the file was not touched.
    Untouched {
        written: usize,
    },
    Written {
        written: usize,
        skipped: usize,
    },
}

/// write_changelog puts the mined rows into the corpus, or declines.
///
/// AN EMPTY RESULT MEANS TWO THINGS, AND ONLY ONE OF THEM MAY PASS (Qodo, #335).
/// The rule dates a CR by comparing two consecutive versions, so an archive that
/// holds ONE version per deliverable — a fresh `li-suite` or `all` build fetches
/// only the newest — has nothing to compare and yields nothing, correctly. The
/// first draft bailed on every empty result and would have failed enrich-etsi on
/// exactly that supported build. What an empty result must never do is ERASE a
/// changelog the corpus already carries: that is a broken reader or a thinned
/// tree, and replace_changes would DELETE every row. So: empty and empty passes,
/// untouched; empty against a non-empty table refuses.
fn write_changelog(store: &Store, rows: &[ChangeRow]) -> Result<Outcome> {
    if rows.is_empty() {
        let held: i64 = store
            .raw()
            .query_row("SELECT count(*) FROM changes", [], |r| r.get(0))
            .context("count the changelog the corpus already carries")?;
        if held > 0 {
            anyhow::bail!(
                "no change record was read, and the corpus carries {held} — the annex reader or the converted tree is broken; refusing to empty the changelog"
            );
        }
        let pairs: i64 = store
            .raw()
            .query_row(
                "SELECT count(*) FROM (SELECT spec_id FROM spec_versions GROUP BY spec_id HAVING count(*) > 1)",
                [],
                |r| r.get(0),
            )
            .context("count the deliverables held at two versions or more")?;
        return Ok(Outcome::LeftEmpty {
            pairs: pairs as usize,
        });
    }
    let (written, skipped, changed) = store.replace_changes(rows, SOURCE)?;
    if !changed {
        // No checkpoint: reaching here means the corpus must not move, and a
        // checkpoint is a write. Same line, same meaning as ingest-crs.
        return Ok(Outcome::Untouched { written });
    }
    store.checkpoint()?;
    Ok(Outcome::Written { written, skipped })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn html(lines: &[&str]) -> String {
        lines
            .iter()
            .map(|l| format!("<p>{l}</p>"))
            .collect::<Vec<_>>()
            .join("\n")
    }

    /// Every spelling below is verbatim from the converted archive.
    #[test]
    fn the_strict_reader_takes_each_spelling_the_archive_uses() {
        let cases: &[(&str, u32, Option<i32>, &str, char, &str)] = &[
            ("CR027r2 (cat B) Generic object mechanism", 27, Some(2), "", 'B', "Generic object mechanism"),
            ("CR010 (cat C) Corrections after implementation", 10, None, "", 'C', "Corrections after implementation"),
            // The date the flattening glued in front belongs to another column.
            ("February 2018 TS103221-1CR001r1 (cat F) Warning and Faults Reporting", 1, Some(1), "", 'F', "Warning and Faults Reporting"),
            ("TS 102 232-01CR031 (Cat B) Expansion of CIN counting mechanisms for future", 31, None, "", 'B', "Expansion of CIN counting mechanisms for future"),
            ("TS10167CR001 (category D) on Removal of reference to TR 101 876", 1, None, "", 'D', "Removal of reference to TR 101 876"),
            ("TS101671CR002 rev1 (category D) on Example on missing information", 2, Some(1), "", 'D', "Example on missing information"),
            ("TS 101 671 CR014 rev 2 (cat F) on annex E, Coding of the Calling Party Number", 14, Some(2), "", 'F', "annex E, Coding of the Calling Party Number"),
            ("CR004, LI(19)P50012r1 (Cat B) CR to support 3GPP 5G work", 4, None, "LI(19)P50012r1", 'B', "CR to support 3GPP 5G work"),
            ("October 2019 1.4.1 CR003. LI(16)P41021r2 (Cat B) Addition of Task IsEmergency flag", 3, None, "LI(16)P41021r2", 'B', "Addition of Task IsEmergency flag"),
        ];
        for (line, n, rev, tdoc, cat, subj) in cases {
            let got = parse_cr_line(line).unwrap_or_else(|| panic!("refused: {line}"));
            assert_eq!(
                (
                    got.number,
                    got.revision,
                    got.tdoc.as_str(),
                    got.category,
                    got.subject.as_str()
                ),
                (*n, *rev, *tdoc, *cat, *subj),
                "{line}"
            );
        }
    }

    /// Lines that sit in the same annexes and are NOT a CR entry. The last three are
    /// the 3GPP-style layout, whose CR number is a bare cell this reader must not
    /// pretend to place.
    #[test]
    fn the_strict_reader_refuses_everything_else() {
        for line in [
            "These CRs were approved by TC LI#58-e (18-22 October 2021)",
            "Included Change Requests:",
            "SCR12 (cat B) not a change request",
            "see CR 12 for the background",
            "CR12345 (cat B) five digits is not a CR number",
            "2007-05 SCP#30 SCP-070133 069 1 7.8.0",
            "2007-10 SCP#33 SCP-070426 071 - B Modification of tags for RFM with script 7.12.0 8.0.0",
            "SCP-22 001 2 B Requirement for Secure channel between the",
        ] {
            assert_eq!(parse_cr_line(line), None, "{line}");
        }
    }

    #[test]
    fn annex_heading_spellings() {
        for h in [
            "Change history",
            "Change History",
            "Change Request history",
            "Document change history",
            "Annex H (informative): Change history",
        ] {
            assert!(is_annex_heading(h), "{h}");
        }
        for h in [
            "History",
            "Change history of the X1 interface",
            "Annex H (normative): XSD",
        ] {
            assert!(!is_annex_heading(h), "{h}");
        }
    }

    #[test]
    fn lines_lose_their_markup_and_keep_their_text() {
        let got =
            text_lines("<p>CR001 (cat F) A &amp; B&#39;s</p>\n<h1>1.5.1\tThis CR</h1>\n<p></p>");
        assert_eq!(
            got,
            vec![
                "CR001 (cat F) A & B's".to_string(),
                "1.5.1 This CR".to_string()
            ]
        );
    }

    /// A deliverable's versions, each with an annex listing CRs one per line.
    fn deliverable(versions: &[(&str, &[&str])]) -> Vec<VersionScan> {
        versions
            .iter()
            .map(|(v, body)| scan_version(v, &html(body)))
            .collect()
    }

    /// THE RULE: a CR absent from P's annex and present in V's was included in V.
    /// Versions compare by NUMBER: 1.10.1 follows 1.9.1.
    #[test]
    fn a_cr_is_dated_to_the_first_version_whose_annex_lists_it() {
        let vs = deliverable(&[
            (
                "1.10.1",
                &[
                    "Change history",
                    "CR001 (cat F) one",
                    "CR002r1 (cat B) two",
                    "History",
                ],
            ),
            ("1.9.1", &["Change history", "CR001 (cat F) one", "History"]),
            ("1.8.1", &["Change history", "First publication", "History"]),
        ]);
        let mut d = Declined::default();
        let rows = date_deliverable("ETSI TS 103 221-1", vs, &mut d);
        let got: Vec<_> = rows
            .iter()
            .map(|r| {
                (
                    r.cr_number.as_str(),
                    r.from_version.as_str(),
                    r.to_version.as_str(),
                )
            })
            .collect();
        assert_eq!(
            got,
            vec![("CR001", "1.8.1", "1.9.1"), ("CR002", "1.9.1", "1.10.1")]
        );
        assert_eq!(rows[1].cr_revision, Some(1));
        assert_eq!(rows[1].category, "B");
        assert_eq!(
            rows[1].meeting, "",
            "the meeting is not placed positionally"
        );
    }

    /// GUARD 1. TS 103 221-1's first annex is in V1.4.1 and lists CR001-CR003,
    /// which were included in 1.2.1-1.4.1. "Absent from 1.3.1" only means 1.3.1 had
    /// no annex, so none of them may be dated.
    #[test]
    fn an_annex_that_first_appears_dates_nothing() {
        let vs = deliverable(&[
            ("1.3.1", &["Scope", "History"]),
            (
                "1.4.1",
                &[
                    "Change history",
                    "TS103221-1CR001r1 (cat F) a",
                    "TS103221-1CR003r3 (cat B) Support for 5G",
                    "History",
                ],
            ),
            (
                "1.5.1",
                &[
                    "Change history",
                    "TS103221-1CR001r1 (cat F) a",
                    "TS103221-1CR003r3 (cat B) Support for 5G",
                    "CR004r1 (cat F) b",
                    "History",
                ],
            ),
        ]);
        let mut d = Declined::default();
        let rows = date_deliverable("ETSI TS 103 221-1", vs, &mut d);
        let got: Vec<_> = rows.iter().map(|r| r.cr_number.as_str()).collect();
        assert_eq!(
            got,
            vec!["CR004"],
            "only the CR new in 1.5.1 against an annex that existed in 1.4.1"
        );
        assert_eq!(d.prev_without_annex, 2);
    }

    /// GUARD 2, the prototype's real failure. TS 101 671 printed "TS10167CR001"
    /// through fourteen versions, then "TS 101 671 CR001" from V3.1.1. A reader
    /// that misses the old spelling sees 78 "new" CRs in V3.1.1. Here the old line
    /// is also one the strict reader refuses (the category wrapped to the next
    /// line) — and the loose suppressor must still hold the CR back.
    #[test]
    fn a_cr_the_strict_reader_missed_earlier_is_not_new_later() {
        let vs = deliverable(&[
            (
                "2.15.1",
                &[
                    "Change Request history",
                    "TS10167CR005 rev1 (category F) on No additional",
                    "TS10167CR006 rev1",
                    "(category F) on Version indication",
                    "History",
                ],
            ),
            (
                "3.1.1",
                &[
                    "Change Request history",
                    "TS 101 671 CR005 rev1 (category F) on No additional",
                    "TS 101 671 CR006 rev1 (category F) on Version indication",
                    "CR101 (cat B) genuinely new",
                    "History",
                ],
            ),
        ]);
        let mut d = Declined::default();
        let rows = date_deliverable("ETSI TS 101 671", vs, &mut d);
        let got: Vec<_> = rows.iter().map(|r| r.cr_number.as_str()).collect();
        assert_eq!(
            got,
            vec!["CR101"],
            "CR006 appears in 2.15.1 in a spelling only the loose reader sees"
        );
    }

    /// And a CR that DISAPPEARS from one annex and comes back (TS 102 232-1 dropped
    /// CR030 between V3.7.1 and V3.8.1) is not re-dated when it comes back.
    #[test]
    fn a_cr_that_drops_out_and_returns_is_dated_once() {
        let vs = deliverable(&[
            ("3.6.1", &["Change history", "First publication", "History"]),
            (
                "3.7.1",
                &[
                    "Change history",
                    "CR030 (Cat D) CIN use clarification",
                    "History",
                ],
            ),
            ("3.8.1", &["Change history", "History"]),
            (
                "3.9.1",
                &[
                    "Change history",
                    "CR030 (Cat D) CIN use clarification",
                    "History",
                ],
            ),
        ]);
        let mut d = Declined::default();
        let rows = date_deliverable("ETSI TS 102 232-1", vs, &mut d);
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].to_version, "3.7.1");
    }

    /// GUARD 3. The newest version's History page lists 1.2.1, which the archive
    /// does not hold: a CR new in 1.3.1 against 1.1.1 may belong to 1.2.1.
    #[test]
    fn a_missing_publication_between_two_versions_dates_nothing_across_it() {
        let vs = deliverable(&[
            ("1.1.1", &["Change history", "First publication", "History"]),
            (
                "1.3.1",
                &[
                    "Change history",
                    "CR001 (cat F) a",
                    "History",
                    "V1.1.1 May 2020 Publication",
                    "V1.2.1 June 2020 Publication",
                    "V1.3.1 July 2020 Publication",
                ],
            ),
        ]);
        let mut d = Declined::default();
        let rows = date_deliverable("ETSI TS 103 999", vs, &mut d);
        assert!(
            rows.is_empty(),
            "{:?}",
            rows.iter().map(|r| &r.to_version).collect::<Vec<_>>()
        );
        assert_eq!(d.version_gap, 1);
    }

    fn row(spec: &str, n: &str) -> ChangeRow {
        ChangeRow {
            spec_id: spec.into(),
            cr_number: n.into(),
            cr_revision: None,
            summary: "s".into(),
            meeting: String::new(),
            category: "F".into(),
            from_version: "1.1.1".into(),
            to_version: "1.2.1".into(),
            tdoc: String::new(),
        }
    }

    fn changes_held(st: &Store) -> i64 {
        st.raw()
            .query_row("SELECT count(*) FROM changes", [], |r| r.get(0))
            .unwrap()
    }

    /// A NEWEST-ONLY ARCHIVE IS A SUPPORTED BUILD (Qodo, #335): `li-suite` and
    /// `all` fetch one version per deliverable, the rule has no pair to compare,
    /// and the pass must succeed without touching the corpus.
    #[test]
    fn an_empty_result_on_an_empty_changelog_passes_untouched() {
        let st = Store::in_memory().unwrap();
        assert_eq!(
            write_changelog(&st, &[]).unwrap(),
            Outcome::LeftEmpty { pairs: 0 }
        );
        assert_eq!(changes_held(&st), 0);
    }

    /// ...and the guard it replaced still stands where it matters: an empty
    /// result must never erase a changelog the corpus already carries.
    #[test]
    fn an_empty_result_never_erases_a_changelog() {
        let st = Store::in_memory().unwrap();
        st.raw()
            .execute_batch("INSERT INTO specs(spec_id) VALUES ('ETSI TS 103 221-1')")
            .unwrap();
        let first = write_changelog(&st, &[row("ETSI TS 103 221-1", "CR001")]).unwrap();
        assert_eq!(
            first,
            Outcome::Written {
                written: 1,
                skipped: 0
            }
        );
        assert!(
            write_changelog(&st, &[]).is_err(),
            "an empty pass must refuse"
        );
        assert_eq!(changes_held(&st), 1, "and leave the changelog where it was");
        assert_eq!(
            write_changelog(&st, &[row("ETSI TS 103 221-1", "CR001")]).unwrap(),
            Outcome::Untouched { written: 1 }
        );
    }

    #[test]
    fn the_same_archive_yields_the_same_rows() {
        let mk = || {
            deliverable(&[
                ("1.1.1", &["Change history", "First publication", "History"]),
                (
                    "1.2.1",
                    &[
                        "Change history",
                        "CR002 (cat B) b",
                        "CR001 (cat F) a",
                        "History",
                    ],
                ),
            ])
        };
        let a = date_deliverable("ETSI TS 1", mk(), &mut Declined::default());
        let b = date_deliverable(
            "ETSI TS 1",
            mk().into_iter().rev().collect(),
            &mut Declined::default(),
        );
        let key = |rs: &[ChangeRow]| {
            rs.iter()
                .map(|r| format!("{}|{}|{}", r.cr_number, r.from_version, r.to_version))
                .collect::<Vec<_>>()
        };
        assert_eq!(key(&a), key(&b));
        assert_eq!(key(&a), vec!["CR001|1.1.1|1.2.1", "CR002|1.1.1|1.2.1"]);
    }
}
