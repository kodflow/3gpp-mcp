package main

// dashboard.go adds a small, self-contained HTML dashboard for the HTTP serve
// mode: a live at-a-glance view of whether ONNX/semantic is active, how much of
// the corpus is embedded, and request throughput + latency (avg / p50 / p95 /
// p99). Pure stdlib + vanilla JS with inline SVG charts — no framework, no CDN,
// no auth (CLAUDE.md §10). Two routes: /dashboard (the page) and /dashboard.json
// (its data). Registered by startEarlyHTTP in landing.go.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/metrics"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// dashStatic holds the facts that do not change during a serve (read-only DB +
// fixed embedder), computed ONCE at readiness. Live request metrics come from the
// metrics.Collector, not from here.
type dashStatic struct {
	Version         string `json:"version"`
	Baseline        string `json:"baseline"`
	OnnxEnabled     bool   `json:"onnx_enabled"` // the client embedder loaded
	Semantic        bool   `json:"semantic"`     // embedder + matching vectors + hnsw all line up
	Hnsw            bool   `json:"hnsw"`
	Fts             bool   `json:"fts"`
	Reranker        bool   `json:"reranker"`
	Reason          string `json:"reason"` // why semantic is off (empty when on)
	EmbeddingModel  string `json:"embedding_model"`
	EmbeddedClauses int    `json:"embedded_clauses"`
	TotalClauses    int    `json:"total_clauses"`
	Specs           int    `json:"specs"`
	Versions        int    `json:"versions"`
	APIOperations   int    `json:"api_operations"`
	StartedUnix     int64  `json:"started_unix"`
}

// buildDashStatic computes the snapshot from the store + the engine capabilities.
// The semantic "reason" ladder mirrors internal/mcp's server_info so the dashboard
// and the MCP tool never disagree on WHY semantic is off.
func buildDashStatic(st *store.Store, caps search.Caps, version, baseline string, started time.Time) dashStatic {
	ctx := context.Background()
	dbModel := st.GetMeta(ctx, "embedding_model")
	hnsw := st.VSSAvailable()
	d := dashStatic{
		Version:        version,
		Baseline:       baseline,
		OnnxEnabled:    caps.EmbedderEnabled,
		Hnsw:           hnsw,
		Fts:            st.FTSAvailable(),
		Reranker:       caps.RerankerEnabled,
		EmbeddingModel: dbModel,
		StartedUnix:    started.Unix(),
	}
	switch {
	case !caps.EmbedderEnabled:
		d.Reason = "embedder_disabled (lexical binary or EMBEDDER=off)"
	case dbModel == "":
		d.Reason = "no_vectors_in_db"
	case dbModel != caps.EmbedderModelID:
		d.Reason = "model_mismatch (db=" + dbModel + " client=" + caps.EmbedderModelID + ")"
	case !hnsw:
		d.Reason = "hnsw_unavailable (exact-scan fallback)"
	default:
		d.Semantic = true
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

// metricsMiddleware records each request's wall-clock latency into the collector.
func metricsMiddleware(c *metrics.Collector, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		c.Observe(time.Since(start))
	})
}

// dashboardData is the /dashboard.json payload: static facts (flattened) + live metrics.
type dashboardData struct {
	dashStatic
	Metrics   metrics.Snapshot `json:"metrics"`
	UptimeSec int64            `json:"uptime_sec"`
	NowUnix   int64            `json:"now_unix"`
}

