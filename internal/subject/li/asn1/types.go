package asn1

import (
	"regexp"
	"strconv"
	"strings"
)

// Member is one element of a structured ASN.1 type (SEQUENCE/CHOICE/SET arm, or
// an ENUMERATED literal — literals carry only a name).
type Member struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Tag      int    `json:"tag,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

// TypeDef is one top-level ASN.1 type assignment (axis #8): the full catalogue,
// not just the LI event CHOICEs, so every IE (FiveGGUTI, SUPI, Location, …) is a
// structured, citable object.
type TypeDef struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // SEQUENCE|CHOICE|ENUMERATED|SET|SEQUENCE OF|INTEGER|alias|...
	Members []Member `json:"members,omitempty"`
}

var (
	reAssign = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)\s*::=\s*(.+)$`)
	reEnum   = regexp.MustCompile(`^\s*([a-zA-Z][A-Za-z0-9-]*)\s*(?:\(\s*\d+\s*\))?\s*,?\s*$`)
)

// structuredKind reports the canonical kind keyword if rhs opens a brace-
// delimited body we should read members from (SEQUENCE/CHOICE/SET/ENUMERATED),
// and false for "SEQUENCE OF"/"SET OF" (single anonymous element, no named
// members) and leaf/alias types.
func structuredKind(rhs string) (string, bool) {
	switch {
	case strings.HasPrefix(rhs, "SEQUENCE OF"), strings.HasPrefix(rhs, "SET OF"):
		return strings.Fields(rhs)[0] + " OF", false
	case strings.HasPrefix(rhs, "SEQUENCE"):
		return "SEQUENCE", true
	case strings.HasPrefix(rhs, "CHOICE"):
		return "CHOICE", true
	case strings.HasPrefix(rhs, "ENUMERATED"):
		return "ENUMERATED", true
	case strings.HasPrefix(rhs, "SET"):
		return "SET", true
	}
	// leaf or alias: kind = first token (INTEGER, UTF8String, or a referenced
	// type name for an alias). Re-join the two-word built-ins.
	f := strings.Fields(rhs)
	if len(f) == 0 {
		return "", false
	}
	switch f[0] {
	case "BIT", "OCTET":
		if len(f) > 1 && f[1] == "STRING" {
			return f[0] + " STRING", false
		}
	case "OBJECT":
		if len(f) > 1 && f[1] == "IDENTIFIER" {
			return "OBJECT IDENTIFIER", false
		}
	}
	return f[0], false
}

// parseAllTypes scans every top-level `Name ::= …` assignment. Structured types
// have their members read with brace-depth tracking (same model as the event
// scanner); leaf/alias types are recorded with kind only.
func parseAllTypes(lines []string) []TypeDef {
	var out []TypeDef
	i := 0
	for i < len(lines) {
		m := reAssign.FindStringSubmatch(strings.TrimRight(lines[i], " \t"))
		if m == nil {
			i++
			continue
		}
		kind, structured := structuredKind(strings.TrimSpace(m[2]))
		td := TypeDef{Name: m[1], Kind: kind}
		if !structured {
			out = append(out, td)
			i++
			continue
		}
		members, end := readTypeBlock(lines, i, kind)
		td.Members = members
		out = append(out, td)
		if end > i {
			i = end + 1
		} else {
			i++
		}
	}
	return out
}

// readTypeBlock reads a brace-delimited type body starting at the assignment
// line, returning its members and the index of the closing-brace line.
func readTypeBlock(lines []string, start int, kind string) ([]Member, int) {
	var members []Member
	opened := false
	depth := 0
	for i := start; i < len(lines); i++ {
		line := lines[i]
		o, c := strings.Count(line, "{"), strings.Count(line, "}")
		depth += o - c
		if o > 0 {
			opened = true
		}
		if opened {
			if mm := reMember.FindStringSubmatch(line); mm != nil {
				tag, _ := strconv.Atoi(mm[2])
				members = append(members, Member{
					Name: mm[1], Type: mm[3], Tag: tag,
					Optional: strings.Contains(line, "OPTIONAL"),
				})
			} else if kind == "ENUMERATED" && i > start {
				if en := reEnum.FindStringSubmatch(line); en != nil {
					members = append(members, Member{Name: en[1]})
				}
			}
		}
		if opened && depth <= 0 {
			return members, i
		}
		if !opened && i > start+3 { // no brace found nearby: treat as leaf
			return nil, start
		}
	}
	return members, len(lines) - 1
}
