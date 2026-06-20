//! openapi — Rust port of internal/openapi (Phase 6). Parses a canonical 3GPP 5GC OpenAPI
//! YAML into citable operations + schemas. Like the Go original it is deliberately minimal
//! (only the fields needed for indexing + citation) and uses untyped node navigation
//! (serde_yaml::Value here == yaml.Node there). Output order is deterministic (paths,
//! methods, schema names, response codes, refs sorted) so ids are stable.

use regex::Regex;
use serde_yaml::Value;
use std::collections::BTreeSet;
use std::sync::OnceLock;

/// One OpenAPI operation (path+method) — maps to a store-rs ApiOperationIn row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedOperation {
    pub spec_id: String,
    pub version: String,
    pub api_doc_version: String,
    pub service: String,
    pub service_family: String,
    pub api_root: String,
    pub path: String,
    pub method: String,
    pub operation_id: String,
    pub summary: String,
    pub tags: Vec<String>,
    pub request_schema: String,
    pub response_codes: Vec<String>,
    pub yaml_file: String,
    pub forge_sha: String,
    pub forge_url: String,
}

/// One OpenAPI schema / IE (components/schemas entry) — maps to a store-rs ApiSchemaIn row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedSchema {
    pub spec_id: String,
    pub version: String,
    pub service: String,
    pub schema_name: String,
    pub kind: String,
    pub description: String,
    pub properties: Vec<String>,
    pub enum_values: Vec<String>,
    pub refs_out: Vec<String>,
    pub yaml_file: String,
    pub forge_sha: String,
    pub forge_url: String,
}

/// The parse result for one YAML file.
pub struct OpenApiResult {
    pub spec_id: String,
    pub version: String,
    pub operations: Vec<ParsedOperation>,
    pub schemas: Vec<ParsedSchema>,
}

fn re_docs_spec() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"TS\s*(\d{2})\.(\d{3})").unwrap())
}
fn re_docs_ver() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"(?:[Vv]ersion\s+|\bV)(\d+\.\d+\.\d+)").unwrap())
}
fn re_file_spec() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"^TS(\d{2})(\d{3})").unwrap())
}

const HTTP_METHODS: &[&str] = &[
    "get", "put", "post", "delete", "patch", "head", "options", "trace",
];

fn base_name(p: &str) -> &str {
    match p.rfind(['/', '\\']) {
        Some(i) => &p[i + 1..],
        None => p,
    }
}

/// forge_raw_url builds the immutable 3GPP Forge raw-file URL pinned to a SHA (== Go
/// model.ForgeRawURL), optionally pointing at a schema fragment.
pub fn forge_raw_url(file: &str, sha: &str, schema_fragment: &str) -> String {
    if file.is_empty() || sha.is_empty() {
        return String::new();
    }
    let mut u = format!("https://forge.3gpp.org/rep/all/5G_APIs/-/raw/{sha}/{file}");
    if !schema_fragment.is_empty() {
        u.push_str("#/components/schemas/");
        u.push_str(schema_fragment);
    }
    u
}

fn str_at<'a>(v: &'a Value, key: &str) -> &'a str {
    v.get(key).and_then(Value::as_str).unwrap_or("")
}

fn ref_schema_name(r: &str) -> &str {
    match r.rfind('/') {
        Some(i) => &r[i + 1..],
        None => "",
    }
}

/// ref_edge resolves an OpenAPI $ref to "<spec_id>:<SchemaName>" (== Go refEdge).
fn ref_edge(r: &str, current_spec: &str) -> String {
    let name = ref_schema_name(r);
    if name.is_empty() {
        return String::new();
    }
    if r.starts_with('#') {
        return format!("{current_spec}:{name}");
    }
    let file = match r.find('#') {
        Some(i) => &r[..i],
        None => r,
    };
    let file = base_name(file);
    if let Some(m) = re_file_spec().captures(file) {
        return format!("{}.{}:{}", &m[1], &m[2], name);
    }
    String::new()
}

