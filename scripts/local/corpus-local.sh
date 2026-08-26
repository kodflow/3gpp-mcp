#!/usr/bin/env bash
#
# corpus-local.sh — LE pipeline d'indexation 3GPP, en local, sur une seule machine.
#
# Remplace : corpus-matrix.yml + corpus-data-image.yml + corpus-embed-orchestrator.yml
#            + corpus-sparse-orchestrator.yml + corpus-rust-embed-kaggle.yml
#            + corpus-sparse-kaggle.yml + scripts/kaggle/*.py
# soit ~200 Ko de YAML et 5 canaux d'artefacts (3 GHCR + 2 releases) -> un script.
#
#   scripts/local/corpus-local.sh                 # tout, en reprise
#   scripts/local/corpus-local.sh --from embed    # repart a l'embed
#   scripts/local/corpus-local.sh --only ingest   # une seule phase
#   scripts/local/corpus-local.sh --scope "23 24 29 33 38"
#
# ---- ORDRE DES PHASES, et pourquoi ----------------------------------------
#
# La CI embeddait CHAQUE shard separement puis fusionnait. On fait l'inverse :
# on fusionne d'abord, on embedde ENSUITE, une seule fois, sur la DB fusionnee.
#
# Raison : `embedder` porte deux mecanismes de reprise dans UN seul ledger --
# `done` (HashSet<chunk_id>) et `by_hash` (content-hash -> vecteur). Or `ingest`
# rebase les chunk_id a ~0 dans chaque shard : deux shards contiennent tous deux
# un chunk_id 42. Partager un ledger entre shards ferait donc SAUTER des clauses
# par collision de chunk_id (rust/embedder/src/main.rs:263) -- perte silencieuse.
# Apres le merge, les chunk_id sont globalement uniques (offset applique par
# fold_shard), donc UN seul ledger est a la fois sur et optimal :
#
#   `by_hash` donne la dedup de contenu SUR TOUT LE CORPUS, toutes releases et
#   toutes series confondues. Mesure sur les 2 855 712 clauses reelles :
#     2 282 337 clauses embeddables -> 833 924 textes distincts = facteur 2,74x
#     79,8 % des clauses sont dupliquees A L'IDENTIQUE ENTRE RELEASES
#   -> le GPU ne voit que 834 k textes au lieu de 2,28 M.
#
# ---- LE DELTA --------------------------------------------------------------
#
# `merge --index-out` ecrit corpus-index.json (spec|Rel -> version max) ;
# `discover --index` le relit pour ne re-fetcher que ce qui a bouge sur 3gpp.org.
# En CI cette boucle etait CASSEE (plus personne ne republiait l'index depuis le
# nettoyage de 2026-06) : le delta etait ancre sur une photo gelee. En local le
# fichier vit a cote de la DB -- la boucle se referme gratuitement.
#
set -uo pipefail
# shellcheck source=lib-local.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib-local.sh"

ALL_PHASES=(discover fetch ingest merge embed enrich freeze validate)
PHASES=""; FROM=""; ONLY=""
SCOPE="${SCOPE:-}"            # series explicites ; vide = delta auto
FLOOR="${FLOOR:-Rel-99}"      # Rel-99 = toutes les vraies releases 3GPP
JOBS="${JOBS:-6}"             # workers de conversion ; 6 mesure 21 % plus rapide que 4
                              # (A/B/B/A sur 28 documents : 225 s a 4, 178 s a 6)
BASE_DB="${BASE_DB:-$DB_OUT}" # base de depart du merge incremental
FULL=0; DRY=0; PURGE_ZIP="${PURGE_ZIP:-0}"   # on garde les caches par defaut
EMBED_FLOOR="${EMBED_FLOOR:-Rel-99}"
STATUS_URL="https://www.3gpp.org/DynaReport/status-report.htm"

