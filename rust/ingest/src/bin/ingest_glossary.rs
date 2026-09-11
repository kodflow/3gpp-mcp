//! ingest-glossary — mine the ETSI half's vocabulary into the acronyms table.
//!
//! WHY A SEPARATE PASS. The 3GPP glossary comes from ONE spec (TS 21.905) and is
//! extracted during that spec's ingest. ETSI has no vocabulary deliverable: every
//! deliverable carries its own "Abbreviations" clause, so the vocabulary is spread
//! across the whole archive. Following ingest-catalog / ingest-li / ingest-openapi,
//! that is an enrichment pass over the converted HTML, not a change to ingest — it
//! writes ONLY acronyms, so it can be re-run at any time without touching a clause.
//!
//! WHAT IT MINES. parse3gpp::glossary::extract_acronyms is already generic: it finds
//! the clause whose heading contains "abbreviation" and reads
//! "ABBR<TAB|2+ spaces>Expansion" lines until the region ends. It was gated to
//! 21.905 only because that is where the 3GPP vocabulary lives.
//!
//! Measured on TS 102 221 v18.4.0 before writing this: 122 entries from clause 3.3
//! "Abbreviations" and 12 from 3.2 "Symbols" (Di = Baud rate adjustment integer,
//! Fi = clock rate conversion factor — vocabulary too), and ZERO from the prose of
//! 3.1 "Definitions". The separator requirement is what keeps prose out, and it
//! holds on pdftotext -layout output.
//!
//! ONE VERSION PER DELIVERABLE. The archive holds 11 822 versions of 5 142
//! deliverables and their abbreviation lists barely move, so this reads the NEWEST
//! version of each: the current meaning of a term, at a fraction of the work.
//!
//! WHAT IT COUNTS, AND WHY THE COUNT IS THE PRODUCT. 938 of the 3 042 terms in the
//! ETSI half carry more than one expansion, and ETSI states no precedence rule to
//! choose between them — so before this pass counted agreement, the first row a
//! caller read was the alphabetical one: MSC as "Main Service Channel", IMS as "IP
//! Multimdia Subsystem" (a typo), TS as a sentence about SimulCrypt. See `Tally`.
//!
//! Usage: ingest-glossary --convert <dir> --db <db>
use anyhow::{Context, Result};
use clap::Parser;
use std::collections::HashMap;

#[derive(Parser)]
#[command(
    name = "ingest-glossary",
    about = "Mine every ETSI deliverable's Abbreviations clause into the acronyms table"
)]
struct Args {
    /// Converted-corpus root holding the ETSI HTML (data/sources/convert-etsi).
    #[arg(long)]
    convert: String,
    /// DuckDB to write the acronyms into (the ETSI half).
    #[arg(long)]
    db: String,
}

// --source-series IS GONE, and its absence is the fix rather than a tidy-up.
//
// It stamped the constant "etsi" on all 4 941 rows: a value that names no
// document, so nothing could be cited and — because Store.ResolveTerm ranks on
// what source_series says — nothing could be ranked either. Every ETSI row landed
// in the same bucket and the tie-break decided the answer alphabetically. Each row
// now carries the deliverable that declares it ("ETSI TS 103 221-1"), which still
// tells the halves apart, since every ETSI id begins with "ETSI ".

/// version_key turns "18.4.0" into a comparable tuple, so 18.10.0 sorts after
/// 18.9.0. A string compare puts them the other way round, which is the same defect
/// versionOrderSQL exists to avoid on the serve side.
fn version_key(v: &str) -> (u32, u32, u32) {
    let mut it = v.split('.').map(|p| p.parse::<u32>().unwrap_or(0));
    (
        it.next().unwrap_or(0),
        it.next().unwrap_or(0),
        it.next().unwrap_or(0),
    )
}

/// newest_per_deliverable keys on the file name up to "_v", which is exactly the
/// deliverable identity the converter writes (TS_102_221_v18.4.0.html).
fn newest_per_deliverable(files: Vec<String>) -> Vec<String> {
    let mut best: HashMap<String, (u32, u32, u32, String)> = HashMap::new();
    for f in files {
        let name = std::path::Path::new(&f)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or_default()
            .to_string();
        let Some(idx) = name.rfind("_v") else {
            continue;
        };
        let key = name[..idx].to_string();
        let ver = name[idx + 2..].trim_end_matches(".html").to_string();
        let k = version_key(&ver);
        match best.get(&key) {
            Some((a, b, c, _)) if (*a, *b, *c) >= k => {}
            _ => {
                best.insert(key, (k.0, k.1, k.2, f));
            }
        }
    }
    let mut out: Vec<String> = best.into_values().map(|(_, _, _, f)| f).collect();
    out.sort();
    out
}