/// collect_refs walks a node resolving every $ref to a "<spec>:<Schema>" edge (== Go).
fn collect_refs(node: &Value, current_spec: &str, out: &mut BTreeSet<String>) {
    match node {
        Value::Mapping(m) => {
            for (k, v) in m {
                if k.as_str() == Some("$ref") {
                    if let Some(s) = v.as_str() {
                        let e = ref_edge(s, current_spec);
                        if !e.is_empty() {
                            out.insert(e);
                        }
                    }
                } else {
                    collect_refs(v, current_spec, out);
                }
            }
        }
        Value::Sequence(seq) => {
            for c in seq {
                collect_refs(c, current_spec, out);
            }
        }
        _ => {}
    }
}

/// first_ref_name returns the first $ref schema name anywhere in a node (== Go).
fn first_ref_name(node: &Value) -> String {
    match node {
        Value::Mapping(m) => {
            for (k, v) in m {
                if k.as_str() == Some("$ref") {
                    if let Some(s) = v.as_str() {
                        return ref_schema_name(s).to_string();
                    }
                }
            }
            for (_, v) in m {
                let n = first_ref_name(v);
                if !n.is_empty() {
                    return n;
                }
            }
            String::new()
        }
        Value::Sequence(seq) => {
            for c in seq {
                let n = first_ref_name(c);
                if !n.is_empty() {
                    return n;
                }
            }
            String::new()
        }
        _ => String::new(),
    }
}

fn or_default(s: String, def: &str) -> String {
    if s.is_empty() {
        def.to_string()
    } else {
        s
    }
}

/// schema_from builds a ParsedSchema from its YAML node (== Go schemaFrom).
fn schema_from(name: &str, node: &Value, current_spec: &str) -> ParsedSchema {
    let mut kind = String::new();
    let description = str_at(node, "description").trim().to_string();
    if let Some(t) = node.get("type").and_then(Value::as_str) {
        kind = t.to_string();
    }
    let mut enum_values = Vec::new();
    if let Some(en) = node.get("enum").and_then(Value::as_sequence) {
        kind = "enum".to_string();
        for e in en {
            if let Some(s) = e.as_str() {
                enum_values.push(s.to_string());
            }
        }
    }
    let mut properties = Vec::new();
    if let Some(props) = node.get("properties").and_then(Value::as_mapping) {
        kind = or_default(kind, "object");
        for (k, _) in props {
            if let Some(s) = k.as_str() {
                properties.push(s.to_string());
            }
        }
    }
    for comp in ["allOf", "oneOf", "anyOf"] {
        if let Some(c) = node.get(comp).and_then(Value::as_sequence) {
            kind = or_default(kind, "composite");
            for sub in c {
                if let Some(en) = sub.get("enum").and_then(Value::as_sequence) {
                    kind = "enum".to_string();
                    for e in en {
                        if let Some(s) = e.as_str() {
                            enum_values.push(s.to_string());
                        }
                    }
                }
            }
        }
    }
    kind = or_default(kind, "object");

    let mut refs = BTreeSet::new();
    collect_refs(node, current_spec, &mut refs);
    ParsedSchema {
        spec_id: String::new(),
        version: String::new(),
        service: String::new(),
        schema_name: name.to_string(),
        kind,
        description,
        properties,
        enum_values,
        refs_out: refs.into_iter().collect(),
        yaml_file: String::new(),
        forge_sha: String::new(),
        forge_url: String::new(),
    }
}

/// identify returns (spec_id, version), preferring externalDocs (== Go identify).
fn identify(doc: &Value, filename: &str) -> (String, String) {
    let desc = doc
        .get("externalDocs")
        .and_then(|e| e.get("description"))
        .and_then(Value::as_str)
        .unwrap_or("");
    if let Some(m) = re_docs_spec().captures(desc) {
        let spec = format!("{}.{}", &m[1], &m[2]);
        let ver = re_docs_ver()
            .captures(desc)
            .map(|v| v[1].to_string())
            .unwrap_or_default();
        return (spec, ver);
    }
    if let Some(m) = re_file_spec().captures(filename) {
        return (format!("{}.{}", &m[1], &m[2]), String::new());
    }
    (String::new(), String::new())
}

