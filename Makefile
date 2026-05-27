SCRIPTS_DIR := .devcontainer/images/.claude/scripts
BATS := bats

.PHONY: test coverage lint

## Run bats tests
test:
	$(BATS) tests/**/*.bats

## Run tests with kcov coverage
coverage:
	@mkdir -p ./coverage
	kcov --exclude-path=/tmp,/usr \
		--include-pattern=$(SCRIPTS_DIR) \
		./coverage \
		$(BATS) tests/**/*.bats

## Run shellcheck on all scripts
lint:
	shellcheck -x $(SCRIPTS_DIR)/*.sh
