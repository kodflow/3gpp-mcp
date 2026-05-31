<!-- updated: 2026-05-30T08:38:41Z -->
# internal/subject/li/asn1 — TS 33.128 ASN.1 Scanner

## Purpose

A focused, **dependency-free** scanner for the TS 33.128 ASN.1 module
(`TS33128Payloads.asn`) shipped inside every 33.128 spec zip's attachments. It is
NOT a general ASN.1 compiler: it extracts the LI event registry — the four
interface CHOICEs (X2/X3/HI2/HI4), each event's originating NF + spec clause, and
each event's SEQUENCE fields — the authoritative, citable source the
prose/heading mining only approximated.

## Structure

```text
asn1/
├── parse.go    # scanner: CHOICE members → Events; SEQUENCE → fields
├── types.go    # Event / type model
└── zipsrc.go   # locate + read the .asn from a spec zip's attachments
```

## Conventions

- Dependency-free by design — stdlib `bufio`/`regexp` only. Do not pull an ASN.1
  library; scope is the LI registry, not full ASN.1.
- Output feeds `li/asn1store.go`; it is the citable ground truth for `li_events`.
- `parse_test` pins the extraction against the real module shape.