fn service_from_file(filename: &str) -> (String, String) {
    let name = filename.strip_suffix(".yaml").unwrap_or(filename);
    let service = match name.find('_') {
        Some(i) => name[i + 1..].to_string(),
        None => String::new(),
    };
    if service.is_empty() {
        return (String::new(), String::new());
    }
    let family = match service.find('_') {
        Some(i) => service[..i].to_string(),
        None => service.clone(),
    };
    (service, family)
}

fn api_root_from_servers(doc: &Value) -> String {
    let url = doc
        .get("servers")
        .and_then(Value::as_sequence)
        .and_then(|s| s.first())
        .and_then(|s| s.get("url"))
        .and_then(Value::as_str)
        .unwrap_or("")
        .trim();
    if let Some(i) = url.find("}/") {
        return url[i + 2..].to_string();
    }
    url.strip_prefix('/').unwrap_or(url).to_string()
}

fn request_schema(op: &Value) -> String {
    let content = op.get("requestBody").and_then(|r| r.get("content"));
    let Some(content) = content.and_then(Value::as_mapping) else {
        return String::new();
    };
    let schema = content
        .get(Value::from("application/json"))
        .or_else(|| content.iter().next().map(|(_, v)| v))
        .and_then(|c| c.get("schema"));
    schema.map(first_ref_name).unwrap_or_default()
}

