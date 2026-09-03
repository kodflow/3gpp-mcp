# mk/local.mk — local corpus pipeline (replaces Kaggle + the corpus workflows).
#
# THESE TARGETS ARE WRAPPERS. The pipeline — its steps, their dependencies, their
# fingerprints and their validations — is declared once, in internal/goal. Nothing
# here restates it, because every duplicate description eventually disagrees with
# the code, which is exactly how the retired CI drifted from reality.
#
#   make local-toolchain   one-off: portable toolchain (Go, mingw-UCRT, Rust, DuckDB,
#                          LibreOffice, ONNX Runtime, CUDA runtime) into .local/
#   make goal-plan         what would run, and why. Changes nothing.
#   make goal              bring the repo to the target state (resumes automatically)
#   make goal-status       what is valid right now, from persisted state alone
#   make goal-manifest     machine-readable provenance of the local build

# GOAL, GOAL_ENV and goal-bin live in the top-level Makefile and are NOT redefined
# here. They were, once, with a different default for GOTAGS and a different rule
# for when to relink — so `make build` and `make goal` were two orchestrators
# wearing one name, and only one of them was current. ENV is kept as an alias
# because the recipes below still read better with it.
ENV := $(GOAL_ENV)

.PHONY: goal goal-plan goal-status goal-resume goal-manifest \
        local-toolchain local-setup local-model local-clean local-clean-all \
        corpus-verify snapshot-smoke

# The four goal-* names are the older spelling of build/plan/status. They are
# aliases, not copies: one implementation, two vocabularies.
goal-plan: plan ## [goal] Differential plan: what would run and why. Changes nothing.

goal: build ## [goal] Bring the repo to the target state (resumable)

goal-resume: build ## [goal] Alias of `goal` — every step is resumable by construction

goal-status: status ## [goal] What is valid now, read from persisted state

goal-manifest: goal-bin ## [goal] Provenance manifest of the local build
	@$(ENV); "$(GOAL)" manifest

local-toolchain: ## [goal] Install the portable, elevation-free toolchain into .local/
	bash scripts/local/toolchain-bootstrap.sh

local-setup: ## [goal] WSL2/Linux toolchain (apt, Go, Rust, CUDA, LibreOffice)
	bash scripts/local/setup-wsl.sh

local-model: ## [goal] Fetch BGE-M3 ONNX (~2.3 GB) into data/models/
	bash scripts/fetch-model.sh

local-clean: ## [goal] Drop shards and step state; KEEP converted sources and the vector ledger
	rm -rf .local/shards .local/state/steps
	@echo "shards and step state cleared; converted HTML and .local/vecs/ledger.jsonl kept."

local-clean-all: ## [goal] Drop ALL local state, including the vector ledger (= full GPU re-embed)
	@printf 'This destroys the vector ledger (hours of GPU). Continue? [y/N] '; \
	 read a; [ "$$a" = "y" ] || { echo aborted; exit 1; }
	rm -rf .local/state .local/shards .local/vecs .local/logs
	@echo "local state cleared (toolchain kept)."

corpus-verify: goal-bin ## [goal] Strict corpus invariants — anchor, holes, contract. Exit != 0 on any violation.
	@set -e; $(ENV); \
	  .local/bin/anchorcheck$(if $(filter Windows_NT,$(OS)),.exe,) \
	    --db data/3gpp.duckdb --index .local/corpus-index.json \
	    --accept contracts/accepted-absences.txt

snapshot-smoke: ## [goal] Download the PUBLISHED artefact into a fresh dir, serve it, prove vectors are on
	@$(ENV); bash scripts/snapshot-smoke.sh $(ARGS)