while [ $# -gt 0 ]; do
  case "$1" in
    --from)        FROM="$2"; shift 2;;
    --only)        ONLY="$2"; shift 2;;
    --scope)       SCOPE="$2"; shift 2;;
    --floor)       FLOOR="$2"; shift 2;;
    --jobs)        JOBS="$2"; shift 2;;
    --base)        BASE_DB="$2"; shift 2;;
    --embed-floor) EMBED_FLOOR="$2"; shift 2;;
    --full)        FULL=1; shift;;
    --keep-zip)    PURGE_ZIP=0; shift;;   # retro-compat : c'est deja le defaut
    --purge-zip)   PURGE_ZIP=1; shift;;
    --dry-run)     DRY=1; shift;;
    -h|--help)     sed -n '2,45p' "$0"; exit 0;;
    *) die "argument inconnu: $1";;
  esac
done

# Selection des phases
if [ -n "$ONLY" ]; then
  PHASES="${ONLY//,/ }"
elif [ -n "$FROM" ]; then
  seen=0; PHASES=""
  for p in "${ALL_PHASES[@]}"; do
    [ "$p" = "$FROM" ] && seen=1
    [ "$seen" = 1 ] && PHASES="$PHASES $p"
  done
  [ -n "$PHASES" ] || die "phase inconnue: $FROM (choix: ${ALL_PHASES[*]})"
else
  PHASES="${ALL_PHASES[*]}"
fi

runs() { case " $PHASES " in *" $1 "*) return 0;; *) return 1;; esac; }
run()  { if [ "$DRY" = 1 ]; then dim "DRY: $*"; else "$@"; fi; }

START_TS=$(date +%s)
trap 'printf "\n"; warn "interrompu -- relance la meme commande, tout est reprenable"; exit 130' INT TERM

# ================================================================ 0. binaires
build_binaries() {
  PHASE=build
  need cargo; need go
  # workspace : ingest (+ ingest-catalog/openapi/li) et store (merge/overlay/
  # freeze-hnsw/embed-io) partagent un target/ pour ne compiler libduckdb qu'une fois.
  cargo_build rust/ingest/Cargo.toml --bin ingest --bin ingest-catalog --bin ingest-openapi --bin ingest-li
  cargo_build rust/store/Cargo.toml  --bin merge --bin overlay --bin freeze-hnsw --bin embed-io
  cargo_build rust/discover/Cargo.toml --bin discover
  if runs embed; then
    # embedder est HORS workspace : il tire ort + CUDA.
    cargo_build rust/embedder/Cargo.toml --bin embedder
  fi
  ok "binaires prets dans $RUST_BIN"
}

# ============================================================= 1. discover
phase_discover() {
  PHASE=discover
  local sf="$LOCAL_DIR/status-report.htm"
  log "recuperation du status report 3GPP"
  # 3gpp.org renvoie 403 aux user-agents robots -> UA navigateur, avec retry.
  run curl -fsSL --retry 5 --retry-delay 3 -A "Mozilla/5.0 (X11; Linux x86_64) discover" \
      "$STATUS_URL" -o "$sf" || die "status report inaccessible"
  dim "$(wc -c < "$sf") octets"

  local args=(--status-file "$sf" --floor "$FLOOR")
  if [ "$FULL" = 1 ]; then
    args+=(--all); warn "mode --full : TOUTES les series seront reindexees (ignore l'index)"
  elif [ -s "$CORPUS_INDEX" ]; then
    args+=(--index "$CORPUS_INDEX"); dim "delta vs $CORPUS_INDEX ($(wc -c < "$CORPUS_INDEX") octets)"
  else
    warn "aucun corpus-index.json local -> premiere passe = FULL"
  fi
  [ -s "$ABSENT_INDEX" ] && args+=(--absent-index "$ABSENT_INDEX")
  [ -n "$SCOPE" ] && args+=(--series "$SCOPE")

  local series_json
  series_json="$("$RUST_BIN/discover" "${args[@]}" 2>"$LOG_DIR/discover.err")" \
    || { cat "$LOG_DIR/discover.err" >&2; die "discover a echoue"; }
  printf '%s\n' "$series_json" > "$LOCAL_DIR/series.json"

  # Worklist complete (release url nom) pour le fetch.
  if [ -n "$SCOPE" ]; then
    "$RUST_BIN/discover" --status-file "$sf" --floor "$FLOOR" --series "$SCOPE" --emit-worklist \
      > "$LOCAL_DIR/worklist.txt" 2>>"$LOG_DIR/discover.err" || die "discover --emit-worklist a echoue"
  else
    "$RUST_BIN/discover" --status-file "$sf" --floor "$FLOOR" --emit-worklist \
      > "$LOCAL_DIR/worklist.txt" 2>>"$LOG_DIR/discover.err" || die "discover --emit-worklist a echoue"
  fi

  local n_series n_work
  n_series="$(tr -d '[]"' < "$LOCAL_DIR/series.json" | tr ',' ' ' | wc -w)"
  n_work="$(wc -l < "$LOCAL_DIR/worklist.txt")"
  ok "$n_series serie(s) a traiter - worklist = $n_work (spec,release)"
  dim "series : $(tr -d '[]"' < "$LOCAL_DIR/series.json" | tr ',' ' ')"
}

