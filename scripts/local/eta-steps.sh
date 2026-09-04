#!/usr/bin/env bash
#
# eta-steps.sh — L'ETA DE CHAQUE ETAPE AVANT QU'ELLE SOIT CONSIDEREE COMME FINIE.
#
# Rien n'est deviné. `goal` enregistre `duration_sec` dans
# .local/state/steps/<etape>.json a chaque run, et `make plan` dit ce qu'il
# ferait de chacune. Ce script croise les deux.
#
# Trois honnêtetés que les chiffres bruts cachent, et qui sont marquées :
#
#   (declin)  la derniere execution a DECLINE — elle n'a rien fait. Sa duree
#             mesure un refus, pas le travail. `paragraphs` affiche 0,4 s et
#             `corpus-etsi` 4,4 s pour cette seule raison.
#   RUN?      l'etape sera RE-DECIDEE contre l'etat reel quand sa dependance
#             aura fini, et sautee si celle-ci n'a rien change. Son ETA est un
#             plafond, pas une prevision.
#   (jamais)  aucune execution enregistree : pas d'estimation possible.
#
# Une etape EN COURS est traitee a part : l'ETA y est la duree mesuree moins le
# temps deja ecoule, et devient "depasse de …" quand ce run dure plus que le
# precedent — ce qui est une information, pas une erreur.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

# On interroge `goal` DIRECTEMENT, jamais `make plan`. Un make imbriqué hérite de
# MAKEFLAGS (jobserver compris) et sa sortie n'est plus celle qu'on parse : la
# table sortait vide, sans une erreur. `make eta` a déjà rebâti l'orchestrateur
# avant d'arriver ici, donc le binaire est à jour par construction.
GOAL="$ROOT/.local/bin/goal.exe"
[ -x "$GOAL" ] || GOAL="$ROOT/.local/bin/goal"
STEPS="$ROOT/.local/state/steps"

# L'amorce, ici aussi. Sans elle `go` n'est pas sur le PATH, l'etape `toolchain`
# rate sa validation, et comme elle est en amont de tout, ce script annonce
# serieusement 26 etapes a rejouer. Un tableau d'ETA faux est pire que pas de
# tableau. Idempotent : ne rien reinstaller, seulement exporter.
# shellcheck source=scripts/local/toolchain-env.sh
. "$ROOT/scripts/local/toolchain-env.sh" >/dev/null 2>&1 || true

# strip_ansi avec un octet ESC LITTERAL, jamais "\x1b".
#
# `\x1b` est une extension GNU que tous les sed ne connaissent pas — et la
# toolchain portable en met un autre en tete du PATH. Resultat : le meme script
# nettoyait la couleur en autonome et la laissait passer sous `make`, ou le motif
# ne matchait alors plus rien et la table sortait VIDE, sans une erreur. Le
# caractere reel marche partout.
ESC=$(printf '\033')
strip_ansi() { sed "s/${ESC}\[[0-9;]*m//g"; }

# hhmmss met en forme des secondes. Au-dela de l'heure les secondes ne servent
# plus a rien et brouillent la lecture.
hhmmss() {
  awk -v s="${1:-0}" 'BEGIN{
    s = (s < 0 ? -s : s); h = int(s/3600); m = int((s%3600)/60); sec = int(s%60);
    if (h > 0)      printf "%dh%02dm", h, m;
    else if (m > 0) printf "%dm%02ds", m, sec;
    else            printf "%ds", sec;
  }'
}

field() { grep -o "\"$2\": *\"\?[^,\"}]*" "$1" 2>/dev/null | head -1 | sed 's/.*: *"\?//'; }

printf '\n=== ETA PAR ETAPE ===  %s\n\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

plan=$(mktemp); trap 'rm -f "$plan"' EXIT
"$GOAL" plan 2>&1 | strip_ansi > "$plan"

if ! grep -q 'GOAL PLAN' "$plan"; then
  echo "goal plan a echoue :"; tail -5 "$plan"; exit 1
fi

printf '%-17s %-6s %-11s %s\n' "ETAPE" "PLAN" "MESURE" "ETA AVANT « FINI »"
printf '%.0s-' {1..74}; printf '\n'

now=$(date -u +%s)
certain=0; maybe=0; running_seen=0

# Couverture de l'arbre source : ce que `fetch` trouverait deja converti, sur ce
# que la liste de travail lui demande. Deux lectures de fichier, aucune requete.
WL=$(wc -l < "$ROOT/.local/state/worklist.txt" 2>/dev/null || echo 0)
CONV=$(find "$ROOT/data/sources/convert" -type f 2>/dev/null | wc -l)
COVER_PCT=0; MISSING=0
if [ "$WL" -gt 0 ]; then
  COVER_PCT=$(( CONV * 100 / WL ))
  MISSING=$(( WL - CONV ))
fi

