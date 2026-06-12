package main

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSchemeOr pins the proxy-aware scheme resolution: behind a TLS-terminating
// proxy (Cloudflare) the connection is plain HTTP, so X-Forwarded-Proto must win
// or every advertised URL would say http:// on an https:// deployment.
func TestSchemeOr(t *testing.T) {
	cases := []struct {
		name, xfp string
		tls       bool
		want      string
	}{
		{"direct http", "", false, "http"},
		{"direct tls", "", true, "https"},
		{"cloudflare https", "https", false, "https"},
		{"proxy chain takes first hop", "https, http", false, "https"},
		{"explicit http proxy", "http", true, "http"},
		{"garbage header ignored", "ws", false, "http"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/llms.txt", nil)
		if c.xfp != "" {
			r.Header.Set("X-Forwarded-Proto", c.xfp)
		}
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := schemeOr(r); got != c.want {
			t.Errorf("%s: schemeOr=%q, want %q", c.name, got, c.want)
		}
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "labs.making.codes"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := originOf(r); got != "https://labs.making.codes" {
		t.Errorf("originOf=%q, want https://labs.making.codes", got)
	}
}

// TestOriginSubstitutionComplete guards against a placeholder regression: every
// rendered doc must carry the caller's origin and zero leftover {{ORIGIN}} (or
// the old hardcoded http://{{…}} shape).
func TestOriginSubstitutionComplete(t *testing.T) {
	for name, doc := range map[string]string{"aiPrompt": aiPrompt, "helpJSON": helpJSON} {
		out := substOrigin(doc, "https://labs.making.codes")
		if strings.Contains(out, "{{ORIGIN}}") {
			t.Errorf("%s: unsubstituted {{ORIGIN}} remains", name)
		}
		if !strings.Contains(out, "https://labs.making.codes/mcp") {
			t.Errorf("%s: expected the substituted https endpoint", name)
		}
		if strings.Contains(out, "http://{{") {
			t.Errorf("%s: hardcoded http:// scheme crept back in", name)
		}
	}
}

// TestSkillIsARealSkill pins the SKILL.md contract: YAML frontmatter (so Claude
// Code picks it up from ~/.claude/skills/3gpp/SKILL.md) + the deep-research
// protocol the answers must follow.
func TestSkillIsARealSkill(t *testing.T) {
	if !strings.HasPrefix(skill3gpp, "---\nname: 3gpp\n") {
		t.Error("skill must start with YAML frontmatter (name: 3gpp)")
	}
	for _, want := range []string{
		"description:",
		"Deep-research protocol",
		"never answer from a single tool call",
		"find_cross_references",
		"┌─ MÉTA",
	} {
		if !strings.Contains(skill3gpp, want) {
			t.Errorf("skill missing %q", want)
		}
	}
}
