package mcp

import (
	"strings"
	"testing"
)

// AN IDENTITY THAT COULD NOT BE READ IS NOT AN EMPTY IDENTITY (meta_reads.go).
//
// server_info read each half's embedding identity through GetMeta, which returns
// "" for a failed read: an ETSI half the server could not query was reported as
// `"embedding_model": ""` — "this corpus carries no vectors" — with nothing
// saying the server had not been able to ask. Three states, and each must be
// told apart: a stamped identity, an absent one (""), an unreadable one (null +
// read_errors). help's inventory reports the same keys and follows its own rule
// for a failed read ("unavailable: <error>").
func TestServerInfoTellsAnUnreadableIdentityFromAnEmptyOne(t *testing.T) {
	st, e := halves(t)
	if err := e.SetMeta("embedding_model", "38067f8c6efe"); err != nil {
		t.Fatal(err)
	}

	// Stamped: the value, and no read error.
	c, ctx := clientOver(t, st, e)
	out, _, _ := callAny(t, c, ctx, "server_info", map[string]any{})
	etsi, _ := out["etsi"].(map[string]any)
	if etsi["embedding_model"] != "38067f8c6efe" || etsi["read_errors"] != nil {
		t.Fatalf("control: a stamped ETSI identity must be reported as such; etsi = %v", etsi)
	}

	// Absent: "" — the corpus states nothing — and still no read error.
	bare := memStore(t)
	c, ctx = clientOver(t, st, bare)
	out, _, _ = callAny(t, c, ctx, "server_info", map[string]any{})
	etsi, _ = out["etsi"].(map[string]any)
	if v, present := etsi["embedding_model"]; !present || v != "" || etsi["read_errors"] != nil {
		t.Errorf("an absent identity is \"\" with no read error; etsi = %v", etsi)
	}

	// Unreadable: null, the error, and not "ok".
	// A real store, closed: GetMeta on it answers "" (the old report), and
	// every raw read fails.
	deadEtsi, _ := halves(t)
	_ = deadEtsi.SetMeta("embedding_model", "38067f8c6efe")
	_ = deadEtsi.Close()
	c, ctx = clientOver(t, st, deadEtsi)
	out, _, _ = callAny(t, c, ctx, "server_info", map[string]any{})
	etsi, _ = out["etsi"].(map[string]any)
	if v, present := etsi["embedding_model"]; !present || v != nil {
		t.Errorf("an unreadable identity must be null, not a value; embedding_model = %#v", v)
	}
	errs, _ := etsi["read_errors"].(map[string]any)
	if s, _ := errs["embedding_model"].(string); !strings.Contains(s, "database is closed") {
		t.Errorf("the read error must be reported; etsi = %v", etsi)
	}
	if etsi["embedding_model_ok"] != false {
		t.Errorf("an unreadable identity cannot be ok; etsi = %v", etsi)
	}
	out, _, _ = callAny(t, c, ctx, "help", map[string]any{})
	corpus, _ := out["corpus"].(map[string]any)
	inv, _ := corpus["etsi"].(map[string]any)
	if s, _ := inv["embedding_model"].(string); !strings.HasPrefix(s, "unavailable: ") {
		t.Errorf("help must say the ETSI identity could not be read; embedding_model = %#v", inv["embedding_model"])
	}

	// The 3GPP half's identity keys follow the same rule.
	dead3GPP, _ := halves(t)
	_ = dead3GPP.Close()
	c, ctx = clientOver(t, dead3GPP, e)
	out, _, _ = callAny(t, c, ctx, "server_info", map[string]any{})
	errs, _ = out["read_errors"].(map[string]any)
	for _, key := range []string{"embedding_model", "sparse_model", "embed_floor"} {
		if s, _ := errs[key].(string); !strings.Contains(s, "database is closed") {
			t.Errorf("3GPP %s: want its read error reported; read_errors = %v", key, errs)
		}
	}
	for _, field := range []string{"embedding_model_db", "sparse_model", "embed_floor"} {
		if v, present := out[field]; !present || v != nil {
			t.Errorf("3GPP %s must be null when unreadable; got %#v", field, v)
		}
	}
}
