<!-- updated: 2026-05-30T08:38:41Z -->
# internal/subject/li — Lawful Interception Subject

## Purpose

The Lawful-Interception domain subject (TS 33.128). Owns the authoritative ASN.1
event registry + type catalogue, the clause-heuristic fallback, the `li_events`
MCP tool, and the `resolve_term` ASN.1 enrichment — everything LI, plugged into
the generic core via `internal/subject`.

## Structure

```text
li/
├── subject.go     # Subject impl: registration, li_events tool, resolve enrichment
├── catalog.go     # LI type catalogue (NF / interface ownership)
├── release.go     # release-scoped event availability
├── store.go       # LI-specific persistence over the generic store
├── asn1store.go   # persist the parsed ASN.1 registry
├── audit.go       # cross-check events vs normative text (drives cmd/li-audit)
├── x2events.go    # X2 interface event handling
└── asn1/          # dependency-free TS 33.128 ASN.1 scanner (see its CLAUDE.md)
```

## Conventions

- Owns spec id `33.128`. The **ASN.1 registry is the authoritative, citable
  source**; prose/heading mining is only a fallback (cite-or-silent, §1).
- The four LI interfaces are X2 / X3 / HI2 / HI4; events carry originating NF +
  spec clause.
- `audit.go` powers relocation: an event absent from its cited spec is relocated
  to its true spec/clause anywhere in the index (e.g. `RESTORE_DATA` → TS 29.002).
- `audit_test` / `li_events_test` / `x2events_test` pin the contract.