# ==================================================== 2. fetch + convert (CPU)
phase_fetch() {
  PHASE=fetch
  guard_disk 25
  local series
  series="$(tr -d '[]"' < "$LOCAL_DIR/series.json" 2>/dev/null | tr ',' ' ' | xargs || true)"
  [ -n "$SCOPE" ] && series="$SCOPE"
  if [ -z "$series" ]; then ok "rien a recuperer (delta vide)"; return 0; fi

  need soffice
  log "telechargement + conversion LibreOffice -- series : $series"
  dim "corpus.sh est incremental : il ne retelecharge ni ne reconvertit l'existant"
  # corpus.sh gere deja : flock, enumeration parallele, retry, profil soffice
  # jetable par appel, retry EMF/WMF sur crash, tag .degraded.tsv.
  run env SET="$FLOOR" "$LOCAL_ROOT/scripts/corpus.sh" \
      --set "$FLOOR" --jobs "$JOBS" --series "$series" \
    || warn "corpus.sh est sorti non-zero -- on continue avec ce qui a ete converti"

  purge_zips
  dim "HTML converti : $(find "$SRC_CONVERT" -name '*.html' 2>/dev/null | wc -l) fichiers"
  ok "fetch/convert termine"
}

# PURGE DES ZIP -- DESACTIVEE PAR DEFAUT depuis le 2026-08-26.
#
# Le raisonnement d'origine tenait quand le disque etait plein a 98 % : le .zip
# ne sert plus une fois le .html produit, donc on le jetait au fil de l'eau. Ce
# qu'il ne disait pas, c'est le prix de le refaire. Le cache de HTML a fini par
# etre purge lui aussi, et repartir de zero coute le retelechargement ET la
# reconversion LibreOffice de ~20 000 specs -- des heures, a chaque fois qu'une
# etape amont change.
#
# Le disque n'est plus la contrainte (168 Go libres). La regle s'inverse donc :
# ON GARDE TOUT par defaut, et il faut demander explicitement la purge
# (PURGE_ZIP=1, ou --purge-zip) pour la retrouver. Un cache qu'on jette est un
# cache qu'on repaie.
purge_zips() {
  [ "${PURGE_ZIP:-0}" = "1" ] || { dim "zips conserves (PURGE_ZIP=1 pour purger)"; return 0; }
  local freed=0 n=0 z rel base sz
  while IFS= read -r z; do
    rel="$(basename "$(dirname "$z")")"
    base="$(basename "$z" .zip)"
    if compgen -G "$SRC_CONVERT/$rel/${base}*.html" >/dev/null 2>&1; then
      sz="$(stat -c%s "$z" 2>/dev/null || echo 0)"
      freed=$((freed + sz)); n=$((n + 1)); rm -f "$z"
    fi
  done < <(find "$SRC_ORIGIN" -name '*.zip' -type f 2>/dev/null)
  [ "$n" -gt 0 ] && ok "purge : $n zip supprimes ($(human "$freed")) -- HTML conserve"
  return 0
}

