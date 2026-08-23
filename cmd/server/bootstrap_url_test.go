package main

import (
	"strings"
	"testing"
)

// TestBootstrapURLsTargetTheLatestTag pins the one-character-class difference
// that made `mcp-3gpp bootstrap` fail for every user.
//
// GitHub exposes two similar-looking paths:
//
//	/releases/download/latest/<asset>   -> the release TAGGED "latest"
//	/releases/latest/download/<asset>   -> the ALIAS "most recent non-prerelease"
//
// This repository publishes the corpus on the release tagged `latest`, while the
// alias resolves to the `models` tag (ORT and BGE-M3 bundles, published later).
// The alias form therefore redirects to a release that does not carry the DB, and
// bootstrap 404s — which is precisely what happened until this was fixed.
func TestBootstrapURLsTargetTheLatestTag(t *testing.T) {
	for name, url := range map[string]string{
		"defaultDBURL":    defaultDBURL,
		"defaultDBSHAURL": defaultDBSHAURL,
	} {
		if strings.Contains(url, "/releases/latest/download/") {
			t.Errorf("%s uses the /releases/latest/download/ ALIAS (%s).\n"+
				"That alias resolves to the newest non-prerelease — the `models` tag here — "+
				"which does not carry the corpus, so bootstrap 404s. Use /releases/download/latest/.",
				name, url)
		}
		if !strings.Contains(url, "/releases/download/latest/") {
			t.Errorf("%s does not target the release tagged `latest`: %s", name, url)
		}
	}
}
