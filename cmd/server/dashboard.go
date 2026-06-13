package main

// dashboard.go — the HTTP serve-mode dashboard: a live view of capabilities
// (ONNX embedder / semantic / HNSW / FTS / reranker), corpus counts, request
// throughput + latency (p50/p95/p99) AND process metrics (heap, goroutines), with
// the capability pills doubling as RUNTIME TOGGLES so an operator can A/B a
// retrieval arm and watch the impact. Pure stdlib + vanilla JS, inline SVG, no CDN.
//
// Auth: the page + data + toggle endpoints are gated by a random 20-char token
// generated at startup and printed to the container logs. Without it, no dashboard
// call passes (the /mcp data plane stays open — gating it would break MCP clients).
// Three routes: /dashboard, /dashboard.json, POST /dashboard/toggle.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/metrics"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// newDashToken returns a random 20-char alphanumeric token (crypto/rand).
func newDashToken() string {
	const n = 20
	const alpha = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is catastrophic; fall back to time-seeded would be
		// insecure, so refuse to serve an empty token — caller logs & the gate
		// then rejects everything. Returning "" makes tokenOK always false.
		return ""
	}
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return string(b)
}

// dashStatic holds the per-serve constant facts (read-only DB): corpus counts +
// build identity. Capabilities + toggles are LIVE (search.Engine.State), not here.
//
// The Provenance block answers the question that cost us a 23h-blind prod incident:
// "which DATA LAYER is this binary actually serving, and was it indexed?" The mcp
// image inherits its ~14 GB data layer FROM 3gpp-data by digest pinned at image
// build time, so a correct (FTS+HNSW) data image can exist on the registry while a
// stale, unindexed layer is still being served. Surfacing the baked data labels
// (created date + the source-corpus digest) next to the live hnsw_state / fts
// presence + the served DB file's mtime makes "stale data layer" a 5-second curl
// diagnosis instead of a CI-archaeology dig. See [[project_served_stale_data_layer]].
type dashStatic struct {
	Version         string `json:"version"`
	Baseline        string `json:"baseline"`
	EmbeddingModel  string `json:"embedding_model"`
	EmbeddedClauses int    `json:"embedded_clauses"`
	TotalClauses    int    `json:"total_clauses"`
	Specs           int    `json:"specs"`
	Versions        int    `json:"versions"`
	APIOperations   int    `json:"api_operations"`
	StartedUnix     int64  `json:"started_unix"`

	// Provenance of the served data layer (the new "is my DB stale?" signal).
	HNSWState        string `json:"hnsw_state"`                   // raw schema_meta value: "frozen" | "building" | "" (none)
	FTSIndexPresent  bool   `json:"fts_index_present"`            // a persisted BM25 index exists in the served DB
	DataImageCreated string `json:"data_image_created,omitempty"` // io.kodflow.3gpp.data.created, baked into the image
	SourceCorpus     string `json:"source_corpus,omitempty"`      // 3gpp-corpus digest the data was baked from
	DBPath           string `json:"db_path,omitempty"`            // served DuckDB file
	DBSizeBytes      int64  `json:"db_size_bytes,omitempty"`      // size on disk (a tiny DB = lexical-only build)
	DBMTimeUnix      int64  `json:"db_mtime_unix,omitempty"`      // when the served DB file was written
}

// buildDashStatic computes the corpus snapshot from the store (once, at readiness).
func buildDashStatic(st *store.Store, version, baseline string, started time.Time) dashStatic {
	ctx := context.Background()
	d := dashStatic{
		Version:        version,
		Baseline:       baseline,
		EmbeddingModel: st.GetMeta(ctx, "embedding_model"),
		StartedUnix:    started.Unix(),
		// Data-layer provenance: live index state + the labels baked into the image.
		HNSWState:        st.GetMeta(ctx, "hnsw_state"),
		FTSIndexPresent:  st.FTSAvailable(),
		DataImageCreated: os.Getenv("MCP3GPP_DATA_CREATED"),
		SourceCorpus:     os.Getenv("MCP3GPP_SOURCE_CORPUS"),
		DBPath:           servedDBPath,
	}
	if servedDBPath != "" {
		if fi, err := os.Stat(servedDBPath); err == nil {
			d.DBSizeBytes = fi.Size()
			d.DBMTimeUnix = fi.ModTime().Unix()
		}
	}
	if n, err := st.CountClauses(ctx); err == nil {
		d.TotalClauses = n
		if nn, err := st.CountNullEmbeddings(ctx); err == nil {
			d.EmbeddedClauses = n - nn
		}
	}
	if n, err := st.CountSpecs(ctx); err == nil {
		d.Specs = n
	}
	if n, err := st.CountSpecVersions(ctx); err == nil {
		d.Versions = n
	}
	if n, err := st.CountAPIOperations(ctx); err == nil {
		d.APIOperations = n
	}
	return d
}

