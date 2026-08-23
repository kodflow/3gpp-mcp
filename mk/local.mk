# mk/local.mk — indexation 3GPP EN LOCAL (remplace Kaggle + les 5 workflows corpus).
#
# Chaine complete sur une seule machine, avec cache et reprise a chaque etape :
#
#   make local-setup     une fois : toolchain WSL2 (apt, Go, Rust, CUDA, LibreOffice)
#   make corpus-seed     une fois : amorce la base avec le snapshot publie (evite
#                        37 Go de telechargement et ~30 h de conversion LibreOffice)
#   make corpus          LA commande : discover -> fetch -> ingest -> merge ->
#                        embed GPU -> enrich -> freeze HNSW -> validate
#
# Tout est reprenable : reste `make corpus` apres une interruption, chaque phase
# repart de son ledger (ingest_log, vecs/ledger.jsonl, corpus-index.json).

LOCAL_SH  := scripts/local/corpus-local.sh
DATA_DIR  := data
LOCAL_DIR := $(DATA_DIR)/local
DB        := $(DATA_DIR)/3gpp.duckdb
SEED_URL  := https://github.com/kodflow/3gpp-mcp/releases/download/latest/3gpp.duckdb.zst
SEED_IDX  := https://github.com/kodflow/3gpp-mcp/releases/download/latest/corpus-index.json

GOAL := .local/bin/goal$(if $(filter Windows_NT,$(OS)),.exe,)

.PHONY: goal goal-plan goal-status goal-resume goal-manifest goal-bin \
        local-setup local-model corpus corpus-full corpus-seed corpus-status \
        corpus-embed corpus-merge local-clean local-clean-all local-help

# ---------------------------------------------------------------------------
# La machine a etats. cmd/goal + internal/goal sont la SOURCE DE VERITE du
# pipeline (etapes, dependances, fingerprints, validations) ; ces cibles ne font
# que l'appeler. Ne jamais redecrire le pipeline ici.
# ---------------------------------------------------------------------------

goal-bin: ## [goal] (re)construit le binaire d'orchestration
	@set -e; . scripts/local/toolchain-env.sh; \
	  mkdir -p .local/bin; \
	  go build $${GOTAGS:+-tags $$GOTAGS} -o "$(GOAL)" ./cmd/goal

goal-plan: goal-bin ## [goal] Plan differentiel : ce qui tournerait, et pourquoi. Ne modifie rien.
	@. scripts/local/toolchain-env.sh; "$(GOAL)" plan $(ARGS)

goal: goal-bin ## [goal] Amene le depot a l'etat cible (reprise automatique)
	@. scripts/local/toolchain-env.sh; "$(GOAL)" run $(ARGS)

goal-resume: goal ## [goal] Alias de `goal` — chaque etape est reprenable par construction

goal-status: goal-bin ## [goal] Ce qui est valide maintenant, lu depuis l'etat persiste
	@. scripts/local/toolchain-env.sh; "$(GOAL)" status

goal-manifest: goal-bin ## [goal] Provenance machine-readable du build local
	@. scripts/local/toolchain-env.sh; "$(GOAL)" manifest

local-setup: ## [local] Installe la toolchain complete dans WSL2 (idempotent)
	bash scripts/local/setup-wsl.sh

local-model: ## [local] Telecharge BGE-M3 ONNX (~2,3 Go) dans data/models/
	bash scripts/fetch-model.sh

corpus-seed: ## [local] Amorce data/3gpp.duckdb + l'index delta depuis la release publiee
	@mkdir -p $(LOCAL_DIR)
	@if [ -s "$(DB)" ]; then \
	  echo "$(DB) existe deja — rien a faire (supprime-le pour re-amorcer)"; \
	else \
	  echo "==> snapshot lexical publie (~670 Mo compresses, 6,5 Go decompresses)"; \
	  curl -fL --retry 3 -o "$(DB).zst" "$(SEED_URL)"; \
	  zstd -d --long=27 -f "$(DB).zst" -o "$(DB)"; \
	  rm -f "$(DB).zst"; \
	fi
	@echo "==> corpus-index.json (ancre du delta)"
	@curl -fsSL --retry 3 -o "$(LOCAL_DIR)/corpus-index.json" "$(SEED_IDX)" \
	  && echo "    index amorce : $$(wc -c < $(LOCAL_DIR)/corpus-index.json) octets" \
	  || echo "    index indisponible — la premiere passe sera un FULL"
	@echo "amorce OK. Prochaine etape : make corpus"

corpus: ## [local] Pipeline complet en delta+reprise (LA commande)
	bash $(LOCAL_SH) $(ARGS)

corpus-full: ## [local] Rebuild integral (ignore l'index, reindexe toutes les series)
	bash $(LOCAL_SH) --full $(ARGS)

corpus-embed: ## [local] Rejoue seulement l'embed GPU (+ freeze + validate)
	bash $(LOCAL_SH) --from embed $(ARGS)

corpus-merge: ## [local] Rejoue seulement merge -> enrich -> freeze -> validate
	bash $(LOCAL_SH) --from merge $(ARGS)

corpus-status: ## [local] Ou en est le corpus local (tailles, couverture, vecteurs)
	bash scripts/local/status.sh

local-clean: ## [local] Efface les shards et l'etat, GARDE sources converties + ledger de vecteurs
	rm -rf $(LOCAL_DIR)/shards $(LOCAL_DIR)/state
	@echo "shards + etat effaces. HTML converti et vecs/ledger.jsonl conserves."

local-clean-all: ## [local] Efface TOUT le local (y compris le ledger de vecteurs = re-embed GPU complet)
	@printf 'Cela detruit le ledger de vecteurs (~3,4 Go, plusieurs heures de GPU). Continuer ? [y/N] '; \
	read a; [ "$$a" = "y" ] || { echo annule; exit 1; }
	rm -rf $(LOCAL_DIR)
	@echo "local efface."

local-help: ## [local] Aide detaillee du pipeline local
	@bash $(LOCAL_SH) --help
