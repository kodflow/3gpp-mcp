#!/usr/bin/env bash
# where.sh — OU EN EST LE PIPELINE ? Une commande, a lancer en premier.
#
# POURQUOI CE SCRIPT EXISTE. Une campagne de corpus s'etale sur plusieurs jours
# et plusieurs sessions. Sans lui, chacune redecouvre l'etat en fouillant les
# logs, et l'impression qui s'installe est qu'on repart de zero — alors que
# `goal` a tout enregistre. Il ne DEDUIT rien : il lit l'etat que `goal` ecrit
# lui-meme dans .local/state/steps/<etape>.json et compare l'empreinte du corpus
# qui y figure (taille du .duckdb) a celle du fichier aujourd'hui. C'est le meme
# test que `goal` fait pour decider de rejouer une etape, donc le tableau dit ce
# que `goal` FERA, pas ce qu'on espere.
#
# DEUX PIEGES QU'IL CORRIGE, et qu'il ne faut pas reintroduire :
#   - `index-etsi` n'enregistre AUCUNE entree et `validate` ne surveille que
#     3gpp.duckdb. Les juger sur leur propre empreinte les afficherait "success"
#     sur un corpus qui a change sous elles.
#   - La chaine etant lineaire, la peremption se PROPAGE vers l'aval : c'est
#     ainsi que `goal` les rejouera, via les empreintes de dependances.
#
# Usage : scripts/local/where.sh [chemin/vers/corpus.duckdb]   (defaut: data/etsi.duckdb)
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1
DB="${1:-data/etsi.duckdb}"
S=.local/state/steps

cur=""
[ -f "$DB" ] && cur="$(stat -c %s "$DB")"

busy=""
for p in goal embed-io embedder migrate-paragraphs embed-core-sparse compact crane merge ingest; do
	if [ -n "$(powershell -NoProfile -Command "if (Get-Process $p -ErrorAction SilentlyContinue) {'y'}" 2>/dev/null | tr -d '[:space:]')" ]; then
		busy="$busy $p"
	fi
done

st() { # <etape> -> "statut|fini_le|taille_db_enregistree"
	f="$S/$1.json"
	[ -f "$f" ] || { echo "jamais|-|-"; return; }
	DBNAME="$(basename "$DB")" python - "$f" <<'PY'
import json, os, sys
d = json.load(open(sys.argv[1]))
want = os.environ["DBNAME"]
size = "-"
for k, v in (d.get("inputs") or {}).items():
    if k.replace("\\", "/").endswith("/" + want) or k.endswith(want):
        size = str(v).split(":")[0]
print("%s|%s|%s" % (d.get("status", "?"), (d.get("finished_at") or "-")[:19], size))
PY
}

printf '\n=== OU EN EST LE PIPELINE ===  %s UTC\n' "$(date -u +%H:%M:%SZ)"
if [ -n "$busy" ]; then printf 'process actifs :%s\n' "$busy"; else printf 'process actifs : AUCUN (rien ne tourne)\n'; fi
printf 'corpus         : %s  %s octets\n\n' "$DB" "${cur:-absent}"
printf '%-18s %-10s %-20s %s\n' "ETAPE" "ETAT" "FINIE LE (UTC)" "REMARQUE"
printf -- '------------------ ---------- -------------------- ------------------------------\n'

stale=0
for s in corpus-etsi embed-etsi paragraphs-etsi sparse-etsi compact index-etsi validate smoke; do
	IFS='|' read -r status fin dbsz <<<"$(st "$s")"
	note=""
	if [ "$status" = "success" ] && [ "$dbsz" != "-" ] && [ -n "$cur" ] && [ "$dbsz" != "$cur" ]; then
		status="PERIMEE"; note="corpus a change depuis (etait $dbsz)"; stale=1
	elif [ "$status" = "running" ]; then
		stale=1
	elif [ "$status" != "success" ]; then
		stale=1
	elif [ "$stale" = "1" ]; then
		status="A REJOUER"; note="une etape amont a change"
	fi
	printf '%-18s %-10s %-20s %s\n' "$s" "$status" "$fin" "$note"
done
printf -- '------------------ ---------- -------------------- ------------------------------\n'
printf 'Un "success" DATE ne prouve rien : c est l empreinte du corpus qui tranche.\n\n'
