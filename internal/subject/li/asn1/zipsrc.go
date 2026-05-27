package asn1

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// payloadsName is the ASN.1 module file shipped in the 33.128 attachments zip.
const payloadsName = "TS33128Payloads.asn"

// ParseFromSpecZip opens a 33.128 spec zip, finds the nested
// "*-attachments.zip", extracts TS33128Payloads.asn and parses it. Returns a
// nil module + nil error path is NOT used: a missing attachment yields an error
// so callers can degrade (fall back to clause/prose) without blocking.
func ParseFromSpecZip(specZipPath string) (*Module, error) {
	zr, err := zip.OpenReader(specZipPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()

	// Match "attachment" and the recurring 3GPP misspelling "attachmens"
	// (e.g. 33128-j60-attachmens.zip in Rel-19); "attachm" covers both.
	var attach *zip.File
	for _, f := range zr.File {
		n := strings.ToLower(f.Name)
		if strings.Contains(n, "attachm") && strings.HasSuffix(n, ".zip") {
			attach = f
			break
		}
	}
	if attach == nil {
		return nil, fmt.Errorf("no attachments zip in %s", specZipPath)
	}
	rc, err := attach.Open()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return nil, err
	}
	inner, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	for _, f := range inner.File {
		if strings.EqualFold(baseName(f.Name), payloadsName) {
			pr, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = pr.Close() }()
			return ParseModule(pr)
		}
	}
	return nil, fmt.Errorf("%s not found in attachments of %s", payloadsName, specZipPath)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}
