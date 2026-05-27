// Package asn1 is a focused, dependency-free scanner for the TS 33.128
// ASN.1 module (TS33128Payloads.asn) shipped inside every 33.128 spec zip's
// attachments. It is NOT a general ASN.1 compiler: it extracts the LI event
// registry — the four interface CHOICEs (X2/X3/HI2/HI4), each event's
// originating NF + spec clause, and each event's SEQUENCE fields — which is the
// authoritative, citable source the prose/heading mining only approximated.
package asn1

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Event is one LI event: a member of an interface CHOICE.
type Event struct {
	Interface  string // X2 | X3 | HI2 | HI4
	Name       string // CHOICE member, e.g. "registration"
	Type       string // referenced type, e.g. "AMFRegistration"
	Tag        int    // CHOICE tag
	NF         string // originating NF, e.g. "AMF"
	Domain     string // 5GC | EPC | IMS | ...
	Clause     string // spec clause from the group comment, e.g. "6.2.2.2"
	FieldCount int
}

// Field is one IE inside an event's SEQUENCE.
type Field struct {
	Interface, EventName, FieldName, Type string
	Tag                                   int
	Optional                              bool
	Ordinal                               int
}

// NFClause anchors an NF/interface to its spec clause.
type NFClause struct{ NF, Interface, Clause string }

// Module is the parsed result.
type Module struct {
	Release       string // "Rel-18"
	ModuleVersion string // "r18 version14"
	Events        []Event
	Fields        []Field
	NFClauses     []NFClause
	Types         []TypeDef // full top-level type catalogue (axis #8)
}

// container CHOICE name -> interface label.
var containers = map[string]string{
	"XIRIEvent":             "X2",
	"CCPDU":                 "X3",
	"IRIEvent":              "HI2",
	"LINotificationMessage": "HI4",
}

// knownNF lists NE/NF acronyms used as event-type prefixes, longest-first so the
// longest match wins (AMF before AF, SMSF before SMS, N9HR before… ). The
// type-name prefix is the primary NF signal (group comments are functional in
// Rel-16/17), cross-checked by the group comment.
var knownNF = []string{
	"SMSF", "AUSF", "N9HR", "S8HR", "AAnF", "SCEF", "PDHR", "PDSR", "LALS",
	"5GMSAF", "AMF", "SMF", "UPF", "UDM", "LMF", "NEF", "NRF", "MME", "SGW",
	"PGW", "HSS", "EES", "MMS", "PTC", "RCS", "IMS", "SMS", "AF",
}

var (
	reOID    = regexp.MustCompile(`ts33128\(19\)\s+r(\d+)\(\d+\)\s+version(\d+)\(\d+\)`)
	reMember = regexp.MustCompile(`^\s*([a-z][A-Za-z0-9]*)\s+\[(\d+)\]\s+([A-Za-z][A-Za-z0-9-]*)`)
	reClause = regexp.MustCompile(`clause\s+([0-9]+[0-9A-Za-z.]*)`)
	reNFcmt  = regexp.MustCompile(`^\s*--\s*([A-Z][A-Za-z0-9/-]*)\s+events`)
)

