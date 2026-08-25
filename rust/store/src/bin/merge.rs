//! merge (Rust) — fold disjoint per-series/-release shard DuckDBs into one corpus DB via
//! store-rs (== Go cmd/merge). Beyond folding it reproduces, BYTE-IDENTICALLY, the three
//! index sidecars discover consumes (corpus-index, subject-index, build-index — see
//! store_rs::identity, golden-tested), supports --base incremental bucket replacement and
//! --strip-embeddings (lexical-only), and rebuilds FTS + the frozen HNSW ONCE on the merged
//! DB (per-shard HNSW indexes are not concatenable — internal/store/CLAUDE.md).
use anyhow::{Context, Result};
use clap::Parser;
use std::collections::{BTreeMap, BTreeSet, HashMap};
use store_rs::identity;
use store_rs::Store;

#[derive(Parser)]
#[command(
    name = "merge",
    about = "Fold per-shard DuckDBs into one corpus DB (Rust port of cmd/merge)"
)]
struct Args {
    /// Output merged DuckDB.
    #[arg(long)]
    out: String,
    /// EmbedIdentity stamped into the merged DB's frozen HNSW (DB meta wins when present).
    #[arg(long, default_value = "")]
    model: String,
    /// Fold rows only; skip the FTS + HNSW rebuild.
    #[arg(long, default_value_t = false)]
    no_index: bool,
    /// Skip ONLY the HNSW build (FTS still rebuilt) — for low-RAM boxes (== Go --no-hnsw).
    #[arg(long, default_value_t = false)]
    no_hnsw: bool,
    /// NULL all vectors + purge vector meta → lexical-only output (== Go --strip-embeddings).
    #[arg(long, default_value_t = false)]
    strip_embeddings: bool,
    /// Existing DB to start from (incremental): each shard's (spec,release) buckets REPLACE
    /// the base's; subjects whose series are not all rebuilt carry the base's footprint.
    #[arg(long, default_value = "")]
    base: String,
    /// Also write a corpus-index.json (spec|release → latest version).
    #[arg(long, default_value = "")]
    index_out: String,
    /// Also write a subject-index.json (subject → effective footprint).
    #[arg(long, default_value = "")]
    subject_index_out: String,
    /// Also write a build-index.json (the three canonical identities).
    #[arg(long, default_value = "")]
    build_index_out: String,
    /// Shard DuckDB paths to fold (disjoint on (spec, release)).
    shards: Vec<String>,
}