/// initials_match asks whether an expansion can actually BE what the term
/// abbreviates: the term's letters must appear, in order, as word initials of the
/// expansion. "IMSI" / "International Mobile Subscriber Identity" passes; "IMSI" /
/// "International Organization for Standardization" does not.
///
/// WHY THIS GUARD EXISTS, measured before it did. extract_acronyms pairs a line's
/// first token with the rest of the line, which is exactly right on the 3GPP side,
/// where the abbreviation list survives .doc conversion as a table. ETSI ships PDFs,
/// and `pdftotext -layout` prints a tall cell's expansion ABOVE its term:
///
/// ```text
/// AND   Boolean "and"
/// Conditional requirement (to be observed if the relevant conditions apply)
/// C     Digital Subscriber Signalling System No. one
/// DSS1  Information Elements Received
/// ```
///
/// From there every line pairs term N with expansion N+1, for the rest of the list.
/// Run without this guard over the whole ETSI corpus: 8 995 rows, of which 3 264
/// (36,3 %) carried an expansion belonging to another term — including
/// "IMSI = International Organization for Standardization". resolve_term would have
/// answered those with a straight face. A wrong definition is worse than a missing
/// one: it is an answer the caller cannot check.
///
/// The guard is conservative in the safe direction. It also drops syllabic
/// abbreviations that are genuinely correct — CAPEX/Capital Expenditure,
/// N/A/not supported — so the vocabulary it keeps is smaller than the truth and
/// never wider than it.
fn initials_match(term: &str, expansion: &str) -> bool {
    let letters: Vec<char> = term
        .chars()
        .filter(|c| c.is_alphabetic())
        .flat_map(|c| c.to_uppercase())
        .collect();
    if letters.is_empty() {
        return false;
    }
    let mut i = 0;
    for word in expansion.split(|c: char| !c.is_alphanumeric()) {
        let Some(first) = word.chars().next() else {
            continue;
        };
        if i < letters.len() && first.to_uppercase().next() == Some(letters[i]) {
            i += 1;
        }
    }
    i == letters.len()
}

/// A whole file whose columns are shifted produces rows that are individually
/// wrong, and a handful may still pass initials_match by coincidence. So a file has
/// to look self-consistent as a WHOLE before any of its rows are trusted: below this
/// share of passing candidates, the pairing itself is suspect and the file is
/// dropped entirely.
const MIN_FILE_CONSISTENCY: f64 = 0.6;

/// How long one deliverable may take before it names itself in the log. See the
/// loop in main() for the two stalls that made a per-file signal necessary.
const SLOW_FILE: std::time::Duration = std::time::Duration::from_secs(10);

