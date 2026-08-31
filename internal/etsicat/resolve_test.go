package etsicat

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// fakeFetch maps a URL prefix (the spec dir) to a listing body, so the crawl is tested
// with zero network. The keys are the version-less deliver dir URLs ResolveLatest builds.
//
// An unexpected URL is returned as an ERROR rather than t.Fatalf'd: BuildSite now
// resolves concurrently, and Fatalf outside the test goroutine is undefined. The
// caller surfaces it — an unexpected URL lands in BuildSite's failed list, which
// every test here asserts is empty.
func fakeFetch(t *testing.T, listings map[string]string) Fetcher {
	t.Helper()
	return func(url string) (io.ReadCloser, error) {
		body, ok := listings[url]
		if !ok {
			return nil, fmt.Errorf("unexpected fetch URL %q", url)
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

// TestBuildSiteResolvesEveryIDConcurrently covers the property the worker pool must
// not break: with far more ids than workers, every id still lands in the site map,
// nothing is dropped at the channel close, and the failed list stays deterministic.
// Run under -race it also pins that the shared map/slice writes are guarded.
//
// The count deliberately exceeds BuildSiteWorkers so the pool actually cycles.
func TestBuildSiteResolvesEveryIDConcurrently(t *testing.T) {
	const n = BuildSiteWorkers * 7

	listings := map[string]string{}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		// 103 200 … : ids in one range folder, each with its own published version.
		id := fmt.Sprintf("103 %03d", 200+i)
		ids = append(ids, id)
		listings[fmt.Sprintf("https://www.etsi.org/deliver/etsi_ts/103200_103299/103%03d/", 200+i)] =
			listing(fmt.Sprintf("0%d.01.01_60/", i%9+1))
	}
	// One id whose directory is missing entirely → a failure, so the failed path is
	// exercised under concurrency rather than only the happy one.
	ids = append(ids, "103 999")

	site, failed := BuildSite(fakeFetch(t, listings), ids)

	if len(site) != n {
		t.Errorf("site has %d entries, want %d", len(site), n)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("103 %03d", 200+i)
		want := fmt.Sprintf("%d.1.1", i%9+1)
		if site[id] != want {
			t.Errorf("site[%q] = %q, want %q", id, site[id], want)
		}
	}
	if len(failed) != 1 || failed[0] != "103 999" {
		t.Errorf("failed = %v, want [103 999]", failed)
	}
}

// TestBuildSiteEmpty — the pool must not deadlock or panic on no work.
func TestBuildSiteEmpty(t *testing.T) {
	site, failed := BuildSite(fakeFetch(t, map[string]string{}), nil)
	if len(site) != 0 || len(failed) != 0 {
		t.Errorf("BuildSite(nil) = (%v, %v), want empty", site, failed)
	}
}