// procStats is a cheap process snapshot read per request (the toggles' impact
// shows here: e.g. HNSW off → latency up, reranker on → heap + latency up).
type procStats struct {
	HeapMB     float64 `json:"heap_mb"`
	SysMB      float64 `json:"sys_mb"`
	Goroutines int     `json:"goroutines"`
	NumGC      uint32  `json:"num_gc"`
	NumCPU     int     `json:"num_cpu"`
}

func readProc() procStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return procStats{
		HeapMB:     float64(m.HeapAlloc) / (1024 * 1024),
		SysMB:      float64(m.Sys) / (1024 * 1024),
		Goroutines: runtime.NumGoroutine(),
		NumGC:      m.NumGC,
		NumCPU:     runtime.NumCPU(),
	}
}

// metricsMiddleware records each request's wall-clock latency into the collector.
func metricsMiddleware(c *metrics.Collector, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		c.Observe(time.Since(start))
	})
}

// dashboardData is the /dashboard.json payload.
type dashboardData struct {
	dashStatic
	State     search.State     `json:"state"`
	Reason    string           `json:"reason"`
	Options   []capOption      `json:"options"`
	Process   procStats        `json:"process"`
	Metrics   metrics.Snapshot `json:"metrics"`
	UptimeSec int64            `json:"uptime_sec"`
	NowUnix   int64            `json:"now_unix"`
}

// reasonFor mirrors internal/mcp server_info, extended with the runtime toggles,
// so the dashboard explains WHY semantic is effectively off.
func reasonFor(s search.State, dbModel string) string {
	switch {
	case !s.EmbedderEnabled:
		return "embedder_disabled (lexical binary or EMBEDDER=off)"
	case dbModel == "":
		return "no_vectors_in_db"
	case dbModel != s.EmbedderModelID:
		return "model_mismatch (db=" + dbModel + " client=" + s.EmbedderModelID + ")"
	case !s.HNSWFrozen:
		return "hnsw_unavailable (exact-scan fallback)"
	case !s.VectorOn:
		return "vector arm toggled off"
	case !s.HNSWOn:
		return "hnsw toggled off (exact-scan)"
	default:
		return ""
	}
}

// capOption is the EXPLICIT per-arm status block — one entry per dashboard pill.
// It carries the live state, WHY the arm is in that state, and what would change
// it. The explanation is computed server-side so the JSON (curl) and the HTML
// page tell exactly the same story; the page renders these verbatim.
type capOption struct {
	Key      string `json:"key"`           // toggle name (lexical|vector|hnsw|rerank) or "embedder"
	Label    string `json:"label"`         // display label
	State    string `json:"state"`         // "on" | "degraded" | "off" | "unavailable"
	Badge    string `json:"badge"`         // short pill text ("on", "exact-scan", "non gelé", …)
	Toggle   bool   `json:"toggleable"`    // a runtime toggle exists for this arm
	ToggleOn bool   `json:"toggle_on"`     // current toggle position (meaningful when Toggle)
	Reason   string `json:"reason"`        // explicit cause, plain words
	Fix      string `json:"fix,omitempty"` // remediation when not "on"
}

