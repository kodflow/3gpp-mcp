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
}

// buildDashStatic computes the corpus snapshot from the store (once, at readiness).
func buildDashStatic(st *store.Store, version, baseline string, started time.Time) dashStatic {
	ctx := context.Background()
	d := dashStatic{
		Version:        version,
		Baseline:       baseline,
		EmbeddingModel: st.GetMeta(ctx, "embedding_model"),
		StartedUnix:    started.Unix(),
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

  <h2>Requêtes / minute (60 dernières min)</h2>
  <div class="card span3"><svg class="chart" id="rpm" preserveAspectRatio="none"></svg></div>

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
// pill: capable→clickable (toggles name); on/off colour. name null = capability badge.
function pill(label,capable,on,name){
  const cls='pill'+(capable&&name?' click':'')+(capable?'':' na');
  const click=capable&&name?(' onclick="tog(\''+name+'\','+(!on)+')"'):'';
  const txt=capable?(on?'on':'off'):'n/a';
  return '<span class="'+cls+'"'+click+'><span class="dot '+(capable?(on?'on':'off'):'')+'"></span>'+label+': '+txt+'</span>';
}
async function tog(name,on){
  try{await fetch('/dashboard/toggle?name='+name+'&on='+on,{method:'POST',cache:'no-store'});}catch(e){}
  tick();
}
function drawRpm(series){
  const svg=$('rpm'),W=1000,H=160,pad=6;svg.setAttribute('viewBox','0 0 '+W+' '+H);
  const max=Math.max(1,...series.map(p=>p.count));const n=series.length;const bw=(W-2*pad)/n;
  let s='<line class="axis" x1="'+pad+'" y1="'+(H-pad)+'" x2="'+(W-pad)+'" y2="'+(H-pad)+'"/>';
  series.forEach((p,i)=>{const h=(H-2*pad)*(p.count/max);const x=pad+i*bw;const y=H-pad-h;
    s+='<rect class="bar" x="'+(x+1)+'" y="'+y+'" width="'+Math.max(1,bw-2)+'" height="'+Math.max(0,h)+'"><title>'+
      p.count+' req</title></rect>';});
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
    const d=await r.json();const s=d.state||{};
    $('sub').textContent='v'+(d.version||'?').slice(0,12)+' · baseline '+(d.baseline||'?');
    $('pills').innerHTML=
      pill('ONNX embedder',s.embedder_enabled,s.embedder_enabled,null)+
      pill('Semantic',s.embedder_enabled,s.vector_on&&s.embedder_enabled,'vector')+
      pill('HNSW',s.hnsw_frozen,s.hnsw_on&&s.hnsw_frozen,'hnsw')+
      pill('FTS (BM25)',true,s.lexical_on,'lexical')+
      pill('Reranker',s.reranker_enabled,s.rerank_on&&s.reranker_enabled,'rerank');
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
    $('up').textContent=dur(d.uptime_sec);
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
tick();
</script>
</body></html>`
