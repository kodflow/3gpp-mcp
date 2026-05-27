package catalog

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Endpoint URLs (axis #3 §1). Both require a browser User-Agent — a bare curl/Go
// UA gets a 302 loop / 403.
const (
	StatusReportURL = "https://www.3gpp.org/dynareport?code=status-report.htm"
	ReleasesURL     = "https://portal.3gpp.org/Releases.aspx"
	browserUA       = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/124.0 Safari/537.36"
)

// Fetch GETs url with a browser UA, following redirects. Used for the one-shot
// catalog seed (internet only at ingestion time, never on the query path).
func Fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// FetchToCache fetches url and writes it under dir/name, returning the bytes.
// Caching the raw HTML keeps the seed reproducible (re-parse offline).
func FetchToCache(url, dir, name string) ([]byte, error) {
	b, err := Fetch(url)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return nil, err
		}
	}
	return b, nil
}