// optionRows derives the five explicit option blocks from the live engine state
// + the DB's embedding model. Order = pill order on the page.
func optionRows(s search.State, dbModel string) []capOption {
	// ONNX embedder — pure capability (no runtime toggle): either the binary can
	// vectorise a query or it can't.
	emb := capOption{Key: "embedder", Label: "ONNX embedder"}
	if s.EmbedderEnabled {
		emb.State, emb.Badge = "on", "on"
		emb.Reason = "BGE-M3 chargé (id " + s.EmbedderModelID + ") — vectorise les requêtes en local."
	} else {
		emb.State, emb.Badge = "unavailable", "absent"
		emb.Reason = "Binaire compilé sans ONNX ou EMBEDDER=off : aucune vectorisation de requête possible."
		emb.Fix = "Déployer le binaire onnx avec le modèle BGE-M3."
	}

	// Semantic (the vector arm) — needs the embedder, vectors in the DB, and the
	// SAME model on both sides; full speed only with a frozen HNSW.
	vec := capOption{Key: "vector", Label: "Semantic", Toggle: true, ToggleOn: s.VectorOn}
	switch {
	case !s.EmbedderEnabled:
		vec.State, vec.Badge = "unavailable", "n/a"
		vec.Reason = "Nécessite l'embedder ONNX (absent) : impossible de vectoriser la question."
		vec.Fix = "Activer l'embedder (binaire onnx + modèle)."
	case dbModel == "":
		vec.State, vec.Badge = "unavailable", "sans vecteurs"
		vec.Reason = "La base servie ne contient aucun embedding (build lexical)."
		vec.Fix = "Re-baker l'image corpus-data avec les vecteurs."
	case dbModel != s.EmbedderModelID:
		vec.State, vec.Badge = "unavailable", "modèles ≠"
		vec.Reason = "Modèle des vecteurs en base (" + dbModel + ") ≠ modèle requête (" + s.EmbedderModelID + ") : scores incomparables, bras coupé."
		vec.Fix = "Aligner le modèle baké dans la DB et celui du binaire."
	case !s.VectorOn:
		vec.State, vec.Badge = "off", "off"
		vec.Reason = "Bras vectoriel coupé à chaud (toggle opérateur)."
		vec.Fix = "Cliquer la pastille pour le réactiver."
	case s.HNSWFrozen && s.HNSWOn:
		vec.State, vec.Badge = "on", "on (HNSW)"
		vec.Reason = "Recherche par sens active : k-NN HNSW sur tout le corpus (~ms)."
	default:
		vec.State, vec.Badge = "degraded", "exact-scan"
		vec.Reason = "Actif SANS index HNSW : cosine exact borné aux 200 candidats BM25 — recall réduit, latence accrue."
		if s.HNSWFrozen {
			vec.Fix = "Réactiver la pastille HNSW."
		} else {
			vec.Fix = "Re-baker l'image corpus-data avec l'index gelé (cmd/freeze-hnsw)."
		}
	}

	// Sparse (BGE-M3 learned-lexical) — capability = a sparse-capable embedder AND
	// sparse postings in the served DB. Complements BM25 with learned term weights.
	sp := capOption{Key: "sparse", Label: "Sparse (lexical-sem.)", Toggle: true, ToggleOn: s.SparseOn}
	switch {
	case !s.SparseEnabled:
		sp.State, sp.Badge = "unavailable", "absent"
		sp.Reason = "Aucune embedding sparse dans la base (ou binaire/modèle sans tête sparse) : bras non offert."
		sp.Fix = "Ré-embedder avec un modèle BGE-M3 exporté avec la tête sparse (scripts/export-bge-m3-sparse.py)."
	case !s.SparseOn:
		sp.State, sp.Badge = "off", "off"
		sp.Reason = "Bras sparse coupé à chaud (toggle opérateur)."
		sp.Fix = "Cliquer la pastille pour le réactiver."
	default:
		sp.State, sp.Badge = "on", "on"
		sp.Reason = "Poids lexicaux appris (BGE-M3) fusionnés au BM25 + dense via RRF."
	}

	// HNSW — the frozen index is a property of the served DB; the toggle only
	// forces exact-scan for A/B.
	hnsw := capOption{Key: "hnsw", Label: "HNSW", Toggle: true, ToggleOn: s.HNSWOn}
	switch {
	case !s.HNSWFrozen:
		hnsw.State, hnsw.Badge = "unavailable", "non gelé"
		hnsw.Reason = "Aucun index HNSW gelé dans la base servie : le bras vectoriel retombe en exact-scan."
		hnsw.Fix = "Re-baker l'image corpus-data (cmd/freeze-hnsw gèle l'index au bake)."
	case !s.HNSWOn:
		hnsw.State, hnsw.Badge = "off", "off (manuel)"
		hnsw.Reason = "Index présent mais coupé à chaud : exact-scan forcé (A/B latence)."
		hnsw.Fix = "Cliquer la pastille pour le réactiver."
	default:
		hnsw.State, hnsw.Badge = "on", "on"
		hnsw.Reason = "Index gelé chargé : plus-proches-voisins en quelques millisecondes."
	}

	// FTS (BM25) — capability = the FTS index exists in the DB; without it the
	// store degrades to LIKE.
	lex := capOption{Key: "lexical", Label: "FTS (BM25)", Toggle: true, ToggleOn: s.LexicalOn}
	switch {
	case !s.LexicalOn:
		lex.State, lex.Badge = "off", "off"
		lex.Reason = "Bras lexical coupé à chaud (toggle opérateur)."
		lex.Fix = "Cliquer la pastille pour le réactiver."
	case !s.FTSEnabled:
		lex.State, lex.Badge = "degraded", "LIKE"
		lex.Reason = "Index FTS absent de la base : repli LIKE (lent, sans scoring BM25)."
		lex.Fix = "Re-baker la base (l'ingest crée l'index FTS)."
	default:
		lex.State, lex.Badge = "on", "on"
		lex.Reason = "BM25 actif sur l'index FTS (heading + texte)."
	}

	// Reranker — capability = model shipped + onnx binary. When capable it is
	// per-request by default; the toggle re-ranks EVERY query.
	rr := capOption{Key: "rerank", Label: "Reranker", Toggle: true, ToggleOn: s.RerankOn}
	switch {
	case !s.RerankerEnabled:
		rr.State, rr.Badge = "unavailable", "n/a"
		rr.Reason = "Modèle bge-reranker-v2-m3 absent du bake (ou binaire sans ONNX) : aucun re-classement possible."
		rr.Fix = "Re-baker l'image avec le modèle reranker."
	case !s.RerankOn:
		rr.State, rr.Badge = "degraded", "à la demande"
		rr.Reason = "Disponible — appliqué seulement quand la requête passe rerank=true."
		rr.Fix = "Cliquer pour re-classer TOUTES les requêtes (fenêtre top-20, +latence)."
	default:
		rr.State, rr.Badge = "on", "toutes les requêtes"
		rr.Reason = "Cross-encoder re-classe la fenêtre des 20 meilleurs candidats sur chaque requête (+latence)."
	}

	return []capOption{emb, vec, sp, hnsw, lex, rr}
}

