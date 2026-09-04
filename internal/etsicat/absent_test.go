package etsicat

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"testing"
)

// TestAbsentIsNotFailed — the crawl enumerates ids from the ETSI index that the
// /deliver archive never carried (historical ETS/I-ETS numbers: the whole
// 100000_100099 range folder is a 404 in all three trees, checked 2026-09-02).
// Reporting those as failures made the completeness summary promise 90 documents
// of work that can never complete, every run, forever.
//
// A transient error must STILL land in failed: a partition that swallows real
// failures into "absent" would hide a crawler regression as archive absence — the
// worse of the two mistakes, because it is silent.
func TestAbsentIsNotFailed(t *testing.T) {
	good := "https://www.etsi.org/deliver/etsi_ts/103200_103299/103280/"
	gone := "https://www.etsi.org/deliver/etsi_ts/100000_100099/100027/"

	fetch := func(url string) (io.ReadCloser, error) {
		switch url {
		case good:
			return io.NopCloser(strings.NewReader(
				`<html><body><a href="02.01.01_60/">02.01.01_60/</a></body></html>`)), nil
		case gone:
			return nil, fmt.Errorf("%w: GET %s", ErrNotInArchive, url)
		default:
			return nil, errors.New("connection reset by peer")
		}
	}

	ds := []Deliverable{
		{TypeDir: model.EtsiTypeTS, ID: "103 280"},
		{TypeDir: model.EtsiTypeTS, ID: "100 027"},
		{TypeDir: model.EtsiTypeTS, ID: "103 221"},
	}

	for _, tc := range []struct {
		name         string
		run          func() (int, []string, []string)
		wantResolved int
	}{
		{"BuildHistory", func() (int, []string, []string) {
			h, f, a := BuildHistory(fetch, ds)
			return len(h), f, a
		}, 1},
		{"BuildSiteIn", func() (int, []string, []string) {
			s, f, a := BuildSiteIn(fetch, ds)
			return len(s), f, a
		}, 1},
	} {
		n, failed, absent := tc.run()
		if n != tc.wantResolved {
			t.Errorf("%s resolved %d deliverable(s), want %d", tc.name, n, tc.wantResolved)
		}
		if len(absent) != 1 || absent[0] != "100 027" {
			t.Errorf("%s absent = %v, want [100 027] — a 404 is an answer, not a retry", tc.name, absent)
		}
		if len(failed) != 1 || failed[0] != "103 221" {
			t.Errorf("%s failed = %v, want [103 221] — a transient error must stay retryable", tc.name, failed)
		}
	}
}