# ============================================================ 3. ingest (CPU)
phase_ingest() {
  PHASE=ingest
  local series
  series="$(tr -d '[]"' < "$LOCAL_DIR/series.json" 2>/dev/null | tr ',' ' ' | xargs || true)"
  [ -n "$SCOPE" ] && series="$SCOPE"
  if [ -z "$series" ]; then ok "rien a ingerer (delta vide)"; return 0; fi

  # 1 shard = 1 serie. Le shard est CONSERVE : c'est l'unite de rebuild
  # incremental (merge --base remplace le bucket (spec,release) correspondant).
  local pids=() tags=() s db p i fail
  for s in $series; do
    db="$(shard_db "$s")"
    (
      PHASE="ingest:$s"
      # `--resume` s'appuie sur la table ingest_log stampee PIPELINE_VERSION :
      # un changement de parser/chunking/schema invalide le log, c'est voulu.
      "$RUST_BIN/ingest" --series "$s" --convert "$SRC_CONVERT" --db "$db" --resume \
        >"$LOG_DIR/ingest-$s.log" 2>&1
    ) &
    pids+=($!); tags+=("$s")
    while [ "$(jobs -rp | wc -l)" -ge "$JOBS" ]; do wait -n 2>/dev/null || break; done
  done
  fail=0; i=0
  for p in "${pids[@]}"; do
    if ! wait "$p"; then
      warn "shard ${tags[$i]} en echec -- voir $LOG_DIR/ingest-${tags[$i]}.log"
      fail=$((fail + 1))
    fi
    i=$((i + 1))
  done
  [ "$fail" -eq 0 ] || warn "$fail shard(s) en echec sur ${#tags[@]}"
  ok "$(find "$SHARD_DIR" -name '*.duckdb' | wc -l) shard(s) presents dans $SHARD_DIR"
}

# ============================================================= 4. merge (CPU)
phase_merge() {
  PHASE=merge
  local shards=() f args=()
  while IFS= read -r f; do shards+=("$f"); done < <(find "$SHARD_DIR" -name '*.duckdb' | sort)

  if [ "${#shards[@]}" -eq 0 ]; then
    if [ -s "$BASE_DB" ]; then
      ok "aucun shard neuf -- la base $BASE_DB reste la DB courante"
      [ -s "$CORPUS_INDEX" ] || warn "corpus-index.json absent : le prochain delta repartira de zero"
      return 0
    fi
    die "aucun shard a fusionner et aucune base"
  fi

  guard_disk 20
  args=(--out "$DB_OUT.new"
        --index-out "$CORPUS_INDEX"
        --subject-index-out "$SUBJECT_INDEX"
        --build-index-out "$BUILD_INDEX"
        --no-hnsw)   # l'index HNSW se construit APRES l'embed (phase freeze)
  if [ -s "$BASE_DB" ] && [ "$FULL" != 1 ]; then
    args+=(--base "$BASE_DB")
    dim "merge incremental sur $BASE_DB ($(human "$(stat -c%s "$BASE_DB")"))"
    dim "chaque shard REMPLACE ses buckets (spec_id, release) dans la base"
  fi
  log "fusion de ${#shards[@]} shard(s)"
  run "$RUST_BIN/merge" "${args[@]}" "${shards[@]}" || die "merge a echoue"
  run mv -f "$DB_OUT.new" "$DB_OUT"
  ok "DB fusionnee : $DB_OUT ($(human "$(stat -c%s "$DB_OUT")"))"
  dim "index du delta reecrit : $CORPUS_INDEX -- la boucle est refermee"
}