fn main() -> Result<()> {
    let args = Args::parse();
    if args.shards.is_empty() {
        anyhow::bail!("merge: pass at least one shard path");
    }

    // --base: start from the prior corpus (copy it), then REPLACE each shard's buckets. The
    // base's footprints are read first (carry-forward for subjects not rebuilt this run).
    let mut base_fps: HashMap<String, String> = HashMap::new();
    let _ = std::fs::remove_file(&args.out);
    if !args.base.is_empty() {
        std::fs::copy(&args.base, &args.out).with_context(|| format!("copy base {}", args.base))?;
        let b = Store::open_rw(&args.out).context("open base copy")?;
        for (name, _, _, _) in identity::SUBJECTS {
            base_fps.insert(name.to_string(), b.get_meta(&format!("subject_fp_{name}"))?);
        }
        b.checkpoint()?;
        drop(b);
    }
    let store = Store::open_rw(&args.out).context("open merged DB")?;

    // rebuilt = the union of series across THIS run's input shards (NOT the base).
    let mut rebuilt: BTreeSet<String> = BTreeSet::new();
    for shard in &args.shards {
        for s in store.shard_series(shard)? {
            rebuilt.insert(s);
        }
        // KEEP THE VECTORS OF TEXT THAT DID NOT CHANGE.
        //
        // A bucket replacement is delete-then-insert, and the shard carries no
        // embeddings — so without this every re-ingested spec loses its vectors even
        // when its wording is untouched, and the next embed pass pays the GPU for work
        // already done (211 511 clauses on the 2026-08-25 repair). Stash before the
        // delete, hand back after the fold.
        let mut carried = 0usize;
        if !args.base.is_empty() {
            let buckets = store.shard_spec_releases(shard)?;
            store.stash_bucket_vectors(&buckets)?;
            for (spec, rel) in &buckets {
                store.delete_spec_release(spec, rel)?;
            }
        }
        let offset = store.max_chunk_id()?;
        store.fold_shard(shard, offset)?;
        if !args.base.is_empty() {
            carried = store.restore_stashed_vectors()?;
        }
        eprintln!("merge: folded {shard} (chunk_id offset {offset}, {carried} vector(s) carried)");
    }

    if args.strip_embeddings {
        store.strip_embeddings()?;
        eprintln!("merge: stripped embeddings (lexical-only output)");
    }

    // Effective subject footprints → stamped into meta (so a later --base carries them).
    let eff = identity::effective_subject_footprints(&rebuilt, &base_fps);
    for (name, fp) in &eff {
        store.set_meta(&format!("subject_fp_{name}"), fp)?;
    }
    store.set_meta("producer", "rust-writeside")?;
    store.set_meta("schema_version", "1")?;
    store.checkpoint()?;

    if !args.no_index {
        if let Err(e) = store.enable_fts() {
            eprintln!("merge: FTS build skipped ({e})");
        }
        let want_hnsw = !args.no_hnsw && !args.strip_embeddings;
        if want_hnsw && store.count_null_embeddings()? == 0 && store.count_clauses()? > 0 {
            let model = store.get_meta("embedding_model")?;
            let model = if model.is_empty() {
                if args.model.is_empty() {
                    "merged".to_string()
                } else {
                    args.model.clone()
                }
            } else {
                model
            };
            store.build_and_freeze_hnsw(&model)?;
            eprintln!("merge: built FTS + frozen HNSW (model={model})");
        } else {
            eprintln!("merge: FTS built, HNSW skipped (no_hnsw/strip/unembedded)");
        }
    }

    // ---- index sidecars (byte-identical to Go cmd/merge; discover reads these) ----
    if !args.index_out.is_empty() {
        let mut idx: BTreeMap<String, String> = BTreeMap::new();
        for (spec, rel, ver) in store.spec_versions()? {
            let key = format!("{spec}|{rel}");
            let better = idx
                .get(&key)
                .is_none_or(|cur| identity::cmp_ver(&ver, cur) == std::cmp::Ordering::Greater);
            if better {
                idx.insert(key, ver);
            }
        }
        write_json(&args.index_out, &idx)?;
        eprintln!("merge: index: {} — {} specs", args.index_out, idx.len());
    }
    if !args.subject_index_out.is_empty() {
        let map: BTreeMap<String, String> = eff.iter().cloned().collect();
        write_json(&args.subject_index_out, &map)?;
        eprintln!(
            "merge: subject-index: {} — {} subjects",
            args.subject_index_out,
            map.len()
        );
    }
    if !args.build_index_out.is_empty() {
        let model = store.get_meta("embedding_model")?;
        let fps: Vec<String> = eff.iter().map(|(_, f)| f.clone()).collect();
        let bi = BTreeMap::from([
            (
                "spec_ingest_identity".to_string(),
                identity::spec_ingest_identity(&fps),
            ),
            (
                "global_enrichment_identity".to_string(),
                identity::global_enrichment_identity(),
            ),
            (
                "embed_identity".to_string(),
                identity::embed_identity(&model),
            ),
        ]);
        write_json(&args.build_index_out, &bi)?;
        eprintln!("merge: build-index: {}", args.build_index_out);
    }

    eprintln!("merge: wrote {}", args.out);
    Ok(())
}

/// write_json writes a string→string map as pretty JSON with a single-space indent (matching
/// Go's json.MarshalIndent("", " ") so the sidecars are byte-identical).
fn write_json(path: &str, m: &BTreeMap<String, String>) -> Result<()> {
    let mut s = String::from("{");
    for (i, (k, v)) in m.iter().enumerate() {
        s.push_str(if i == 0 { "\n " } else { ",\n " });
        s.push_str(&format!(
            "{}: {}",
            serde_json::to_string(k)?,
            serde_json::to_string(v)?
        ));
    }
    s.push_str(if m.is_empty() { "}" } else { "\n}" });
    std::fs::write(path, s).with_context(|| format!("write {path}"))?;
    Ok(())
}
