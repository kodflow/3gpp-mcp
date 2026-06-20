//! asn1 — Rust port of internal/subject/li/asn1 (Phase 8b). A focused, dependency-free
//! scanner for the TS 33.128 ASN.1 module (TS33128Payloads.asn): it extracts the LI event
//! registry — the four interface CHOICEs (X2/X3/HI2/HI4), each event's originating NF +
//! spec clause + SEQUENCE fields — plus the full top-level type catalogue. NOT a general
//! ASN.1 compiler; scope is the citable LI registry. Mirrors the Go scanner line-for-line.

use regex::Regex;
use serde::Serialize;
use std::sync::OnceLock;

/// One LI event (a member of an interface CHOICE).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Event {
    pub interface: String,
    pub name: String,
    pub asn1_type: String,
    pub tag: i32,
    pub nf: String,
    pub domain: String,
    pub clause: String,
    pub field_count: i32,
}

/// One IE inside an event's SEQUENCE.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Field {
    pub interface: String,
    pub event_name: String,
    pub field_name: String,
    pub asn1_type: String,
    pub tag: i32,
    pub optional: bool,
    pub ordinal: i32,
}

/// Anchors an NF/interface to its spec clause.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NFClause {
    pub nf: String,
    pub interface: String,
    pub clause: String,
}

/// One element of a structured ASN.1 type (serialised to the asn1_types.members JSON).
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Member {
    pub name: String,
    #[serde(rename = "type", skip_serializing_if = "String::is_empty")]
    pub asn1_type: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub tag: i32,
    #[serde(skip_serializing_if = "is_false")]
    pub optional: bool,
}
fn is_zero(n: &i32) -> bool {
    *n == 0
}
fn is_false(b: &bool) -> bool {
    !*b
}

/// One top-level ASN.1 type assignment (the full catalogue, axis #8).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TypeDef {
    pub name: String,
    pub kind: String,
    pub members: Vec<Member>,
}

/// The parsed module.
#[derive(Debug, Default)]
pub struct Module {
    pub release: String,
    pub module_version: String,
    pub events: Vec<Event>,
    pub fields: Vec<Field>,
    pub nf_clauses: Vec<NFClause>,
    pub types: Vec<TypeDef>,
}

// container CHOICE name → interface label (fixed order for deterministic output).
const CONTAINERS: &[(&str, &str)] = &[
    ("XIRIEvent", "X2"),
    ("CCPDU", "X3"),
    ("IRIEvent", "HI2"),
    ("LINotificationMessage", "HI4"),
];

// NE/NF acronyms used as event-type prefixes, longest-first so the longest match wins.
const KNOWN_NF: &[&str] = &[
    "SMSF", "AUSF", "N9HR", "S8HR", "AAnF", "SCEF", "PDHR", "PDSR", "LALS", "5GMSAF", "AMF", "SMF",
    "UPF", "UDM", "LMF", "NEF", "NRF", "MME", "SGW", "PGW", "HSS", "EES", "MMS", "PTC", "RCS",
    "IMS", "SMS", "AF",
];

fn re_oid() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"ts33128\(19\)\s+r(\d+)\(\d+\)\s+version(\d+)\(\d+\)").unwrap())
}
fn re_member() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(r"^\s*([a-z][A-Za-z0-9]*)\s+\[(\d+)\]\s+([A-Za-z][A-Za-z0-9-]*)").unwrap()
    })
}
fn re_clause() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"clause\s+([0-9]+[0-9A-Za-z.]*)").unwrap())
}
fn re_nf_cmt() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"^\s*--\s*([A-Z][A-Za-z0-9/-]*)\s+events").unwrap())
}
fn re_assign() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"^([A-Za-z][A-Za-z0-9-]*)\s*::=\s*(.+)$").unwrap())
}
fn re_enum() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(r"^\s*([a-zA-Z][A-Za-z0-9-]*)\s*(?:\(\s*\d+\s*\))?\s*,?\s*$").unwrap()
    })
}