fn response_codes(op: &Value) -> Vec<String> {
    let mut codes: Vec<String> = op
        .get("responses")
        .and_then(Value::as_mapping)
        .map(|m| {
            m.iter()
                .filter_map(|(k, _)| k.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default();
    codes.sort();
    codes
}

/// parse_openapi parses one YAML file's bytes into operations + schemas (== Go Parse).
/// release is not a parse input — the ingest attaches it from the on-disk
/// <Rel>/<sha>/<file> layout (cite-or-silent; never inferred from the YAML).
pub fn parse_openapi(data: &str, filename: &str, sha: &str) -> Result<OpenApiResult, String> {
    let doc: Value = serde_yaml::from_str(data).map_err(|e| format!("{filename}: {e}"))?;

    let (spec_id, version) = identify(&doc, filename);
    if spec_id.is_empty() {
        return Err(format!(
            "{filename}: cannot determine spec_id (no externalDocs, no TS<nnnnn> filename)"
        ));
    }
    let (service, family) = service_from_file(filename);
    let api_root = api_root_from_servers(&doc);
    let forge_url = forge_raw_url(filename, sha, "");
    let api_doc_version = doc
        .get("info")
        .and_then(|i| i.get("version"))
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_string();

    let mut operations = Vec::new();
    if let Some(paths) = doc.get("paths").and_then(Value::as_mapping) {
        let mut path_keys: Vec<&str> = paths.iter().filter_map(|(k, _)| k.as_str()).collect();
        path_keys.sort();
        for p in path_keys {
            let item = paths.get(Value::from(p)).unwrap();
            let Some(item_map) = item.as_mapping() else {
                continue;
            };
            let mut methods: Vec<&str> = item_map
                .iter()
                .filter_map(|(k, _)| k.as_str())
                .filter(|m| HTTP_METHODS.contains(&m.to_lowercase().as_str()))
                .collect();
            methods.sort();
            for m in methods {
                let op = item_map.get(Value::from(m)).unwrap();
                let tags = op
                    .get("tags")
                    .and_then(Value::as_sequence)
                    .map(|s| {
                        s.iter()
                            .filter_map(|t| t.as_str().map(String::from))
                            .collect()
                    })
                    .unwrap_or_default();
                operations.push(ParsedOperation {
                    spec_id: spec_id.clone(),
                    version: version.clone(),
                    api_doc_version: api_doc_version.clone(),
                    service: service.clone(),
                    service_family: family.clone(),
                    api_root: api_root.clone(),
                    path: p.to_string(),
                    method: m.to_uppercase(),
                    operation_id: str_at(op, "operationId").to_string(),
                    summary: str_at(op, "summary").to_string(),
                    tags,
                    request_schema: request_schema(op),
                    response_codes: response_codes(op),
                    yaml_file: filename.to_string(),
                    forge_sha: sha.to_string(),
                    forge_url: forge_url.clone(),
                });
            }
        }
    }

    let mut schemas = Vec::new();
    if let Some(comp) = doc
        .get("components")
        .and_then(|c| c.get("schemas"))
        .and_then(Value::as_mapping)
    {
        let mut names: Vec<&str> = comp.iter().filter_map(|(k, _)| k.as_str()).collect();
        names.sort();
        for n in names {
            let node = comp.get(Value::from(n)).unwrap();
            let mut sc = schema_from(n, node, &spec_id);
            sc.spec_id = spec_id.clone();
            sc.version = version.clone();
            sc.service = service.clone();
            sc.yaml_file = filename.to_string();
            sc.forge_sha = sha.to_string();
            sc.forge_url = forge_raw_url(filename, sha, n);
            schemas.push(sc);
        }
    }

    Ok(OpenApiResult {
        spec_id,
        version,
        operations,
        schemas,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const YAML: &str = r##"
openapi: 3.0.0
info:
  version: 1.2.3
externalDocs:
  description: "3GPP TS 29.518 V18.13.0; 5G System; Access and Mobility Management Services"
servers:
  - url: "{apiRoot}/namf-comm/v1"
paths:
  /ue-contexts/{ueContextId}:
    put:
      operationId: CreateUEContext
      summary: Create a UE context
      tags: [ Individual UE Context ]
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/UeContextCreateData"
      responses:
        "201": {}
        "400": {}
components:
  schemas:
    UeContextCreateData:
      type: object
      description: Data for create
      properties:
        ueContext:
          $ref: "#/components/schemas/UeContext"
        n2Info:
          $ref: "TS29571_CommonData.yaml#/components/schemas/RefToBinaryData"
    AccessType:
      anyOf:
        - type: string
          enum: [ "3GPP_ACCESS", "NON_3GPP_ACCESS" ]
"##;

    #[test]
    fn parses_operations_and_schemas_like_go() {
        let r = parse_openapi(YAML, "TS29518_Namf_Communication.yaml", "abc123").unwrap();
        assert_eq!(r.spec_id, "29.518");
        assert_eq!(r.version, "18.13.0");

        assert_eq!(r.operations.len(), 1);
        let op = &r.operations[0];
        assert_eq!(op.method, "PUT");
        assert_eq!(op.path, "/ue-contexts/{ueContextId}");
        assert_eq!(op.operation_id, "CreateUEContext");
        assert_eq!(op.service, "Namf_Communication");
        assert_eq!(op.service_family, "Namf");
        assert_eq!(op.api_root, "namf-comm/v1");
        assert_eq!(op.api_doc_version, "1.2.3");
        assert_eq!(op.request_schema, "UeContextCreateData");
        assert_eq!(op.response_codes, vec!["201", "400"]);
        assert_eq!(
            op.forge_url,
            "https://forge.3gpp.org/rep/all/5G_APIs/-/raw/abc123/TS29518_Namf_Communication.yaml"
        );

        // schemas sorted by name: AccessType, UeContextCreateData
        assert_eq!(r.schemas.len(), 2);
        assert_eq!(r.schemas[0].schema_name, "AccessType");
        assert_eq!(r.schemas[0].kind, "enum");
        assert_eq!(
            r.schemas[0].enum_values,
            vec!["3GPP_ACCESS", "NON_3GPP_ACCESS"]
        );
        let ucd = &r.schemas[1];
        assert_eq!(ucd.schema_name, "UeContextCreateData");
        assert_eq!(ucd.kind, "object");
        assert_eq!(ucd.properties, vec!["ueContext", "n2Info"]);
        // same-file ref → 29.518:UeContext ; cross-file ref → 29.571:RefToBinaryData (sorted)
        assert_eq!(
            ucd.refs_out,
            vec!["29.518:UeContext", "29.571:RefToBinaryData"]
        );
        assert_eq!(ucd.forge_url, "https://forge.3gpp.org/rep/all/5G_APIs/-/raw/abc123/TS29518_Namf_Communication.yaml#/components/schemas/UeContextCreateData");
    }

    #[test]
    fn no_spec_id_errors() {
        assert!(parse_openapi("openapi: 3.0.0\npaths: {}\n", "random.yaml", "s").is_err());
    }
}