# ============================================================== 5. embed (GPU)
phase_embed() {
  PHASE=embed
  [ -s "$DB_OUT" ] || die "pas de DB a embedder ($DB_OUT)"
  [ -d "$MODEL_DIR" ] || die "modele absent : $MODEL_DIR -- lance 'make local-model'"
  have_gpu || die "aucun GPU visible (nvidia-smi) -- l'embed CPU serait ~100x plus lent"

  local eid idfile wl todo
  eid="$(embed_identity)"
  dim "identite d'embed : $eid"

  # Si l'identite a change, les vecteurs du ledger ne sont plus valides : le
  # clause_hash englobe l'identite, donc by_hash ne matchera plus rien. On archive
  # plutot que d'effacer.
  idfile="$VEC_DIR/.identity"
  if [ -f "$idfile" ] && [ "$(cat "$idfile")" != "$eid" ]; then
    warn "identite d'embed changee ($(cat "$idfile") -> $eid) : le ledger est perime"
    run mv -f "$VEC_LEDGER" "$VEC_LEDGER.$(cat "$idfile").bak" 2>/dev/null || true
  fi
  printf '%s\n' "$eid" > "$idfile"

  wl="$VEC_DIR/worklist.jsonl"
  log "export de la work-list (clauses sans vecteur, floor=$EMBED_FLOOR)"
  run "$RUST_BIN/embed-io" --db "$DB_OUT" --export-worklist "$wl" --embed-floor "$EMBED_FLOOR" \
    || die "embed-io --export-worklist a echoue"
  todo="$(wc -l < "$wl" 2>/dev/null || echo 0)"
  if [ "$todo" -eq 0 ]; then ok "aucune clause a embedder -- deja complet"; return 0; fi
  log "$todo clause(s) sans vecteur"

  # LE point cle. Un ledger UNIQUE pour tout le corpus :
  #   - `done`    -> reprise exacte apres interruption (chunk_id globalement unique
  #                  depuis le merge, donc aucune collision possible)
  #   - `by_hash` -> dedup de CONTENU : une clause dont le texte a deja ete embedde
  #                  sous un autre chunk_id (autre release, autre serie) est remplie
  #                  par COPIE, sans jamais toucher le GPU.
  # Attendu sur le corpus complet : ~2,28 M clauses -> ~834 k passages GPU (2,74x).
  log "embed dense GPU (BGE-M3) -- ledger $VEC_LEDGER"
  dim "le GPU ne verra que les textes DISTINCTS ; les doublons inter-release sont copies"
  run "$RUST_BIN/embedder" \
      --in "$wl" --out "$VEC_LEDGER" \
      --model-dir "$MODEL_DIR" --embed-identity "$eid" \
      --require-cuda --vram-fraction 0.8 --max-batch 512 \
    || die "embedder a echoue (relance : tout est repris depuis le ledger)"

  log "import des vecteurs dans la DB"
  run "$RUST_BIN/embed-io" --db "$DB_OUT" --import-vectors "$VEC_LEDGER" --embed-identity "$eid" \
    || die "embed-io --import-vectors a echoue"
  ok "vecteurs importes - ledger $(human "$(stat -c%s "$VEC_LEDGER" 2>/dev/null || echo 0)")"
}

# ============================================================= 6. enrich (CPU)
phase_enrich() {
  PHASE=enrich
  [ -s "$DB_OUT" ] || die "pas de DB ($DB_OUT)"
  local sf="$LOCAL_DIR/status-report.htm" asn
  # OBLIGATOIRE, pas optionnel : sans lui doc_type reste "TS" en dur pour TOUTES
  # les specs (rust/parse force la valeur), working_group et title restent vides,
  # et le gate --max-empty-meta de cmd/validate echoue.
  log "overlay catalogue DynaReport (doc_type TS/TR, working_group, freeze_date)"
  if [ -s "$sf" ]; then
    run "$RUST_BIN/ingest-catalog" --db "$DB_OUT" --status-report "$sf" \
      || warn "ingest-catalog a echoue -- doc_type restera 'TS' partout"
  else
    run "$RUST_BIN/ingest-catalog" --db "$DB_OUT" \
      || warn "ingest-catalog a echoue -- doc_type restera 'TS' partout"
  fi

  if [ -d "$DATA/sources/5g-apis" ]; then
    log "overlay OpenAPI 5GC"
    run "$RUST_BIN/ingest-openapi" --src "$DATA/sources/5g-apis" --db "$DB_OUT" \
      || warn "ingest-openapi a echoue"
  else
    dim "pas de data/sources/5g-apis -- 'make fetch-apis' pour les recuperer"
  fi

  # ingest-li n'est invoque par AUCUN workflow ni aucune cible make aujourd'hui,
  # alors que li_events/asn1_types sont au schema et que le subject `li` a une
  # empreinte dans identity3gpp::SUBJECTS. On le cable ici.
  asn="$(find "$SRC_CONVERT" "$DATA/sources" -name 'TS33128Payloads*.asn' 2>/dev/null | head -1)"
  if [ -n "$asn" ]; then
    log "registre Lawful Interception depuis $(basename "$asn")"
    run "$RUST_BIN/ingest-li" --db "$DB_OUT" --asn "$asn" || warn "ingest-li a echoue"
  else
    dim "aucun TS33128Payloads.asn trouve -- li_events restera vide"
  fi
  ok "enrichissement termine"
}

