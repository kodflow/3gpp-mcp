package etsicat

import (
	"io"
	"strings"
	"testing"
)

// fakeFetch maps a URL prefix (the spec dir) to a listing body, so the crawl is tested
// with zero network. The keys are the version-less deliver dir URLs ResolveLatest builds.
func fakeFetch(t *testing.T, listings map[string]string) Fetcher {
	t.Helper()
	return func(url string) (io.ReadCloser, error) {
		body, ok := listings[url]
		if !ok {
			t.Fatalf("unexpected fetch URL %q", url)
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}
}

func listing(versionDirs ...string) string {
	var b strings.Builder
	b.WriteString(`<html><body><a href="../">Parent</a>`)
	for _, d := range versionDirs {
		b.WriteString(`<a href="` + d + `">` + d + `</a>`)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func TestResolveLatestAndBuildSite(t *testing.T) {
	// dir URLs ResolveLatest derives from the ids (model.EtsiDeliverURL(id,"")):
	dir1 := "https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/" // 103 221-1
	dir2 := "https://www.etsi.org/deliver/etsi_ts/103200_103299/103280/"   // 103 280
	dir3 := "https://www.etsi.org/deliver/etsi_ts/103100_103199/10312001/" // 103 120-1

	fetch := fakeFetch(t, map[string]string{
		// 103 221-1: latest published = 1.21.1 (1.22.0 is a draft milestone 30).
		dir1: listing("01.01.01_60/", "01.21.01_60/", "01.22.00_30/"),
		// 103 280: only 2.1.1 published.
		dir2: listing("02.01.01_60/"),
		// 103 120-1: drafts only → no published version.
		dir3: listing("01.00.00_20/", "01.01.00_30/"),
	})

	if v, ok, err := ResolveLatest(fetch, "103 221-1"); err != nil || !ok || v != "1.21.1" {
		t.Fatalf("ResolveLatest(103 221-1) = (%q,%v,%v), want (1.21.1,true,nil)", v, ok, err)
	}
	if _, ok, _ := ResolveLatest(fetch, "103 120-1"); ok {
		t.Error("103 120-1 has only drafts → ok should be false")
	}

	site, failed := BuildSite(fetch, []string{"103 221-1", "103 280", "103 120-1"})
	if len(failed) != 0 {
		t.Errorf("unexpected failures: %v", failed)
	}
	if site["103 221-1"] != "1.21.1" || site["103 280"] != "2.1.1" {
		t.Errorf("site = %v, want 103 221-1=1.21.1, 103 280=2.1.1", site)
	}
	if _, ok := site["103 120-1"]; ok {
		t.Error("103 120-1 (drafts only) must be omitted from the site map")
	}
}
