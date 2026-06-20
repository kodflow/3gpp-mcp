//! ingest-openapi (Rust) — Phase 6. The additive 5GC OpenAPI overlay: parse the canonical
//! YAML corpus (parse3gpp::openapi) and write the api_* tables via store-rs. Additive to
//! the HTML ingest — it clears ONLY the api_* tables, never clauses
//! (internal/openapi/CLAUDE.md). The on-disk layout is <src>/<Rel>/<sha>/<file>.yaml; the
//! release + Forge SHA travel with each row so every operation/schema is self-citing.
//!
//! Usage: ingest-openapi --src <dir> --db <out.duckdb>
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::openapi::{parse_openapi, ParsedOperation, ParsedSchema};
use store_rs::{ApiOperationIn, ApiSchemaIn, Store};

#[derive(Parser)]
#[command(
    name = "ingest-openapi",
    about = "Parse the 5GC OpenAPI YAML corpus into api_* tables (parse3gpp + store-rs)"
)]
struct Args {
    /// Root of the YAML corpus: <src>/<Rel>/<sha>/<file>.yaml.
    #[arg(long)]
    src: String,
    /// DuckDB to overlay (api_* tables only).
    #[arg(long)]
    db: String,
}

fn op_in(o: &ParsedOperation, release: &str) -> ApiOperationIn {
    ApiOperationIn {
        spec_id: o.spec_id.clone(),
        release: release.to_string(),
        version: o.version.clone(),
        api_doc_version: o.api_doc_version.clone(),
        service: o.service.clone(),
        service_family: o.service_family.clone(),
        api_root: o.api_root.clone(),
        path: o.path.clone(),
        method: o.method.clone(),
        operation_id: o.operation_id.clone(),
        summary: o.summary.clone(),
        tags: o.tags.clone(),
        request_schema: o.request_schema.clone(),
        response_codes: o.response_codes.clone(),
        yaml_file: o.yaml_file.clone(),
        forge_sha: o.forge_sha.clone(),
        forge_url: o.forge_url.clone(),
    }
}

fn schema_in(s: &ParsedSchema, release: &str) -> ApiSchemaIn {
    ApiSchemaIn {
        spec_id: s.spec_id.clone(),
        release: release.to_string(),
        version: s.version.clone(),
        service: s.service.clone(),
        schema_name: s.schema_name.clone(),
        kind: s.kind.clone(),
        description: s.description.clone(),
        properties: s.properties.clone(),
        enum_values: s.enum_values.clone(),
        refs_out: s.refs_out.clone(),
        yaml_file: s.yaml_file.clone(),
        forge_sha: s.forge_sha.clone(),
        forge_url: s.forge_url.clone(),
    }
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.db)?;
    store.clear_api_tables()?;

    let mut files = 0usize;
    let mut ops_total = 0usize;
    let mut schemas_total = 0usize;

    // Walk <src>/<Rel>/<sha>/<file>.yaml. release = the <Rel> dir, sha = the <sha> dir.
    for rel_ent in std::fs::read_dir(&args.src).with_context(|| format!("read_dir {}", args.src))? {
        let rel_path = rel_ent?.path();
        if !rel_path.is_dir() {
            continue;
        }
        let release = rel_path
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or("")
            .to_string();
        for sha_ent in std::fs::read_dir(&rel_path)? {
            let sha_path = sha_ent?.path();
            if !sha_path.is_dir() {
                continue;
            }
            let sha = sha_path
                .file_name()
                .and_then(|s| s.to_str())
                .unwrap_or("")
                .to_string();
            for file_ent in std::fs::read_dir(&sha_path)? {
                let file_path = file_ent?.path();
                if file_path.extension().and_then(|e| e.to_str()) != Some("yaml") {
                    continue;
                }
                let filename = file_path
                    .file_name()
                    .and_then(|s| s.to_str())
                    .unwrap_or("")
                    .to_string();
                let data = std::fs::read_to_string(&file_path)
                    .with_context(|| format!("read {filename}"))?;
                let r = match parse_openapi(&data, &filename, &sha) {
                    Ok(r) => r,
                    Err(e) => {
                        eprintln!("ingest-openapi: skip {filename}: {e}");
                        continue;
                    }
                };
                let ops: Vec<ApiOperationIn> =
                    r.operations.iter().map(|o| op_in(o, &release)).collect();
                let schemas: Vec<ApiSchemaIn> =
                    r.schemas.iter().map(|s| schema_in(s, &release)).collect();
                store.insert_api_operations(&ops)?;
                store.insert_api_schemas(&schemas)?;
                files += 1;
                ops_total += ops.len();
                schemas_total += schemas.len();
            }
        }
    }
    store.checkpoint()?;
    eprintln!(
        "ingest-openapi: {files} file(s) → {ops_total} operation(s), {schemas_total} schema(s)"
    );
    Ok(())
}
