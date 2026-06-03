#!/usr/bin/env python3
# Per-RELEASE embed progress dashboard for the Kaggle campaign.
#
# Rows = releases (Rel-99 → Rel-20, lineage order). Columns: total clauses (corpus
# denominator), embedded so far, a progress bar, % , and a real clauses/s + ETA in
# the footer. The campaign embeds per SERIES on Kaggle; the laptop only sees a
# series once its DB is pulled, so the per-release embedded counts advance each time
# a series COMPLETES (not continuously). Pure read-only; safe to run in a loop.
#
# Data sources (all local, no Kaggle calls):
#   .kaggle-campaign/release-totals.json     — per-release totals from `latest` (denominator)
#   .kaggle-campaign/out-*/3gpp-embedded.duckdb + state-*/…  — pulled series DBs (embedded)
#   .kaggle-campaign/out-*/RESULT.txt         — per-series embedded= + elapsed= (real cl/s)
import glob
import json
import os
import re
import sys

ROOT = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), ".kaggle-campaign")


def rel_ordinal(rel):
    """Lineage rank: Rel-99 = 3 (oldest real release), Rel-4..Rel-20 = 4..20.
    Anything unparseable sorts last."""
    if rel == "Rel-99":
        return 3
    m = re.match(r"Rel-(\d+)$", rel or "")
    if m:
        n = int(m.group(1))
        return n if n >= 4 else 999  # majors 0/1/2 are pre-99 drafts → end
    return 1000


def load_totals():
    p = os.path.join(ROOT, "release-totals.json")
    if not os.path.isfile(p):
        return None
    return json.load(open(p))


def embedded_per_release():
    """Sum embedded (and seen) clauses per release across every pulled series DB.
    Clauses are disjoint across series (by spec prefix), so summing is correct.
    Prefer out-<s> (freshest pull); fall back to state-<s>."""
    try:
        import duckdb
    except ImportError:
        return None, "duckdb module missing (pip install duckdb in the venv)"
    emb = {}
    seen_series = set()
    dbs = sorted(glob.glob(os.path.join(ROOT, "out-*", "3gpp-embedded.duckdb")) +
                 glob.glob(os.path.join(ROOT, "state-*", "3gpp-embedded.duckdb")))
    for db in dbs:
        m = re.search(r"(?:out|state)-(\d+)", db)
        s = m.group(1) if m else db
        if s in seen_series:
            continue  # one DB per series (out preferred via sort: out-* < state-*)
        try:
            con = duckdb.connect(db, read_only=True)
            rows = con.execute(
                "select release, sum((embedding is not null)::int) e "
                "from clauses group by release").fetchall()
            con.close()
        except Exception:
            continue
        seen_series.add(s)
        for rel, e in rows:
            emb[rel] = emb.get(rel, 0) + int(e or 0)
    return emb, None


def real_throughput():
    """sum(embedded)/sum(embed elapsed) across pulled RESULT.txt = real GPU cl/s."""
    tot_emb, tot_el = 0, 0
    for rf in glob.glob(os.path.join(ROOT, "out-*", "RESULT.txt")):
        txt = open(rf, errors="ignore").read()
        m = re.search(r"embedded=(\d+) .*elapsed=(\d+)s", txt)
        if m:
            tot_emb += int(m.group(1))
            tot_el += int(m.group(2))
    cls = (tot_emb / tot_el) if tot_el else 0.0
    return cls, tot_emb, tot_el


def bar(frac, width=24):
    frac = max(0.0, min(1.0, frac))
    filled = int(round(frac * width))
    return "█" * filled + "░" * (width - filled)


def human(n):
    return f"{n:,}".replace(",", " ")


def fmt_eta(seconds):
    if seconds <= 0 or seconds != seconds:  # 0 or NaN
        return "—"
    h = int(seconds // 3600)
    m = int((seconds % 3600) // 60)
    return f"{h}h{m:02d}"


def main():
    totals = load_totals()
    emb, err = embedded_per_release()
    cls, run_emb, run_el = real_throughput()

    print("┌─ EMBED CORPUS — progression par release " + "─" * 30)
    if totals is None:
        print("│  (totaux corpus pas encore prêts — calcul du dénominateur en cours)")
        if emb:
            print("│  embeddé jusqu'ici (sans dénominateur) :")
            for rel in sorted(emb, key=rel_ordinal):
                print(f"│    {rel:7s} embedded={human(emb[rel])}")
        print("└" + "─" * 70)
        return
    if err:
        print(f"│  ⚠ {err}")

    per = totals["per_release"]
    grand_total = totals["total"]
    emb = emb or {}
    # union of releases (denominator drives the rows), lineage order
    rels = sorted(per.keys(), key=rel_ordinal)
    grand_emb = sum(emb.get(r, 0) for r in per)

    print(f"│ {'Rel':7s} {'clauses':>9s} {'embedded':>9s}  {'progress':24s} {'%':>5s}")
    print("│ " + "─" * 64)
    for rel in rels:
        tot = per[rel]
        e = emb.get(rel, 0)
        frac = (e / tot) if tot else 0.0
        print(f"│ {rel:7s} {human(tot):>9s} {human(e):>9s}  {bar(frac)} {frac*100:4.0f}%")
    print("│ " + "─" * 64)
    gfrac = (grand_emb / grand_total) if grand_total else 0.0
    print(f"│ {'TOTAL':7s} {human(grand_total):>9s} {human(grand_emb):>9s}  {bar(gfrac)} {gfrac*100:4.0f}%")

    remaining = grand_total - grand_emb
    eta = (remaining / cls) if cls else 0
    print("│ " + "─" * 64)
    print(f"│  cl/s réel (GPU): {cls:5.1f}   │   embeddé: {human(grand_emb)}/{human(grand_total)}"
          f"   │   ETA GPU pseudo: {fmt_eta(eta)}")
    print(f"│  (réel = {human(run_emb)} clauses en {run_el}s de GPU cumulé sur séries finies;"
          f" ETA = restant ÷ cl/s, hors build/pull/quota)")
    print("└" + "─" * 70)


if __name__ == "__main__":
    sys.exit(main())