fn collect_html(root: &str) -> Result<Vec<String>> {
    let mut out = Vec::new();
    let mut stack = vec![std::path::PathBuf::from(root)];
    while let Some(dir) = stack.pop() {
        let Ok(rd) = std::fs::read_dir(&dir) else {
            continue;
        };
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
    Ok(out)
}

/// What the corpus, taken as a whole, says about one (term, expansion).
///
/// AGREEMENT IS THE ONLY AUTHORITY ETSI OFFERS. 3GPP ranks a term by WHO declares
/// it — TS 23.501 §3.2: "An abbreviation defined in the present document takes
/// precedence over the definition of the same abbreviation, if any, in TR 21.905"
/// — and Store.ResolveTerm implements exactly that. ETSI publishes no TR 21.905
/// and states no precedence: 5 142 deliverables each declare their own vocabulary
/// and none outranks another in general. So the honest signal is how many of them
/// say the same thing, and it has to be counted HERE, because the primary key
/// keeps one row per expansion — after the write, the corpus can no longer tell
/// forty declarations from one.
struct Consensus {
    /// How many DELIVERABLES declare this exact expansion.
    declared_by: i64,
    /// The deliverable cited on the row, and its version.
    ///
    /// The lexicographically smallest declaring id, which is a REPRODUCIBILITY
    /// rule and not a claim of authority: the same corpus must yield the same row
    /// on every run, and "whichever file the walker reached last" does not. The
    /// count is what says how many others agree.
    cite_spec: String,
    cite_version: String,
}

/// Tally folds each deliverable's kept acronyms into the corpus-wide consensus.
///
/// A BTreeMap, so the write order is the corpus's own order rather than a hash
/// seed's: an idempotent pass has to produce the same rows in the same sequence,
/// or "nothing changed" cannot be told from "everything moved".
#[derive(Default)]
struct Tally {
    rows: std::collections::BTreeMap<(String, String), Consensus>,
}

impl Tally {
    /// add_file folds ONE deliverable in.
    ///
    /// DISTINCT PER DELIVERABLE. An Abbreviations clause can list the same pair
    /// twice — ETSI TS 102 221 lists TC under both its Symbols and Abbreviations
    /// headings — and counting both would let one document outvote two.
    fn add_file(&mut self, spec_id: &str, version: &str, acs: &[(String, String)]) {
        let mut seen = std::collections::HashSet::new();
        for (term, expansion) in acs {
            if !seen.insert((term.as_str(), expansion.as_str())) {
                continue;
            }
            let e = self
                .rows
                .entry((term.clone(), expansion.clone()))
                .or_insert_with(|| Consensus {
                    declared_by: 0,
                    cite_spec: spec_id.to_string(),
                    cite_version: version.to_string(),
                });
            e.declared_by += 1;
            if spec_id < e.cite_spec.as_str() {
                e.cite_spec = spec_id.to_string();
                e.cite_version = version.to_string();
            }
        }
    }
}

/// THE SCOPE replace_mined_acronyms DELETES, spelled exactly as its DELETE spells
/// it (rust/store/src/lib.rs). The guard reads the rows this pass owns through
/// the same predicate, so "what the pass would replace" and "what the guard
/// compares" cannot be two different sets. `mined_scope_is_the_delete_scope`
/// holds the two together by behaviour, not by text.
const MINED_SCOPE: &str = "source_series = 'etsi' OR source_series LIKE 'ETSI %'";

/// row_hash reduces one glossary row — every column the write sets — to a number,
/// so the stored glossary and the one about to be written can be compared without
/// sorting either.
///
/// NULL IS NOT "" AND IS NOT 0: each slot carries a presence byte before its
/// value, and each string a length prefix, so ("ab","c") and ("a","bc") differ by
/// construction. The same shape as rust/store/src/changes.rs, which could not be
/// reused without touching lib.rs — see `already_written`.
///
/// std's SipHash, not SHA-256: the ingest crate does not link sha2, and adding it
/// would move Cargo.lock, which four data steps declare. The sums are compared
/// inside ONE process, so the hasher's lack of a cross-release guarantee does not
/// matter; two independently tagged 64-bit hashes make a 128-bit row hash, far
/// beyond what 28 154 rows can collide on by accident.
fn row_hash(text: [Option<&str>; 6], declared_by: Option<i64>) -> u128 {
    use std::hash::Hasher;
    let mut out = 0u128;
    for (i, tag) in [b'l', b'h'].into_iter().enumerate() {
        let mut h = std::collections::hash_map::DefaultHasher::new();
        h.write(b"etsi-glossary-row-v1");
        h.write_u8(tag);
        for f in text {
            match f {
                Some(s) => {
                    h.write_u8(1);
                    h.write_u64(s.len() as u64);
                    h.write(s.as_bytes());
                }
                None => h.write_u8(0),
            }
        }
        match declared_by {
            Some(v) => {
                h.write_u8(1);
                h.write_i64(v);
            }
            None => h.write_u8(0),
        }
        out |= (h.finish() as u128) << (64 * i);
    }
    out
}

/// already_written answers whether the rows this pass owns ARE, column for column,
/// the rows replace_mined_acronyms would leave behind — in which case writing them
/// again changes nothing but the file.
///
/// WHY IT EXISTS: THE TABLE WAS IDEMPOTENT, THE FILE WAS NOT. The replace is a
/// DELETE of every mined row and an INSERT of the same rows, and DuckDB does not
/// put them back in the blocks they came from. Measured 2026-09-11 on a copy of
/// the published etsi.duckdb: the same 28 154 rows, and the file went from
/// 19 570 896 896 to 19 574 829 056 bytes. The corpus is ONE image layer per half,
/// addressed by content, so every replay of `enrich-etsi` — an edit to the
/// extraction rule, to the ETSI header parser, to a manifest, to the lockfile —
/// re-pushed the whole ETSI half (~19.5 GB) for a glossary that had not moved.
/// `ingest-crs` paid for the same lesson on the 3GPP half (#323).
///
/// IT READS THE TABLE. No ledger, no stamp: a glossary replaced out of band by a
/// different one of the same size must not pass. The comparison is a count plus
/// an order-independent WRAPPING SUM of row hashes over every column the write
/// sets — addition, not XOR, so a row held twice does not cancel itself out.
///
/// WHAT "WOULD LEAVE BEHIND" MEANS: the write's INSERT is ON CONFLICT DO NOTHING
/// against rows this pass does not own, so a mined key already held by such a row
/// never lands. Leaving those out of the expected set is what keeps the guard
/// idempotent on a table that has them; counting them would make it rewrite the
/// corpus on every run, forever, over a row it can never write.
///
/// IN THIS FILE AND NOT IN rust/store, on purpose. The natural home is a store
/// method beside replace_mined_acronyms, but a new store file needs a `mod` line
/// in rust/store/src/lib.rs, and lib.rs is part of the 3GPP fold's identity
/// (internal/goal foldImpl, recorded in .local/state/fold-state.json): editing it
/// re-folds the 3GPP corpus — 34 min, a rewritten 3gpp.duckdb, a 22 GB layer —
/// to change a pass that only ever writes etsi.duckdb. Store::raw() already
/// exposes the connection for read paths, which is all this needs.
fn already_written(store: &store_rs::Store, rows: &[store_rs::MinedAcronym]) -> Result<bool> {
    let mut st = store.raw().prepare(&format!(
        "SELECT term, expansion, domain, first_release, last_release, source_series,
                CAST(declared_by AS BIGINT), coalesce({MINED_SCOPE}, false)
           FROM acronyms"
    ))?;
    let it = st.query_map([], |r| {
        Ok((
            [
                r.get::<_, Option<String>>(0)?,
                r.get::<_, Option<String>>(1)?,
                r.get::<_, Option<String>>(2)?,
                r.get::<_, Option<String>>(3)?,
                r.get::<_, Option<String>>(4)?,
                r.get::<_, Option<String>>(5)?,
            ],
            r.get::<_, Option<i64>>(6)?,
            r.get::<_, bool>(7)?,
        ))
    })?;
    // A DISCARDED ROW ERROR WOULD READ AS A SHORTER GLOSSARY, and a shorter
    // glossary only ever makes the guard rewrite — never skip — so it would be
    // safe; it would also be silent. Propagated, as in changes_sum.
    let (mut have, mut have_n) = (0u128, 0usize);
    let mut foreign: std::collections::HashSet<(String, String, String)> =
        std::collections::HashSet::new();
    for r in it {
        let (text, declared, mine) = r.context("read the glossary etsi.duckdb already holds")?;
        if mine {
            let parts: [Option<&str>; 6] = std::array::from_fn(|i| text[i].as_deref());
            have = have.wrapping_add(row_hash(parts, declared));
            have_n += 1;
        } else if let [Some(t), Some(e), Some(d), ..] = &text {
            // Only a non-NULL domain can conflict: the key is (term, expansion,
            // domain) and a NULL never equals the "" the write stages.
            foreign.insert((t.clone(), e.clone(), d.clone()));
        }
    }
    let (mut want, mut want_n) = (0u128, 0usize);
    for r in rows {
        // The domain the write stages is "" (replace_mined_acronyms).
        if foreign.contains(&(r.term.clone(), r.expansion.clone(), String::new())) {
            continue;
        }
        let parts = [
            Some(r.term.as_str()),
            Some(r.expansion.as_str()),
            Some(""),
            Some(r.version.as_str()),
            Some(r.version.as_str()),
            Some(r.source.as_str()),
        ];
        want = want.wrapping_add(row_hash(parts, Some(r.declared_by)));
        want_n += 1;
    }
    Ok(have_n == want_n && have == want)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = store_rs::Store::open_rw(&args.db)?;

    let files = newest_per_deliverable(collect_html(&args.convert)?);
    eprintln!(
        "ingest-glossary: {} deliverable(s) (newest version of each)",
        files.len()
    );

    let (mut specs, mut rows) = (0usize, 0usize);
    let (mut candidates, mut dropped_files, mut dropped_rows) = (0usize, 0usize, 0usize);
    let mut tally = Tally::default();
    let trace = std::env::var("GLOSSARY_TRACE").is_ok_and(|v| v != "0" && !v.is_empty());
    for (i, f) in files.iter().enumerate() {
        // NAME THE FILE, AND SAY WHERE THE TIME WENT.
        //
        // This pass stalled twice on the same deliverable — 2026-09-07 21:53 and
        // 2026-09-08 06:59, both after "5000/5142 file(s)" and then nothing for
        // hours. Measured live on the second: 145 % of one core, ZERO read and ZERO
        // write operations over 20 s, so it was neither the corpus write nor the
        // disk. It was CPU inside ONE file that had already been read, and the
        // 500-file counter could not say which — it names a position in a list
        // nobody has, and only every five hundredth one.
        //
        // So the counter is no longer the only thing that speaks. A file that takes
        // longer than a person would wait names ITSELF, with the phase that spent
        // the time, whether or not anyone thought to set a trace variable first.
        // The threshold is far above anything healthy: the fast phase of this same
        // run averages about 24 ms per file.
        if trace {
            eprintln!("ingest-glossary: [{}/{}] {f}", i + 1, files.len());
        }
        let t_read = std::time::Instant::now();
        let html = parse3gpp::html_bytes::read_html(f).with_context(|| format!("read {f}"))?;
        let read_el = t_read.elapsed();
        let Some(meta) = parse3gpp::etsi::parse_etsi_meta(&html) else {
            continue; // not an ETSI deliverable: no provenance header
        };
        let t_parse = std::time::Instant::now();
        let (clauses, _, _) =
            parse3gpp::parse_html_clauses(&html, &meta.spec_id, &meta.release, &meta.version);
        let parse_el = t_parse.elapsed();
        // first/last carry the VERSION, not meta.release: an ETSI deliverable's
        // release is the constant "ETSI", so stamping it would put zero information
        // in a field the caller reads to know when a term applied.
        let t_ex = std::time::Instant::now();
        let acs = parse3gpp::glossary::extract_acronyms(&clauses, &meta.version);
        let ex_el = t_ex.elapsed();
        if read_el + parse_el + ex_el >= SLOW_FILE {
            eprintln!(
                "ingest-glossary: SLOW {f} — read {:.1}s, parse {:.1}s ({} clause(s)), extract {:.1}s",
                read_el.as_secs_f64(),
                parse_el.as_secs_f64(),
                clauses.len(),
                ex_el.as_secs_f64()
            );
        }
        if acs.is_empty() {
            continue;
        }
        candidates += acs.len();
        let kept: Vec<(String, String)> = acs
            .iter()
            .filter(|a| initials_match(&a.term, &a.expansion))
            .map(|a| (a.term.clone(), a.expansion.clone()))
            .collect();
        if (kept.len() as f64) < MIN_FILE_CONSISTENCY * (acs.len() as f64) {
            dropped_files += 1;
            dropped_rows += acs.len();
            continue;
        }
        dropped_rows += acs.len() - kept.len();
        specs += 1;
        rows += kept.len();
        tally.add_file(&meta.spec_id, &meta.version, &kept);
        if (i + 1) % 500 == 0 {
            eprintln!(
                "ingest-glossary: {}/{} file(s), {rows} row(s) so far",
                i + 1,
                files.len()
            );
        }
    }

    // THE WRITE IS THE SECOND PASS, and it is what makes this idempotent.
    //
    // Row-at-a-time writing could not carry the count, and it could not carry an
    // honest citation either: the same (term, expansion) is upserted by every
    // deliverable that declares it, so source_series ended up naming whichever
    // file the walker happened to reach last. That was hidden while every row was
    // stamped with the constant "etsi" — a value that names no document, cites
    // nothing, and left the ETSI half unrankable.
    // AND IT REPLACES RATHER THAN ADDS. The pass reads the newest version of every
    // deliverable, so an expansion a later version corrected — or dropped — kept
    // its row for ever beside the current one and stayed visible through
    // resolve_term. The vocabulary is a snapshot of what the archive says now, and
    // an additive writer cannot express that. The scope is the provenance itself,
    // which is why this could not have been written while every row said "etsi".
    let mined: Vec<store_rs::MinedAcronym> = tally
        .rows
        .iter()
        .map(|((term, expansion), c)| store_rs::MinedAcronym {
            term: term.clone(),
            expansion: expansion.clone(),
            version: c.cite_version.clone(),
            source: c.cite_spec.clone(),
            declared_by: c.declared_by,
        })
        .collect();
    // A PASS THAT MINED NOTHING is a regression, not "no work": every ETSI TS/EN
    // carries clause 3. Fail loudly rather than leave resolve_term silently
    // 3GPP-only, which is the state this pass exists to end — and fail BEFORE the
    // write, which is a replacement: reaching it with nothing would delete the
    // whole ETSI glossary and only then report the failure. (This check used to
    // sit after the write, on its return value.)
    if mined.is_empty() {
        anyhow::bail!("no acronym was extracted from {} — the abbreviations heuristic or the corpus is broken", args.convert);
    }
    let agreed = tally.rows.values().filter(|c| c.declared_by > 1).count();
    let summary = format!(
        "ingest-glossary: {candidates} candidate row(s); dropped {dropped_rows} \
         (of which {dropped_files} whole file(s) whose columns did not line up); \
         kept {rows} declaration(s) from {specs} deliverable(s) -> {} row(s), \
         {agreed} of them declared by more than one deliverable",
        mined.len()
    );

    // NOTHING TO WRITE IS NOT A WRITE. No replace and no checkpoint on this path:
    // either one moves the file, and the file is an image layer. See
    // already_written for the 3.9 MB a same-rows replace was measured to add.
    if already_written(&store, &mined)? {
        eprintln!("{summary}");
        eprintln!(
            "ingest-glossary: the glossary already carries exactly these rows — corpus untouched"
        );
        return Ok(());
    }

    // SAY WHAT IS ABOUT TO BE WRITTEN, BEFORE WRITING IT.
    //
    // Between the last "N/5142 file(s)" line and the end of the pass there was NO
    // output at all, so when the write hung — twice, for two and a half hours each
    // — the log's final word was a file counter, and every reading of the stall
    // started from the wrong half of the program. Two nights were spent on the
    // parser and on the 49.6 GB corpus before a live measurement showed the loop
    // had finished minutes earlier. A phase that can take time announces itself.
    eprintln!(
        "ingest-glossary: mined {} distinct (term, expansion) pair(s); writing them",
        mined.len()
    );
    store.replace_mined_acronyms(&mined)?;
    eprintln!("{summary}");
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn decls(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
        pairs
            .iter()
            .map(|(t, e)| (t.to_string(), e.to_string()))
            .collect()
    }

    /// THE ANSWER THE ETSI HALF USED TO GIVE, and why counting changes it.
    ///
    /// Measured on the shipped corpus 2026-09-07: MSC carries twelve expansions
    /// and Store.ResolveTerm ordered them `domain, expansion` with domain empty on
    /// every ETSI row — so the first row a caller read was "Main Service Channel",
    /// alphabetically first and, for anyone asking about a mobile network, wrong.
    /// The switching centre is what the archive overwhelmingly declares, and that
    /// is a countable fact rather than a preference.
    #[test]
    fn the_expansion_most_deliverables_declare_wins_over_the_alphabet() {
        let mut t = Tally::default();
        for spec in ["ETSI TS 101 200", "ETSI TS 102 221", "ETSI TS 103 221-1"] {
            t.add_file(
                spec,
                "1.1.1",
                &decls(&[("MSC", "Mobile-services Switching Centre")]),
            );
        }
        t.add_file(
            "ETSI EN 300 175-1",
            "2.7.1",
            &decls(&[("MSC", "Main Service Channel")]),
        );

        let switching = &t.rows[&("MSC".into(), "Mobile-services Switching Centre".into())];
        let channel = &t.rows[&("MSC".into(), "Main Service Channel".into())];
        assert_eq!(switching.declared_by, 3);
        assert_eq!(channel.declared_by, 1);
        // BOTH ARE KEPT. Ranking is not filtering: "Main Service Channel" is a real
        // DECT term and the corpus reproduces its sources rather than choosing for
        // them. What changes is which one a caller reads first.
        assert_eq!(t.rows.len(), 2);
    }

    /// ONE DELIVERABLE, ONE VOTE. An Abbreviations clause can list the same pair
    /// twice — a Symbols heading and an Abbreviations heading in the same region —
    /// and counting both would let one document outvote two.
    #[test]
    fn a_pair_listed_twice_in_one_file_counts_once() {
        let mut t = Tally::default();
        t.add_file(
            "ETSI TS 102 221",
            "18.4.0",
            &decls(&[
                ("TC", "Transmission Convergence"),
                ("TC", "Transmission Convergence"),
            ]),
        );
        assert_eq!(
            t.rows[&("TC".into(), "Transmission Convergence".into())].declared_by,
            1
        );
    }

    /// THE CITATION IS REPRODUCIBLE, and that is all it claims.
    ///
    /// The row names the lexicographically smallest declaring deliverable, with
    /// its version, because the same corpus must produce the same row on every
    /// run — the previous pass stamped whichever file the walker reached last,
    /// under a constant "etsi" that hid it. It is not a claim that this
    /// deliverable is more authoritative; `declared_by` is what says how many
    /// others agree.
    #[test]
    fn the_cited_deliverable_does_not_depend_on_walk_order() {
        let files: [(&str, &str); 3] = [
            ("ETSI TS 103 221-1", "1.12.1"),
            ("ETSI TS 102 221", "18.4.0"),
            ("ETSI EN 300 175-1", "2.7.1"),
        ];
        let mut forward = Tally::default();
        for (spec, ver) in files {
            forward.add_file(spec, ver, &decls(&[("AID", "Application IDentifier")]));
        }
        let mut backward = Tally::default();
        for (spec, ver) in files.iter().rev() {
            backward.add_file(spec, ver, &decls(&[("AID", "Application IDentifier")]));
        }

        let key = ("AID".to_string(), "Application IDentifier".to_string());
        assert_eq!(forward.rows[&key].cite_spec, "ETSI EN 300 175-1");
        assert_eq!(forward.rows[&key].cite_version, "2.7.1");
        assert_eq!(backward.rows[&key].cite_spec, forward.rows[&key].cite_spec);
        assert_eq!(
            backward.rows[&key].cite_version, forward.rows[&key].cite_version,
            "the version must travel with the deliverable it cites, not with the last file read"
        );
        assert_eq!(forward.rows[&key].declared_by, 3);
    }

    /// THE DEFECT THE GUARD EXISTS FOR, taken verbatim from EN 300 286-2 v1.2.4.
    /// `pdftotext -layout` prints a tall cell's expansion ABOVE its term, so from
    /// there every line pairs term N with expansion N+1 for the rest of the list.
    /// Measured over the whole ETSI corpus without the guard: 8 995 rows, 3 264
    /// (36,3 %) belonging to another term — "IMSI = International Organization for
    /// Standardization" among them.
    #[test]
    fn a_shifted_column_does_not_pass() {
        for (t, e) in [
            ("DSS1", "Information Elements Received"),
            ("IER", "Information Elements Transmitted"),
            ("ISDN", "Implementation Under Test"),
            ("IMSI", "International Organization for Standardization"),
        ] {
            assert!(!initials_match(t, e), "{t} / {e} must be refused");
        }
    }

    /// And it must not refuse the real thing, including a term carrying digits or
    /// punctuation, and an expansion with a parenthetical tail.
    #[test]
    fn a_real_abbreviation_passes() {
        for (t, e) in [
            ("IMSI", "International Mobile Subscriber Identity"),
            ("UICC", "Universal Integrated Circuit Card"),
            (
                "STM-1",
                "Synchronous Transport Module Level 1 (155,52 Mbit/s)",
            ),
            ("TD-CDMA", "Time Division Code Division Multiple Access"),
            ("BBERF", "Bearer Binding and Event Reporting Function"),
        ] {
            assert!(initials_match(t, e), "{t} / {e} must be kept");
        }
    }

    /// The guard is conservative in the SAFE direction and this pins that, so the
    /// loss is a decision on the record rather than a surprise: a syllabic
    /// abbreviation is correct and is still dropped.
    #[test]
    fn the_guard_is_known_to_be_conservative() {
        assert!(!initials_match("CAPEX", "Capital Expenditure"));
        assert!(!initials_match("N/A", "not supported"));
    }

    fn mined(term: &str, expansion: &str, source: &str, n: i64) -> store_rs::MinedAcronym {
        store_rs::MinedAcronym {
            term: term.into(),
            expansion: expansion.into(),
            version: "1.1.1".into(),
            source: source.into(),
            declared_by: n,
        }
    }

    fn batch() -> Vec<store_rs::MinedAcronym> {
        vec![
            mined(
                "MSC",
                "Mobile-services Switching Centre",
                "ETSI TS 101 200",
                3,
            ),
            mined(
                "UICC",
                "Universal Integrated Circuit Card",
                "ETSI TS 102 221",
                1,
            ),
        ]
    }

    /// A glossary the pass itself just wrote is recognised as written — the second
    /// run of an unchanged archive writes nothing. This is the property the whole
    /// guard exists for: measured on a copy of the published etsi.duckdb, a
    /// same-rows replace added 3.9 MB to the file, i.e. a new 19.5 GB image layer.
    #[test]
    fn a_glossary_the_pass_wrote_is_already_written() {
        let s = store_rs::Store::in_memory().unwrap();
        assert!(
            !already_written(&s, &batch()).unwrap(),
            "an empty table holds none of it"
        );
        s.replace_mined_acronyms(&batch()).unwrap();
        assert!(already_written(&s, &batch()).unwrap());
    }

    /// EVERY COLUMN THE WRITE SETS IS COMPARED — a guard keyed on fewer would skip a
    /// write whose only change is the column it forgot, and report the corpus
    /// current. Each edit below leaves the row count unchanged.
    #[test]
    fn a_change_in_any_written_column_is_seen() {
        for (what, sql) in [
            (
                "expansion",
                "UPDATE acronyms SET expansion = 'Mobile Switching Centre' WHERE term = 'MSC'",
            ),
            (
                "domain",
                "UPDATE acronyms SET domain = 'RAN' WHERE term = 'MSC'",
            ),
            (
                "first_release",
                "UPDATE acronyms SET first_release = '1.2.1' WHERE term = 'MSC'",
            ),
            (
                "last_release",
                "UPDATE acronyms SET last_release = '1.2.1' WHERE term = 'MSC'",
            ),
            (
                "citation",
                "UPDATE acronyms SET source_series = 'ETSI TS 102 221' WHERE term = 'MSC'",
            ),
            (
                "count",
                "UPDATE acronyms SET declared_by = 4 WHERE term = 'MSC'",
            ),
            (
                "count NULL is not a number",
                "UPDATE acronyms SET declared_by = NULL WHERE term = 'MSC'",
            ),
        ] {
            let s = store_rs::Store::in_memory().unwrap();
            s.replace_mined_acronyms(&batch()).unwrap();
            s.raw().execute_batch(sql).unwrap();
            assert!(
                !already_written(&s, &batch()).unwrap(),
                "a changed {what} went unnoticed"
            );
        }
    }

    /// A row too many, or a row missing, is a different glossary even when every
    /// other row matches.
    #[test]
    fn an_extra_or_a_missing_row_is_seen() {
        let s = store_rs::Store::in_memory().unwrap();
        s.replace_mined_acronyms(&batch()).unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO acronyms VALUES ('TC', 'Transmission Convergence', '', '1.1.1', '1.1.1', 'ETSI TS 102 221', 1)",
            )
            .unwrap();
        assert!(
            !already_written(&s, &batch()).unwrap(),
            "an extra mined row went unnoticed"
        );

        let s = store_rs::Store::in_memory().unwrap();
        s.replace_mined_acronyms(&batch()).unwrap();
        let mut more = batch();
        more.push(mined(
            "TC",
            "Transmission Convergence",
            "ETSI TS 102 221",
            1,
        ));
        assert!(
            !already_written(&s, &more).unwrap(),
            "a row the archive gained went unnoticed"
        );
    }

    /// Rows the pass does not own are not its glossary: a 3GPP row beside the mined
    /// ones changes nothing — and a mined key that collides with such a row, which
    /// the write's ON CONFLICT DO NOTHING never lands, must not make every later
    /// run rewrite the corpus over a row it cannot write.
    #[test]
    fn rows_the_pass_does_not_own_are_not_compared() {
        let s = store_rs::Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO acronyms VALUES ('UICC', 'Universal Integrated Circuit Card', '', 'Rel-19', 'Rel-19', '21', NULL);
                 INSERT INTO acronyms VALUES ('AMF', 'Access and Mobility Management Function', '', '19.0.0', '19.0.0', '23.501', 79);",
            )
            .unwrap();
        s.replace_mined_acronyms(&batch()).unwrap();
        assert!(already_written(&s, &batch()).unwrap());
    }

    /// THE GUARD READS THE SAME SET THE WRITE REPLACES. MINED_SCOPE is spelled like
    /// replace_mined_acronyms' DELETE; this holds the two together by behaviour: a
    /// row the DELETE removes is one the guard counts, and a row it leaves is one
    /// the guard ignores — the legacy constant "etsi", a NULL provenance and a
    /// look-alike ("ETSI" with no space) included.
    #[test]
    fn mined_scope_is_the_delete_scope() {
        let s = store_rs::Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO acronyms VALUES ('A1', 'x one', '', 'v', 'v', 'etsi', 1);
                 INSERT INTO acronyms VALUES ('A2', 'x two', '', 'v', 'v', 'ETSI TS 102 221', 1);
                 INSERT INTO acronyms VALUES ('A3', 'x three', '', 'v', 'v', 'ETSIX', 1);
                 INSERT INTO acronyms VALUES ('A4', 'x four', '', 'v', 'v', NULL, 1);
                 INSERT INTO acronyms VALUES ('A5', 'x five', '', 'v', 'v', '21', NULL);",
            )
            .unwrap();
        let in_scope = |s: &store_rs::Store| -> Vec<String> {
            let mut st = s
                .raw()
                .prepare(&format!(
                    "SELECT term FROM acronyms WHERE coalesce({MINED_SCOPE}, false) ORDER BY term"
                ))
                .unwrap();
            let v: Vec<String> = st
                .query_map([], |r| r.get::<_, String>(0))
                .unwrap()
                .map(|r| r.unwrap())
                .collect();
            v
        };
        let scoped = in_scope(&s);
        assert_eq!(scoped, vec!["A1", "A2"]);
        s.replace_mined_acronyms(&[mined("Z", "Zed", "ETSI TS 1", 1)])
            .unwrap();
        let mut left: Vec<String> = {
            let mut st = s.raw().prepare("SELECT term FROM acronyms").unwrap();
            let v: Vec<String> = st
                .query_map([], |r| r.get::<_, String>(0))
                .unwrap()
                .map(|r| r.unwrap())
                .collect();
            v
        };
        left.sort();
        assert_eq!(
            left,
            vec!["A3", "A4", "A5", "Z"],
            "the DELETE removed exactly the rows MINED_SCOPE selects"
        );
    }

    /// 18.10.0 is NEWER than 18.9.0, and a string sort says otherwise. Picking the
    /// wrong file would mine a stale vocabulary while looking entirely correct.
    #[test]
    fn newest_is_numeric_not_lexical() {
        let got = newest_per_deliverable(vec![
            "d/TS_102_221_v18.9.0.html".into(),
            "d/TS_102_221_v18.10.0.html".into(),
            "d/TS_102_221_v3.1.0.html".into(),
            "d/TR_103_101_v1.1.1.html".into(),
        ]);
        assert_eq!(
            got,
            vec![
                "d/TR_103_101_v1.1.1.html".to_string(),
                "d/TS_102_221_v18.10.0.html".to_string()
            ],
            "one file per deliverable, and the newest by NUMBER"
        );
    }
}