fn count(line: &str, c: char) -> i32 {
    line.matches(c).count() as i32
}

fn nf_from_type(typ: &str) -> &'static str {
    for &nf in KNOWN_NF {
        if typ.starts_with(nf) {
            return nf;
        }
    }
    ""
}

fn domain_of(nf: &str) -> &'static str {
    match nf {
        "AMF" | "SMF" | "UPF" | "UDM" | "AUSF" | "LMF" | "NEF" | "NRF" | "SMSF" | "5GMSAF"
        | "AAnF" | "EES" => "5GC",
        "MME" | "SGW" | "PGW" | "SCEF" => "EPC",
        "IMS" | "RCS" | "PTC" | "MMS" | "HSS" => "IMS",
        _ => "",
    }
}

/// find_def locates `<name> ::= <kind>` and returns the line index + the brace depth after
/// that line (== Go findDef).
fn find_def(lines: &[String], name: &str, kind: &str) -> (i64, i32) {
    let re = Regex::new(&format!(r"^{}\s*::=\s*{}\b", regex::escape(name), kind)).unwrap();
    for (i, l) in lines.iter().enumerate() {
        if re.is_match(l) {
            return (i as i64, count(l, '{') - count(l, '}'));
        }
    }
    (-1, 0)
}

fn parse_choice(lines: &[String], name: &str, iface: &str) -> Vec<Event> {
    let (start, mut depth) = find_def(lines, name, "CHOICE");
    if start < 0 {
        return Vec::new();
    }
    let start = start as usize;
    let mut out = Vec::new();
    let (mut cur_nf, mut cur_clause) = (String::new(), String::new());
    for (i, line) in lines.iter().enumerate().skip(start) {
        depth += count(line, '{') - count(line, '}');
        if let Some(c) = re_nf_cmt().captures(line) {
            cur_nf = c[1].to_uppercase();
        }
        if let Some(cl) = re_clause().captures(line) {
            if line.contains("--") {
                cur_clause = cl[1].to_string();
            }
        }
        if let Some(mm) = re_member().captures(line) {
            let tag: i32 = mm[2].parse().unwrap_or(0);
            let typ = mm[3].to_string();
            let mut nf = nf_from_type(&typ).to_string();
            if nf.is_empty() {
                nf = cur_nf.clone();
            }
            let domain = domain_of(&nf).to_string();
            out.push(Event {
                interface: iface.to_string(),
                name: mm[1].to_string(),
                asn1_type: typ,
                tag,
                nf,
                domain,
                clause: cur_clause.clone(),
                field_count: 0,
            });
        }
        if depth <= 0 && i > start {
            break;
        }
    }
    out
}

fn parse_sequence(lines: &[String], typ: &str, iface: &str) -> Vec<Field> {
    let (start, mut depth) = find_def(lines, typ, "SEQUENCE");
    if start < 0 {
        return Vec::new();
    }
    let start = start as usize;
    let mut out = Vec::new();
    let mut ord = 0;
    for (i, line) in lines.iter().enumerate().skip(start) {
        depth += count(line, '{') - count(line, '}');
        if let Some(mm) = re_member().captures(line) {
            let tag: i32 = mm[2].parse().unwrap_or(0);
            ord += 1;
            out.push(Field {
                interface: iface.to_string(),
                event_name: typ.to_string(),
                field_name: mm[1].to_string(),
                asn1_type: mm[3].to_string(),
                tag,
                optional: line.contains("OPTIONAL"),
                ordinal: ord,
            });
        }
        if depth <= 0 && i > start {
            break;
        }
    }
    out
}