// dashboardJSONHandler serves /dashboard.json. getStatic returns (snapshot, ready):
// while the corpus is still loading it reports loading, so the page shows a clear
// state instead of zeros.
func dashboardJSONHandler(getStatic func() (dashStatic, bool), c *metrics.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		s, ok := getStatic()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"status":"loading"}`+"\n")
			return
		}
		now := time.Now()
		out := dashboardData{
			dashStatic: s,
			Metrics:    c.Snapshot(),
			UptimeSec:  now.Unix() - s.StartedUnix,
			NowUnix:    now.Unix(),
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func dashboardPageHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, dashboardHTML)
}

// dashboardHTML is a single self-contained page: inline CSS + vanilla JS that polls
// /dashboard.json every few seconds and draws an inline-SVG requests/min chart and
// a latency-percentile bar. No external assets, no build step.
const dashboardHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
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
  .pills{display:flex;gap:.5rem;flex-wrap:wrap;margin:.6rem 0 1.2rem}
  .pill{display:inline-flex;align-items:center;gap:.4rem;padding:.32rem .7rem;border-radius:999px;
        background:var(--card);border:1px solid var(--line);font-weight:600;font-size:.82rem}
  .dot{width:.6rem;height:.6rem;border-radius:50%;background:var(--mut)}
  .dot.on{background:var(--ok);box-shadow:0 0 0 3px rgba(46,194,126,.18)}
  .dot.off{background:var(--off);box-shadow:0 0 0 3px rgba(229,83,75,.18)}
  .grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:.9rem}
  .card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:1rem 1.1rem}
  .card h3{margin:0 0 .35rem;font-size:.72rem;font-weight:600;letter-spacing:.8px;
           text-transform:uppercase;color:var(--mut)}
  .big{font-size:1.7rem;font-weight:700;font-variant-numeric:tabular-nums;letter-spacing:.5px}
  .big small{font-size:.85rem;color:var(--mut);font-weight:500}
  .span2{grid-column:span 2}.span3{grid-column:span 3}
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
  .reason{color:var(--warn);font-size:.82rem;margin-top:.5rem;min-height:1rem}
  footer{color:var(--mut);font-size:.78rem;margin-top:1.4rem}
  a{color:var(--accent)}
  h2{font-size:.95rem;color:var(--mut);margin:1.6rem 0 .7rem;font-weight:600}
</style></head><body>
<header>
  <h1>3gpp-mcp</h1>
  <span class="sub" id="sub">live dashboard</span>
</header>
<div class="wrap">
  <div class="pills" id="pills"></div>
  <div class="reason" id="reason"></div>

  <h2>Corpus</h2>
  <div class="grid">
    <div class="card"><h3>Embedded clauses</h3><div class="big" id="emb">–</div></div>
    <div class="card"><h3>Total clauses</h3><div class="big" id="tot">–</div></div>
    <div class="card"><h3>Specs</h3><div class="big" id="specs">–</div></div>
    <div class="card"><h3>Spec versions</h3><div class="big" id="vers">–</div></div>
    <div class="card"><h3>API operations</h3><div class="big" id="api">–</div></div>
    <div class="card"><h3>Embedding model</h3><div class="big" id="model" style="font-size:1rem">–</div></div>
  </div>

  <h2>Requests</h2>
  <div class="grid">
    <div class="card"><h3>Total requests</h3><div class="big" id="req">–</div></div>
    <div class="card"><h3>Avg latency</h3><div class="big" id="avg">–<small> ms</small></div></div>
    <div class="card"><h3>p50</h3><div class="big" id="p50">–<small> ms</small></div></div>
    <div class="card"><h3>p95</h3><div class="big" id="p95">–<small> ms</small></div></div>
    <div class="card"><h3>p99</h3><div class="big" id="p99">–<small> ms</small></div></div>
    <div class="card"><h3>Uptime</h3><div class="big" id="up">–</div></div>
  </div>

  <h2>Requests / minute (last 60 min)</h2>
  <div class="card span3"><svg class="chart" id="rpm" preserveAspectRatio="none"></svg></div>

  <h2>Latency distribution</h2>
  <div class="card span3"><div class="lat" id="latbars"></div></div>

  <footer id="foot">connecting…</footer>
</div>
<script>
const $=id=>document.getElementById(id);
const fmt=n=>n==null?'–':n.toLocaleString('en-US');
const ms=n=>n==null?'–':(n<10?n.toFixed(1):Math.round(n).toLocaleString('en-US'));
function dur(s){if(s==null)return'–';s=Math.max(0,s|0);const d=s/86400|0,h=s%86400/3600|0,m=s%3600/60|0;
  if(d)return d+'d '+h+'h';if(h)return h+'h '+m+'m';if(m)return m+'m';return s+'s';}
function pill(label,on,offText){const ok=!!on;return '<span class="pill"><span class="dot '+(ok?'on':'off')+'"></span>'+
  label+': '+(ok?'on':(offText||'off'))+'</span>';}
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
    if(r.status===503){$('foot').textContent='corpus loading…';setTimeout(tick,1500);return;}
    const d=await r.json();
    $('sub').textContent='v'+(d.version||'?').slice(0,12)+' · baseline '+(d.baseline||'?');
    $('pills').innerHTML=
      pill('ONNX embedder',d.onnx_enabled)+
      pill('Semantic',d.semantic)+
      pill('HNSW',d.hnsw,'exact-scan')+
      pill('FTS (BM25)',d.fts)+
      pill('Reranker',d.reranker);
    $('reason').textContent=d.semantic?'':(d.reason?('semantic off — '+d.reason):'');
    $('emb').innerHTML=fmt(d.embedded_clauses)+(d.total_clauses?' <small>/ '+fmt(d.total_clauses)+'</small>':'');
    $('tot').textContent=fmt(d.total_clauses);
    $('specs').textContent=fmt(d.specs);
    $('vers').textContent=fmt(d.versions);
    $('api').textContent=fmt(d.api_operations);
    $('model').textContent=d.embedding_model||'—';
    const m=d.metrics||{};
    $('req').textContent=fmt(m.total);
    $('avg').innerHTML=ms(m.avg_ms)+'<small> ms</small>';
    $('p50').innerHTML=ms(m.p50_ms)+'<small> ms</small>';
    $('p95').innerHTML=ms(m.p95_ms)+'<small> ms</small>';
    $('p99').innerHTML=ms(m.p99_ms)+'<small> ms</small>';
    $('up').textContent=dur(d.uptime_sec);
    if(m.series)drawRpm(m.series);
    drawLat(m);
    $('foot').textContent='updated '+new Date().toLocaleTimeString()+' · '+(m.samples||0)+' latency samples · auto-refresh 3s';
  }catch(e){$('foot').textContent='fetch error: '+e;}
  setTimeout(tick,3000);
}
tick();
</script>
</body></html>`