# ========================================================= 7. freeze HNSW (RAM)
phase_freeze() {
  PHASE=freeze
  local n ram
  n="$("$RUST_BIN/embed-io" --db "$DB_OUT" --report 2>/dev/null \
       | grep -o '"embedded_clauses":[0-9]*' | cut -d: -f2)"
  if [ "${n:-0}" -eq 0 ]; then warn "aucun vecteur en base -- HNSW saute"; return 0; fi
  # Le build HNSW charge tous les vecteurs en RAM (2,85 M x 1024 x 4 o ~= 11,7 Go)
  # plus le graphe. La CI y arrivait sur un runner de 7 Go de RAM en armant un
  # swapfile de 28 Go : c'est donc la memoire ADRESSABLE (RAM + swap) qui compte,
  # pas la RAM seule. On garde le meme seuil de 24 Go mais sur le total.
  local swap total
  ram="$(awk '/MemTotal/{print int($2/1024/1024)}' /proc/meminfo 2>/dev/null || echo 0)"
  swap="$(awk '/SwapTotal/{print int($2/1024/1024)}' /proc/meminfo 2>/dev/null || echo 0)"
  total=$((ram + swap))
  if [ "$total" -lt 24 ]; then
    warn "memoire adressable ${total}Go (RAM ${ram} + swap ${swap}) < 24Go : HNSW saute"
    warn "la recherche vectorielle marche quand meme, par scan exact (correct, plus lent)"
    warn "pour l'activer : %USERPROFILE%\\.wslconfig -> [wsl2] memory=20GB / swap=16GB, puis 'wsl --shutdown'"
    return 0
  fi
  [ "$ram" -lt 16 ] && warn "seulement ${ram}Go de RAM : le build va swapper, comptez plusieurs heures"
  log "construction + gel de l'index HNSW cosine sur $n vecteurs (RAM ${ram}Go + swap ${swap}Go)"
  run "$RUST_BIN/freeze-hnsw" --db "$DB_OUT" || die "freeze-hnsw a echoue"
  ok "HNSW gele"
}

# ================================================================ 8. validate
phase_validate() {
  PHASE=validate
  need go
  local flags
  flags="$("$LOCAL_ROOT/scripts/data-contract.sh")"
  log "gate de completude : $flags"
  # shellcheck disable=SC2086
  if ( cd "$LOCAL_ROOT" && CGO_ENABLED=1 go run ./cmd/validate --db "$DB_OUT" $flags --report text ); then
    ok "DB VALIDEE -- $DB_OUT est complete pour le contrat courant"
  else
    warn "validation en echec -- la DB est utilisable mais incomplete (voir ci-dessus)"
  fi
}

# ================================================================== execution
log "phases : $PHASES"
dim "floor=$FLOOR scope=${SCOPE:-<delta auto>} jobs=$JOBS base=$BASE_DB"
build_binaries
for phase in $PHASES; do
  case "$phase" in
    discover) phase_discover;;
    fetch)    phase_fetch;;
    ingest)   phase_ingest;;
    merge)    phase_merge;;
    embed)    phase_embed;;
    enrich)   phase_enrich;;
    freeze)   phase_freeze;;
    validate) phase_validate;;
    *) die "phase inconnue: $phase";;
  esac
done
PHASE=local
ok "termine en $(( ($(date +%s) - START_TS) / 60 )) min - DB = $DB_OUT"