fn structured_kind(rhs: &str) -> (String, bool) {
    if rhs.starts_with("SEQUENCE OF") || rhs.starts_with("SET OF") {
        return (
            format!("{} OF", rhs.split_whitespace().next().unwrap_or("")),
            false,
        );
    }
    if rhs.starts_with("SEQUENCE") {
        return ("SEQUENCE".into(), true);
    }
    if rhs.starts_with("CHOICE") {
        return ("CHOICE".into(), true);
    }
    if rhs.starts_with("ENUMERATED") {
        return ("ENUMERATED".into(), true);
    }
    if rhs.starts_with("SET") {
        return ("SET".into(), true);
    }
    let f: Vec<&str> = rhs.split_whitespace().collect();
    if f.is_empty() {
        return (String::new(), false);
    }
    match f[0] {
        "BIT" | "OCTET" if f.get(1) == Some(&"STRING") => (format!("{} STRING", f[0]), false),
        "OBJECT" if f.get(1) == Some(&"IDENTIFIER") => ("OBJECT IDENTIFIER".into(), false),
        _ => (f[0].to_string(), false),
    }
}

fn read_type_block(lines: &[String], start: usize, kind: &str) -> (Vec<Member>, usize) {
    let mut members = Vec::new();
    let mut opened = false;
    let mut depth = 0;
    for (i, line) in lines.iter().enumerate().skip(start) {
        let (o, c) = (count(line, '{'), count(line, '}'));
        depth += o - c;
        if o > 0 {
            opened = true;
        }
        if opened {
            if let Some(mm) = re_member().captures(line) {
                let tag: i32 = mm[2].parse().unwrap_or(0);
                members.push(Member {
                    name: mm[1].to_string(),
                    asn1_type: mm[3].to_string(),
                    tag,
                    optional: line.contains("OPTIONAL"),
                });
            } else if kind == "ENUMERATED" && i > start {
                if let Some(en) = re_enum().captures(line) {
                    members.push(Member {
                        name: en[1].to_string(),
                        asn1_type: String::new(),
                        tag: 0,
                        optional: false,
                    });
                }
            }
        }
        if opened && depth <= 0 {
            return (members, i);
        }
        if !opened && i > start + 3 {
            return (Vec::new(), start);
        }
    }
    (members, lines.len() - 1)
}

fn parse_all_types(lines: &[String]) -> Vec<TypeDef> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < lines.len() {
        let trimmed = lines[i].trim_end_matches([' ', '\t']);
        let Some(m) = re_assign().captures(trimmed) else {
            i += 1;
            continue;
        };
        let (kind, structured) = structured_kind(m[2].trim());
        let name = m[1].to_string();
        if !structured {
            out.push(TypeDef {
                name,
                kind,
                members: Vec::new(),
            });
            i += 1;
            continue;
        }
        let (members, end) = read_type_block(lines, i, &kind);
        out.push(TypeDef {
            name,
            kind,
            members,
        });
        if end > i {
            i = end + 1;
        } else {
            i += 1;
        }
    }
    out
}

/// parse_module scans one TS33128Payloads.asn text (== Go ParseModule). Err when no LI
/// events are found (not a payloads module).
pub fn parse_module(text: &str) -> Result<Module, String> {
    let lines: Vec<String> = text.lines().map(String::from).collect();
    let mut m = Module::default();
    for l in lines.iter().take(10) {
        if let Some(mm) = re_oid().captures(l) {
            m.release = format!("Rel-{}", &mm[1]);
            m.module_version = format!("r{} version{}", &mm[1], &mm[2]);
            break;
        }
    }

    let mut seen_nf_clause = std::collections::HashSet::new();
    for &(cont, iface) in CONTAINERS {
        let evs = parse_choice(&lines, cont, iface);
        for e in &evs {
            let key = format!("{}|{}", e.nf, iface);
            if !e.clause.is_empty() && seen_nf_clause.insert(key) {
                m.nf_clauses.push(NFClause {
                    nf: e.nf.clone(),
                    interface: iface.to_string(),
                    clause: e.clause.clone(),
                });
            }
        }
        m.events.extend(evs);
    }

    // fields per distinct event type (first-seen interface).
    let mut type_of: std::collections::HashMap<String, String> = std::collections::HashMap::new();
    let mut order = Vec::new();
    for e in &m.events {
        if !type_of.contains_key(&e.asn1_type) {
            type_of.insert(e.asn1_type.clone(), e.interface.clone());
            order.push(e.asn1_type.clone());
        }
    }
    let mut field_count: std::collections::HashMap<String, i32> = std::collections::HashMap::new();
    for typ in &order {
        let fs = parse_sequence(&lines, typ, &type_of[typ]);
        field_count.insert(typ.clone(), fs.len() as i32);
        m.fields.extend(fs);
    }
    for e in &mut m.events {
        e.field_count = *field_count.get(&e.asn1_type).unwrap_or(&0);
    }

    m.types = parse_all_types(&lines);
    if m.events.is_empty() {
        return Err("no LI events found (not a TS33128Payloads module?)".to_string());
    }
    Ok(m)
}