// tokenOK accepts the dashboard token from ?token=, the dash_token cookie, or a
// Bearer header. Constant-time compare. An empty configured token rejects all.
func tokenOK(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	got := r.URL.Query().Get("token")
	if got == "" {
		if c, err := r.Cookie("dash_token"); err == nil {
			got = c.Value
		}
	}
	if got == "" {
		got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func dashUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, dashLoginHTML)
}

// dashboardPageHandler serves the page only with a valid token; a query token is
// persisted as an HttpOnly cookie so the page's same-origin /dashboard.json +
// toggle fetches authenticate automatically.
func dashboardPageHandler(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenOK(r, token) {
			dashUnauthorized(w)
			return
		}
		if q := r.URL.Query().Get("token"); q != "" {
			http.SetCookie(w, &http.Cookie{
				Name: "dash_token", Value: q, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, dashboardHTML)
	}
}

// dashboardJSONHandler serves /dashboard.json (token-gated). get returns
// (corpus, engine, ready); while loading it reports {"status":"loading"}.
func dashboardJSONHandler(get func() (dashStatic, *search.Engine, bool), c *metrics.Collector, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenOK(r, token) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"unauthorized — pass ?token=… (see container logs)"}`+"\n")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		corpus, eng, ok := get()
		if !ok || eng == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"status":"loading"}`+"\n")
			return
		}
		state := eng.State()
		now := time.Now()
		out := dashboardData{
			dashStatic: corpus,
			State:      state,
			Reason:     reasonFor(state, corpus.EmbeddingModel),
			Options:    optionRows(state, corpus.EmbeddingModel),
			Process:    readProc(),
			Metrics:    c.Snapshot(),
			UptimeSec:  now.Unix() - corpus.StartedUnix,
			NowUnix:    now.Unix(),
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

// toggleHandler flips one runtime override (token-gated). POST /dashboard/toggle?name=<arm>&on=<bool>
// name ∈ {lexical, vector, hnsw, rerank}. It only turns a CAPABLE arm up/down.
func toggleHandler(get func() (dashStatic, *search.Engine, bool), token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// State-changing endpoint: enforce POST (the documented contract). A GET with
		// a valid token must not flip a toggle (CSRF-like / accidental pre-fetch).
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !tokenOK(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, eng, ok := get()
		if !ok || eng == nil {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		name := r.URL.Query().Get("name")
		on := r.URL.Query().Get("on") == "true"
		switch name {
		case "lexical":
			eng.SetLexical(on)
		case "vector":
			eng.SetVector(on)
		case "sparse":
			eng.SetSparse(on)
		case "hnsw":
			eng.SetHNSW(on)
		case "rerank":
			eng.SetRerank(on)
		default:
			http.Error(w, "unknown toggle "+name, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(eng.State())
	}
}

const dashLoginHTML = `<!doctype html><html lang="fr"><head><meta charset="utf-8">
<title>3gpp-mcp — dashboard protégé</title>
<style>body{font:15px/1.6 system-ui,sans-serif;max-width:520px;margin:18vh auto;padding:0 1rem;color:#e6edf3;background:#0b0f14}
code{background:#131a22;padding:.15rem .4rem;border-radius:5px;color:#39c5cf}a{color:#39c5cf}</style></head><body>
<h2>🔒 Dashboard protégé</h2>
<p>Ce tableau de bord exige un jeton généré au démarrage du conteneur.</p>
<p>Récupère-le dans les logs : <code>docker logs &lt;conteneur&gt; | grep "dashboard token"</code>,
puis ouvre <code>/dashboard?token=&lt;jeton&gt;</code>.</p>
</body></html>`

// dashboardHTML — self-contained page; vanilla JS polls /dashboard.json (cookie
// auth) and POSTs /dashboard/toggle on a pill click. ?refresh=<s> overrides the
// 60s default; locale fr-FR / Europe-Paris.
const dashboardHTML = `<!doctype html>
<html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>3gpp-mcp — dashboard</title>
<style>
  :root{--bg:#0b0f14;--card:#131a22;--line:#1f2a36;--ink:#e6edf3;--mut:#8aa0b3;
        --ok:#2ec27e;--off:#e5534b;--accent:#39c5cf;--warn:#d6a23a}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--ink);
       font:14px/1.5 ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
  header{padding:1.4rem 1.6rem .6rem;display:flex;align-items:baseline;gap:.8rem;flex-wrap:wrap}
  h1{font-size:1.25rem;margin:0;letter-spacing:.5px}
  .sub{color:var(--mut)}
  .wrap{padding:0 1.6rem 2.4rem;max-width:1100px;margin:0 auto}
  .pills{display:flex;gap:.5rem;flex-wrap:wrap;margin:.6rem 0 .4rem}
  .pill{display:inline-flex;align-items:center;gap:.4rem;padding:.32rem .7rem;border-radius:999px;
        background:var(--card);border:1px solid var(--line);font-weight:600;font-size:.82rem;user-select:none}
  .pill.click{cursor:pointer}.pill.click:hover{border-color:var(--accent)}
  .pill.na{opacity:.5}
  .dot{width:.6rem;height:.6rem;border-radius:50%;background:var(--mut)}
  .dot.on{background:var(--ok);box-shadow:0 0 0 3px rgba(46,194,126,.18)}
  .dot.off{background:var(--off);box-shadow:0 0 0 3px rgba(229,83,75,.18)}
  .dot.warn{background:var(--warn);box-shadow:0 0 0 3px rgba(214,162,58,.18)}
  .optgrid{grid-template-columns:repeat(auto-fit,minmax(230px,1fr))}
  .opt .st{font-weight:700;margin:.1rem 0 .3rem}
  .opt .st.on{color:var(--ok)}.opt .st.warn{color:var(--warn)}
  .opt .st.off{color:var(--off)}.opt .st.na{color:var(--mut)}
  .opt p{color:var(--mut);font-size:.8rem;margin:.25rem 0}
  .opt .fix{color:var(--accent)}
  .hint{color:var(--mut);font-size:.76rem;margin:.1rem 0 .6rem}
  details.doc{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:.5rem 1rem;margin:.2rem 0 1rem}
  details.doc summary{cursor:pointer;color:var(--accent);font-weight:600;font-size:.85rem}
  details.doc ul{margin:.6rem 0;padding-left:1.1rem}details.doc li{margin:.35rem 0}
  details.doc p{color:var(--mut);font-size:.82rem;margin:.4rem 0 .2rem}
  .grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:.9rem}
  .card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:1rem 1.1rem}
  .card h3{margin:0 0 .35rem;font-size:.72rem;font-weight:600;letter-spacing:.8px;
           text-transform:uppercase;color:var(--mut)}
  .big{font-size:1.7rem;font-weight:700;font-variant-numeric:tabular-nums;letter-spacing:.5px}
  .big small{font-size:.85rem;color:var(--mut);font-weight:500}
  .span3{grid-column:span 3}
  .chart{width:100%;height:160px}
  .bar{fill:var(--accent)}.bar:hover{fill:#5fe0e8}
  .axis{stroke:var(--line);stroke-width:1}
  .lat{display:flex;gap:.5rem;align-items:flex-end;height:120px;margin-top:.4rem}
  .lat .b{flex:1;background:linear-gradient(#39c5cf,#2b8a90);border-radius:5px 5px 0 0;
          position:relative;min-height:3px;transition:height .4s}
  .lat .b span{position:absolute;top:-1.25rem;left:0;right:0;text-align:center;
               font-size:.72rem;color:var(--mut);font-variant-numeric:tabular-nums}
  .lat .b em{position:absolute;bottom:-1.3rem;left:0;right:0;text-align:center;
             font-size:.7rem;color:var(--mut);font-style:normal}
  .reason{color:var(--warn);font-size:.82rem;margin-top:.3rem;min-height:1rem}
  footer{color:var(--mut);font-size:.78rem;margin-top:1.4rem}
  a{color:var(--accent)}
  h2{font-size:.95rem;color:var(--mut);margin:1.6rem 0 .7rem;font-weight:600}
</style></head><body>
<header>
  <h1>3gpp-mcp</h1>
  <span class="sub" id="sub">dashboard</span>
  <span class="sub" id="clock" style="margin-left:auto;font-variant-numeric:tabular-nums"></span>
</header>
<div class="wrap">
  <div class="pills" id="pills"></div>
  <div class="hint">Clique une option pour l'activer / la désactiver à chaud et observer l'impact (latence, mémoire).</div>
  <details class="doc">
    <summary>À quoi sert chaque option ?</summary>
    <ul>
      <li><b>ONNX embedder</b> — le modèle d'IA (BGE-M3) qui transforme une question en « vecteur de sens ». C'est le moteur de la recherche par sens ; sans lui, on retombe sur les mots-clés.</li>
      <li><b>Semantic</b> (recherche sémantique) — trouve les passages par le <b>sens</b>, pas seulement les mots exacts : « enregistrement de l'UE » remonte aussi « registration procedure ». Idéal pour les questions en langage naturel.</li>
      <li><b>HNSW</b> — l'index qui rend la recherche sémantique <b>rapide</b> (quelques ms au lieu de comparer toute la base). Désactivé : mêmes résultats, mais beaucoup plus lent (exact-scan).</li>
      <li><b>FTS (BM25)</b> — la recherche par <b>mots-clés</b> classique (comme un moteur de recherche texte). Très rapide et précise sur les termes exacts (un identifiant de spec, un sigle).</li>
      <li><b>Reranker</b> — un 2ᵉ modèle qui <b>re-classe</b> les meilleurs candidats en lisant ensemble (question + passage) → résultats plus pertinents, mais plus lent (un passage du modèle par candidat).</li>
    </ul>
    <p>En pratique : <b>hybride</b> (FTS + Semantic fusionnés) est le réglage par défaut le plus robuste ; le <b>reranker</b> affine la pertinence quand on accepte un peu de latence.</p>
  </details>
  <div class="reason" id="reason"></div>

  <h2>Options de recherche — détail</h2>
  <div class="grid optgrid" id="opts"></div>

  <h2>Corpus</h2>
  <div class="grid">
    <div class="card"><h3>Clauses embedées</h3><div class="big" id="emb">–</div></div>
    <div class="card"><h3>Clauses totales</h3><div class="big" id="tot">–</div></div>
    <div class="card"><h3>Specs</h3><div class="big" id="specs">–</div></div>
    <div class="card"><h3>Versions</h3><div class="big" id="vers">–</div></div>
    <div class="card"><h3>Opérations API</h3><div class="big" id="api">–</div></div>
    <div class="card"><h3>Modèle d'embedding</h3><div class="big" id="model" style="font-size:1rem">–</div></div>
  </div>

  <h2>Requêtes</h2>
  <div class="grid">
    <div class="card"><h3>Requêtes totales</h3><div class="big" id="req">–</div></div>
    <div class="card"><h3>Latence moyenne</h3><div class="big" id="avg">–<small> ms</small></div></div>
    <div class="card"><h3>p50</h3><div class="big" id="p50">–<small> ms</small></div></div>
    <div class="card"><h3>p95</h3><div class="big" id="p95">–<small> ms</small></div></div>
    <div class="card"><h3>p99</h3><div class="big" id="p99">–<small> ms</small></div></div>
    <div class="card"><h3>Uptime</h3><div class="big" id="up">–</div></div>
  </div>

  <h2>Serveur</h2>
  <div class="grid">
    <div class="card"><h3>Heap</h3><div class="big" id="heap">–<small> Mo</small></div></div>
    <div class="card"><h3>Mémoire sys</h3><div class="big" id="sys">–<small> Mo</small></div></div>
    <div class="card"><h3>Goroutines</h3><div class="big" id="gor">–</div></div>
    <div class="card"><h3>GC</h3><div class="big" id="gc">–</div></div>
    <div class="card"><h3>CPU</h3><div class="big" id="cpu">–</div></div>
  </div>

  <h2>Requêtes / heure (24 dernières heures)</h2>
  <div class="card span3"><svg class="chart" id="rpm"></svg></div>

  <h2>Distribution de latence</h2>
  <div class="card span3"><div class="lat" id="latbars"></div></div>

  <footer id="foot">connexion…</footer>
</div>
<script>
const REFRESH_MS=(()=>{const v=parseInt(new URLSearchParams(location.search).get('refresh'),10);
  return Math.max(2,Number.isFinite(v)&&v>0?v:60)*1000;})();
const LOC='fr-FR',TZ='Europe/Paris';
const $=id=>document.getElementById(id);
const fmt=n=>n==null?'–':n.toLocaleString(LOC);
const ms=n=>n==null?'–':(n<10?n.toFixed(1):Math.round(n).toLocaleString(LOC));
function dur(s){if(s==null)return'–';s=Math.max(0,s|0);const d=s/86400|0,h=s%86400/3600|0,m=s%3600/60|0;
  if(d)return d+'j '+h+'h';if(h)return h+'h '+m+'m';if(m)return m+'m';return s+'s';}
// Per-option rendering: the server's d.options[] is the single source of truth
// (state + badge + reason + fix) — pills and detail cards render it verbatim.
const STCLS={on:'on',degraded:'warn',off:'off',unavailable:'na'};
function esc(s){return String(s==null?'':s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');}
function pill(o){
  const click=o.toggleable&&o.state!=='unavailable';
  const cls='pill'+(click?' click':'')+(o.state==='unavailable'?' na':'');
  const on=click?(' onclick="tog(\''+o.key+'\','+(!o.toggle_on)+')"'):'';
  const dot=o.state==='unavailable'?'':STCLS[o.state]||'';
  return '<span class="'+cls+'"'+on+' title="'+esc(o.reason)+'"><span class="dot '+dot+'"></span>'+esc(o.label)+': '+esc(o.badge)+'</span>';
}
function optCard(o){
  return '<div class="card opt"><h3>'+esc(o.label)+'</h3>'+
    '<div class="st '+(STCLS[o.state]||'na')+'">'+esc(o.badge)+'</div>'+
    '<p>'+esc(o.reason)+'</p>'+(o.fix?'<p class="fix">→ '+esc(o.fix)+'</p>':'')+'</div>';
}
async function tog(name,on){
  try{await fetch('/dashboard/toggle?name='+name+'&on='+on,{method:'POST',cache:'no-store'});}catch(e){}
  tick();
}
// Localised hour label / full timestamp for a unix start-of-hour (Europe/Paris).
function hourLabel(t){return new Date(t*1000).toLocaleString(LOC,{timeZone:TZ,hour:'2-digit',hour12:false}).replace(/\D*$/,'')+'h';}
function hourFull(t){return new Date(t*1000).toLocaleString(LOC,{timeZone:TZ,day:'2-digit',month:'2-digit',hour:'2-digit',minute:'2-digit'});}
function drawRpm(series){
  const svg=$('rpm'),W=1000,H=160,padX=10,padT=16,padB=22;svg.setAttribute('viewBox','0 0 '+W+' '+H);
  const n=series.length||1;const max=Math.max(1,...series.map(p=>p.count));
  const baseY=H-padB,plotH=H-padT-padB,bw=(W-2*padX)/n;
  // axis + max gridline so the scale is readable even when bars are short.
  let s='<line class="axis" x1="'+padX+'" y1="'+baseY+'" x2="'+(W-padX)+'" y2="'+baseY+'"/>'+
    '<line class="axis" x1="'+padX+'" y1="'+padT+'" x2="'+(W-padX)+'" y2="'+padT+'" stroke-dasharray="2 4" opacity=".5"/>'+
    '<text x="'+padX+'" y="'+(padT-4)+'" fill="#8aa0b3" font-size="10">max '+fmt(max)+' req/h</text>';
  const total=series.reduce((a,p)=>a+(p.count||0),0);
  series.forEach((p,i)=>{
    const h=plotH*(p.count/max);const x=padX+i*bw;const y=baseY-h;
    s+='<rect class="bar" x="'+(x+1)+'" y="'+y+'" width="'+Math.max(1,bw-2)+'" height="'+Math.max(0,h)+'">'+
      '<title>'+esc(hourFull(p.t))+' · '+fmt(p.count)+' req</title></rect>';
    // Label every 3rd hour (and the newest) so 24 ticks don't crowd.
    if(i%3===0||i===n-1){
      s+='<text x="'+(x+bw/2)+'" y="'+(H-7)+'" fill="#8aa0b3" font-size="10" text-anchor="middle">'+esc(hourLabel(p.t))+'</text>';
    }
  });
  if(total===0){s+='<text x="'+(W/2)+'" y="'+(baseY-plotH/2)+'" fill="#8aa0b3" font-size="12" text-anchor="middle">aucune requête sur les 24 dernières heures</text>';}
  svg.innerHTML=s;
}
function drawLat(m){
  const rows=[['avg',m.avg_ms],['p50',m.p50_ms],['p95',m.p95_ms],['p99',m.p99_ms]];
  const max=Math.max(1,...rows.map(r=>r[1]||0));
  $('latbars').innerHTML=rows.map(r=>{const h=Math.max(3,110*((r[1]||0)/max));
    return '<div class="b" style="height:'+h+'px"><span>'+ms(r[1])+'</span><em>'+r[0]+'</em></div>';}).join('');
}
async function tick(){
  try{
    const r=await fetch('/dashboard.json',{cache:'no-store'});
    if(r.status===401){$('foot').textContent='non autorisé — jeton manquant/invalide';return;}
    if(r.status===503){$('foot').textContent='chargement du corpus…';setTimeout(tick,1500);return;}
    const d=await r.json();
    $('sub').textContent='v'+(d.version||'?').slice(0,12)+' · baseline '+(d.baseline||'?');
    const opts=d.options||[];
    $('pills').innerHTML=opts.map(pill).join('');
    $('opts').innerHTML=opts.map(optCard).join('');
    $('reason').textContent=d.reason?('sémantique réduit — '+d.reason):'';
    $('emb').innerHTML=fmt(d.embedded_clauses)+(d.total_clauses?' <small>/ '+fmt(d.total_clauses)+'</small>':'');
    $('tot').textContent=fmt(d.total_clauses);
    $('specs').textContent=fmt(d.specs);
    $('vers').textContent=fmt(d.versions);
    $('api').textContent=fmt(d.api_operations);
    $('model').textContent=d.embedding_model||'—';
    const m=d.metrics||{},p=d.process||{};
    $('req').textContent=fmt(m.total);
    $('avg').innerHTML=ms(m.avg_ms)+'<small> ms</small>';
    $('p50').innerHTML=ms(m.p50_ms)+'<small> ms</small>';
    $('p95').innerHTML=ms(m.p95_ms)+'<small> ms</small>';
    $('p99').innerHTML=ms(m.p99_ms)+'<small> ms</small>';
    $('up').innerHTML=dur(d.uptime_sec)+(d.started_unix?' <small>depuis '+esc(new Date(d.started_unix*1000).toLocaleString(LOC,{timeZone:TZ,day:'2-digit',month:'short',hour:'2-digit',minute:'2-digit'}))+'</small>':'');
    $('heap').innerHTML=ms(p.heap_mb)+'<small> Mo</small>';
    $('sys').innerHTML=ms(p.sys_mb)+'<small> Mo</small>';
    $('gor').textContent=fmt(p.goroutines);
    $('gc').textContent=fmt(p.num_gc);
    $('cpu').textContent=fmt(p.num_cpu);
    if(m.series)drawRpm(m.series);
    drawLat(m);
    $('foot').textContent='mis à jour '+new Date().toLocaleTimeString(LOC,{timeZone:TZ})+' · '+(m.samples||0)+' échantillons de latence · rafraîchissement '+(REFRESH_MS/1000)+'s';
  }catch(e){$('foot').textContent='erreur de requête : '+e;}
  setTimeout(tick,REFRESH_MS);
}
// Live wall-clock in the header (per-second), independent of the data refresh.
function clock(){const e=$('clock');if(e)e.textContent='🕐 '+new Date().toLocaleString(LOC,{timeZone:TZ,weekday:'short',day:'2-digit',month:'short',hour:'2-digit',minute:'2-digit',second:'2-digit'});}
clock();setInterval(clock,1000);
tick();
</script>
</body></html>`
