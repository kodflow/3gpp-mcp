package asn1

import (
	"strings"
	"testing"
)

// minimal TS33128Payloads-shaped fixture (embedded so the test runs without the
// corpus). Covers: module OID release, X2 CHOICE with two NF groups, a SEQUENCE
// with an OPTIONAL field, NF from type prefix vs functional group comment.
const fixture = `TS33128Payloads
{itu-t(0) identified-organization(4) etsi(0) securityDomain(2) lawfulIntercept(2) threeGPP(4) ts33128(19) r18(18) version14(14)}
DEFINITIONS IMPLICIT TAGS EXTENSIBILITY IMPLIED ::=
BEGIN

XIRIEvent ::= CHOICE
{
    -- AMF events, see clause 6.2.2.2
    registration   [1] AMFRegistration,
    deregistration [2] AMFDeregistration,
    -- PDU session-related events, see clause 6.2.3.2
    pDUSessionEstablishment [6] SMFPDUSessionEstablishment,
    -- MME events, see clause 6.3.2.2
    mMEAttach      [87] MMEAttach
}

CCPDU ::= CHOICE
{
    uPFCCPDU [1] UPFCCPDU
}

AMFRegistration ::= SEQUENCE
{
    sUPI     [1] SUPI,
    location [2] Location OPTIONAL,
    gUTI     [3] FiveGGUTI
}

AMFRegistrationType ::= ENUMERATED
{
    initialRegistration (0),
    mobilityRegistration (1),
    periodicRegistration (2)
}

SUPI ::= UTF8String

END
`

func TestParseModule(t *testing.T) {
	m, err := ParseModule(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if m.Release != "Rel-18" || m.ModuleVersion != "r18 version14" {
		t.Errorf("release=%q version=%q", m.Release, m.ModuleVersion)
	}
	byIface := map[string]int{}
	byNF := map[string]int{}
	var amfReg *Event
	for i := range m.Events {
		e := &m.Events[i]
		byIface[e.Interface]++
		byNF[e.NF]++
		if e.Type == "AMFRegistration" {
			amfReg = e
		}
	}
	if byIface["X2"] != 4 {
		t.Errorf("X2 events = %d, want 4", byIface["X2"])
	}
	if byIface["X3"] != 1 {
		t.Errorf("X3 events = %d, want 1", byIface["X3"])
	}
	// NF resolved from type prefix even when the comment is functional (SMF group
	// comment says "PDU session-related events", not "SMF").
	if byNF["SMF"] != 1 {
		t.Errorf("SMF events = %d, want 1 (NF from type prefix)", byNF["SMF"])
	}
	if byNF["AMF"] != 2 || byNF["MME"] != 1 {
		t.Errorf("NF counts wrong: %v", byNF)
	}
	if amfReg == nil || amfReg.Clause != "6.2.2.2" || amfReg.FieldCount != 3 {
		t.Errorf("AMFRegistration: %+v (want clause 6.2.2.2, 3 fields)", amfReg)
	}
	// field optionality
	var loc *Field
	for i := range m.Fields {
		if m.Fields[i].EventName == "AMFRegistration" && m.Fields[i].FieldName == "location" {
			loc = &m.Fields[i]
		}
	}
	if loc == nil || !loc.Optional {
		t.Errorf("location field should be OPTIONAL: %+v", loc)
	}
	// NF clause anchor captured
	if len(m.NFClauses) == 0 {
		t.Error("no NF clause anchors")
	}
}

// TestTypeCatalogue checks the full type pass (axis #8): structured types with
// members + kind, an ENUMERATED, and a leaf alias.
func TestTypeCatalogue(t *testing.T) {
	m, err := ParseModule(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]TypeDef{}
	for _, td := range m.Types {
		byName[td.Name] = td
	}
	if td := byName["AMFRegistration"]; td.Kind != "SEQUENCE" || len(td.Members) != 3 {
		t.Errorf("AMFRegistration type = %+v, want SEQUENCE/3", td)
	}
	if td := byName["XIRIEvent"]; td.Kind != "CHOICE" || len(td.Members) != 4 {
		t.Errorf("XIRIEvent type = %+v, want CHOICE/4", td)
	}
	if td := byName["AMFRegistrationType"]; td.Kind != "ENUMERATED" || len(td.Members) != 3 {
		t.Errorf("AMFRegistrationType = %+v, want ENUMERATED/3", td)
	}
	if td := byName["SUPI"]; td.Kind != "UTF8String" || len(td.Members) != 0 {
		t.Errorf("SUPI alias = %+v, want UTF8String/0", td)
	}
	// OPTIONAL carried on a member.
	for _, mb := range byName["AMFRegistration"].Members {
		if mb.Name == "location" && !mb.Optional {
			t.Error("AMFRegistration.location should be OPTIONAL")
		}
	}
}