while read -r verdict name; do
  rec="$STEPS/$name.json"
  dur=""; declined=""; status=""
  if [ -f "$rec" ]; then
    dur=$(grep -o '"duration_sec": *[0-9.]*' "$rec" | head -1 | grep -o '[0-9.]*$')
    grep -q '"declined": *true' "$rec" && declined=" (declin)"
    status=$(field "$rec" status)
  fi

  # Une etape EN COURS : l'ETA est ce qui reste sur sa propre horloge.
  if [ "$status" = "running" ]; then
    running_seen=1
    started=$(field "$rec" started_at)
    st=$(date -u -d "${started%.*}Z" +%s 2>/dev/null || echo "$now")
    elapsed=$(( now - st ))
    if [ -n "$dur" ]; then
      left=$(awk -v d="$dur" -v e="$elapsed" 'BEGIN{printf "%d", d-e}')
      if [ "$left" -ge 0 ]; then eta="$(hhmmss "$left")"; else eta="depasse de $(hhmmss "$left")"; fi
    else
      eta="inconnu"
    fi
    printf '%-17s %-6s %-11s EN COURS depuis %s, reste %s\n' \
      "$name" "RUN" "$(hhmmss "${dur:-0}")" "$(hhmmss "$elapsed")" "$eta"
    continue
  fi

  case "$verdict" in
    SKIP) printf '%-17s %-6s %-11s %s\n' "$name" "SKIP" "-" "deja fini, prouve"; continue ;;
  esac

  if [ -z "$dur" ]; then
    printf '%-17s %-6s %-11s %s\n' "$name" "$verdict" "(jamais)" "inconnu — aucune execution enregistree"
    continue
  fi

  secs=$(awk -v d="$dur" 'BEGIN{printf "%d", d}')

  # Une duree n'est une ETA que si le run qu'elle mesure a fait le travail.
  # Un echec en 0,01 s et un declin en 1,4 s sont des chiffres vrais qui
  # repondent a une autre question — les afficher comme des ETA est exactement
  # le genre de nombre juste et trompeur qui coute des heures ici.
  if [ "$status" = "failed" ]; then
    printf '%-17s %-6s %-11s %s\n' "$name" "$verdict" "$(hhmmss "$secs")" \
      "PAS UNE ETA — le dernier essai a PLANTE, la duree mesure l'echec"
    continue
  fi
  if [ -n "$declined" ]; then
    printf '%-17s %-6s %-11s %s\n' "$name" "$verdict" "$(hhmmss "$secs")" \
      "$(hhmmss "$secs") — le dernier run a DECLINE : mesure un refus, pas le travail"
    [ "$verdict" = "RUN?" ] && maybe=$(( maybe + secs )) || certain=$(( certain + secs ))
    continue
  fi

  # fetch est le seul dont la mesure peut avoir ete prise dans un autre monde :
  # les archives sont purgees apres conversion, donc une passe incrementale sur
  # un arbre plein ne dit rien d'une re-acquisition sur un arbre vide.
  if [ "$name" = "fetch" ] && [ "$COVER_PCT" -lt 90 ]; then
    printf '%-17s %-6s %-11s %s\n' "$name" "$verdict" "$(hhmmss "$secs")" \
      "MESURE PERIMEE — prise sur un arbre source complet ; il en manque $(( 100 - COVER_PCT )) %"
    printf '%-17s %-6s %-11s %s\n' "" "" "" \
      "  reel : ~$MISSING specs a re-acquerir et reconvertir (LibreOffice), compter en HEURES"
    [ "$verdict" = "RUN?" ] && maybe=$(( maybe + secs )) || certain=$(( certain + secs ))
    continue
  fi

  note=""
  [ "$verdict" = "RUN?" ] && note="  plafond : sautee si sa dependance n'a rien change"
  printf '%-17s %-6s %-11s %s%s%s\n' "$name" "$verdict" "$(hhmmss "$secs")" "$(hhmmss "$secs")" "$declined" "$note"

  if [ "$verdict" = "RUN?" ]; then maybe=$(( maybe + secs )); else certain=$(( certain + secs )); fi
done < <(sed -n 's/^ *\[\(SKIP\|RUN \|RUN?\)\] \([a-z-]*\) .*/\1 \2/p' "$plan" | sed 's/RUN  /RUN /')

printf '%.0s-' {1..74}; printf '\n'
printf 'CERTAIN                        %s\n' "$(hhmmss "$certain")"
printf 'SI TOUS LES RUN? SE CONFIRMENT %s  (plafond, pas une prevision)\n' "$(hhmmss "$maybe")"

if [ "$running_seen" = 0 ]; then
  printf '\nRien ne tourne. Les ETA ci-dessus sont ce que couterait `make build` maintenant.\n'
fi
printf 'arbre source : %s/%s converti (%s %%)' "$CONV" "$WL" "$COVER_PCT"
if [ "$COVER_PCT" -lt 90 ]; then
  printf ' — le total ci-dessus SOUS-ESTIME donc largement `fetch`.\n'
else
  printf ' — les mesures de `fetch` restent representatives.\n'
fi

# Une passe GPU a son propre compteur, bien plus fin que duration_sec : le ledger
# est append-only, donc `wc -l` est la seule mesure d'avancement qui ne mente pas.
for l in "$ROOT"/.local/vecs/*.jsonl; do
  [ -f "$l" ] || continue
  age=$(( now - $(stat -c %Y "$l") ))
  [ "$age" -lt 600 ] || continue
  printf 'ledger actif : %s — %s lignes (fige depuis %s)\n' \
    "$(basename "$l")" "$(wc -l < "$l")" "$(hhmmss "$age")"
done

printf '\ndetail vif d une passe d embedding : bash .local/resume/eta.sh\n'