/// members_json serialises a type's members to the asn1_types.members JSON string.
pub fn members_json(members: &[Member]) -> String {
    serde_json::to_string(members).unwrap_or_else(|_| "[]".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    const ASN: &str = r#"TS33128Payloads {itu-t(0) identified-organization(4) etsi(0) mobileDomain(0)
  threeGPPSA3LI(33128) ts33128(19) r18(18) version14(14)}
DEFINITIONS AUTOMATIC TAGS ::= BEGIN

XIRIEvent ::= CHOICE
{
  -- AMF events, see clause 6.2.2
  registration [1] AMFRegistration,
  deregistration [2] AMFDeregistration,
  -- SMF events, see clause 6.2.3
  pduSessionEstablishment [3] SMFPDUSessionEstablishment
}

AMFRegistration ::= SEQUENCE
{
  registrationType [1] RegistrationType,
  supi [2] SUPI OPTIONAL
}

RegistrationType ::= ENUMERATED
{
  initial(1),
  mobility(2)
}
END
"#;

    #[test]
    fn parses_li_registry() {
        let m = parse_module(ASN).unwrap();
        assert_eq!(m.release, "Rel-18");
        assert_eq!(m.module_version, "r18 version14");
        assert_eq!(m.events.len(), 3);
        let reg = &m.events[0];
        assert_eq!(reg.interface, "X2");
        assert_eq!(reg.name, "registration");
        assert_eq!(reg.asn1_type, "AMFRegistration");
        assert_eq!(reg.nf, "AMF");
        assert_eq!(reg.domain, "5GC");
        assert_eq!(reg.clause, "6.2.2");
        assert_eq!(reg.field_count, 2); // AMFRegistration has 2 fields
                                        // SMF event uses its own NF prefix.
        assert_eq!(m.events[2].nf, "SMF");

        // fields of AMFRegistration.
        let amf_fields: Vec<&Field> = m
            .fields
            .iter()
            .filter(|f| f.event_name == "AMFRegistration")
            .collect();
        assert_eq!(amf_fields.len(), 2);
        assert_eq!(amf_fields[0].field_name, "registrationType");
        assert!(amf_fields[1].optional);

        // type catalogue includes the ENUMERATED with its literals.
        let rt = m
            .types
            .iter()
            .find(|t| t.name == "RegistrationType")
            .unwrap();
        assert_eq!(rt.kind, "ENUMERATED");
        assert_eq!(
            rt.members
                .iter()
                .map(|x| x.name.as_str())
                .collect::<Vec<_>>(),
            vec!["initial", "mobility"]
        );

        // nf_clauses anchored AMF/X2 → 6.2.2.
        assert!(m
            .nf_clauses
            .iter()
            .any(|c| c.nf == "AMF" && c.interface == "X2" && c.clause == "6.2.2"));
    }

    #[test]
    fn non_payloads_errors() {
        assert!(parse_module("Foo ::= SEQUENCE { bar [1] INTEGER }\n").is_err());
    }
}