// ParseModule scans one TS33128Payloads.asn text stream.
func ParseModule(r io.Reader) (*Module, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	m := &Module{}
	for _, l := range lines[:min(len(lines), 10)] {
		if mm := reOID.FindStringSubmatch(l); mm != nil {
			m.Release = "Rel-" + mm[1]
			m.ModuleVersion = "r" + mm[1] + " version" + mm[2]
			break
		}
	}
	seenNFClause := map[string]bool{}
	for cont, iface := range containers {
		evs := parseChoice(lines, cont, iface)
		m.Events = append(m.Events, evs...)
		for _, e := range evs {
			key := e.NF + "|" + iface
			if e.Clause != "" && !seenNFClause[key] {
				seenNFClause[key] = true
				m.NFClauses = append(m.NFClauses, NFClause{NF: e.NF, Interface: iface, Clause: e.Clause})
			}
		}
	}
	// fields per distinct event type
	typeOf := map[string]string{} // type -> interface (first seen)
	order := []string{}
	for _, e := range m.Events {
		if _, ok := typeOf[e.Type]; !ok {
			typeOf[e.Type] = e.Interface
			order = append(order, e.Type)
		}
	}
	fieldCount := map[string]int{}
	for _, typ := range order {
		fs := parseSequence(lines, typ, typeOf[typ])
		m.Fields = append(m.Fields, fs...)
		fieldCount[typ] = len(fs)
	}
	for i := range m.Events {
		m.Events[i].FieldCount = fieldCount[m.Events[i].Type]
	}
	m.Types = parseAllTypes(lines)
	if len(m.Events) == 0 {
		return nil, fmt.Errorf("no LI events found (not a TS33128Payloads module?)")
	}
	return m, nil
}

// parseChoice reads the members of `<name> ::= CHOICE { ... }`.
func parseChoice(lines []string, name, iface string) []Event {
	start, depth := findDef(lines, name, "CHOICE")
	if start < 0 {
		return nil
	}
	var out []Event
	curNF, curClause := "", ""
	for i := start; i < len(lines); i++ {
		line := lines[i]
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if c := reNFcmt.FindStringSubmatch(line); c != nil {
			curNF = strings.ToUpper(c[1])
		}
		if cl := reClause.FindStringSubmatch(line); cl != nil && strings.Contains(line, "--") {
			curClause = cl[1]
		}
		if mm := reMember.FindStringSubmatch(line); mm != nil {
			tag, _ := strconv.Atoi(mm[2])
			typ := mm[3]
			nf := nfFromType(typ)
			if nf == "" {
				nf = curNF
			}
			out = append(out, Event{
				Interface: iface, Name: mm[1], Type: typ, Tag: tag,
				NF: nf, Domain: domainOf(nf), Clause: curClause,
			})
		}
		if depth <= 0 && i > start {
			break
		}
	}
	return out
}

// parseSequence reads the members of `<typ> ::= SEQUENCE { ... }`.
func parseSequence(lines []string, typ, iface string) []Field {
	start, depth := findDef(lines, typ, "SEQUENCE")
	if start < 0 {
		return nil
	}
	var out []Field
	ord := 0
	for i := start; i < len(lines); i++ {
		line := lines[i]
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if mm := reMember.FindStringSubmatch(line); mm != nil {
			tag, _ := strconv.Atoi(mm[2])
			ord++
			out = append(out, Field{
				Interface: iface, EventName: typ, FieldName: mm[1], Type: mm[3],
				Tag: tag, Optional: strings.Contains(line, "OPTIONAL"), Ordinal: ord,
			})
		}
		if depth <= 0 && i > start {
			break
		}
	}
	return out
}

// findDef locates `<name> ::= <kind>` and returns the line index and the brace
// depth after consuming that line (so the caller continues from the next line).
func findDef(lines []string, name, kind string) (int, int) {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\s*::=\s*` + kind + `\b`)
	for i, l := range lines {
		if re.MatchString(l) {
			return i, strings.Count(l, "{") - strings.Count(l, "}")
		}
	}
	return -1, 0
}

// nfFromType returns the longest known NF that prefixes an event type name.
func nfFromType(typ string) string {
	for _, nf := range knownNF {
		if strings.HasPrefix(typ, nf) {
			return nf
		}
	}
	return ""
}

func domainOf(nf string) string {
	switch nf {
	case "AMF", "SMF", "UPF", "UDM", "AUSF", "LMF", "NEF", "NRF", "SMSF", "5GMSAF", "AAnF", "EES":
		return "5GC"
	case "MME", "SGW", "PGW", "SCEF":
		return "EPC"
	case "IMS", "RCS", "PTC", "MMS", "HSS":
		return "IMS"
	default:
		return ""
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
