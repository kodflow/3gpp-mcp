# Generic Lint-Fix-Iterate (18+ Languages)

> Invoked from lint.md for any language except Go-with-ktn-linter.
> Follows the same Makefile-first pattern as lint.sh and on-stop-quality.sh.

## Tool Mapping

Select tools based on the detected language. Use the **first available** tool in each category.

| Language | Linter (check + fix) | Type Checker | Formatter |
|----------|---------------------|--------------|-----------|
| **Python** | `ruff check --fix .` | `mypy --strict .` | `ruff format .` |
| **Node.js/TS** | `eslint --fix .` | `tsc --noEmit` | `prettier --write .` |
| **Rust** | `cargo clippy --fix --allow-dirty` | `cargo check` | `cargo fmt` |
| **Go** | `golangci-lint run --fix` | `go vet ./...` | `goimports -w .` |
| **Java** | `checkstyle -c google_checks.xml` | `javac -Xlint:all -Werror` | (build system) |
| **C/C++** | `clang-tidy --fix` | (compiler) | `clang-format -i` |
| **C#** | `dotnet build /warnaserror` | (Roslyn) | `dotnet format` |
| **VB.NET** | `dotnet build /warnaserror` | (Roslyn) | `dotnet format` |
| **Ruby** | `rubocop -A` | `steep check` or `srb tc` | (rubocop) |
| **PHP** | `phpstan analyse -l max` | (phpstan) | `php-cs-fixer fix .` |
| **Kotlin** | `ktlint '**/*.kt'` | `detekt --all-rules` | `ktlint --format` |
| **Swift** | `swiftlint lint --strict` | `swift build` | `swiftformat .` |
| **Elixir** | `mix credo --strict` | `mix dialyzer` | `mix format` |
| **Dart** | `dart analyze --fatal-infos` | (dart analyze) | `dart format .` |
| **Scala** | `scalafix --check` | `scalac -Werror` | `scalafmt` |
| **Lua** | `luacheck .` | N/A | `stylua .` |
| **Perl** | `perlcritic --stern lib/` | `perl -c` | `perltidy -b` |
| **R** | `Rscript -e "lintr::lint_package()"` | N/A | `Rscript -e "styler::style_pkg()"` |
| **Fortran** | `gfortran -fsyntax-only -Wall -Wextra` | N/A | `fprettify` |
| **Ada** | `gnat make -gnatc -gnatwa -gnatwe` | `gnatprove` | `gnatpp` |
| **COBOL** | `cobc -Wall -fsyntax-only` | N/A | N/A |
| **Pascal** | `fpc -Mobjfpc -Se` | (compiler) | `ptop` |
| **Shell** | `shellcheck` | N/A | `shfmt -w` |

---

## Execution Workflow

### Step 1: Format first

```text
Run formatter for detected language (see table above).
Formatting before linting avoids style-only lint noise.
```

### Step 2: Run linter (check mode)

```bash
# Example for Python:
ruff check . 2>&1
```

Parse the output to count issues. If 0 issues, skip to Step 5.

### Step 3: Fix issues

Two strategies based on tool capabilities:

**Auto-fix mode** (preferred): If the linter supports `--fix`:
```bash
ruff check --fix .
eslint --fix .
cargo clippy --fix --allow-dirty
rubocop -A
```

**Manual-fix mode**: If no auto-fix (e.g., phpstan, checkstyle):
1. Parse each issue (file, line, message)
2. Read the affected file
3. Apply the fix with Edit tool
4. Move to next issue

### Step 4: Re-run for convergence

```text
Re-run linter after fixes.
IF still issues AND iteration < 5:
  → Go back to Step 3
IF still issues AND iteration >= 5:
  → Report remaining issues and stop
IF 0 issues:
  → Continue to Step 5
```

### Step 5: Type checking (if available)

```text
Run type checker for detected language (see table above).
IF type errors found:
  → Fix type errors (Edit tool)
  → Re-run type checker
  → Max 3 iterations for type checking
```

### Step 6: Final verification

```text
Re-run linter one last time to confirm 0 issues.
Report results.
```

---

## Report Format

```text
═══════════════════════════════════════════════════════════════
  /lint [{language}] - COMPLETE
═══════════════════════════════════════════════════════════════

  Language         : Python
  Tools used       : ruff (lint), mypy (typecheck), ruff (format)
  Issues fixed     : 23
  Type errors fixed: 4
  Iterations       : 2

  Final verification: 0 issues

═══════════════════════════════════════════════════════════════
```

---

## Tool Availability Check

Before running any tool, verify it exists:

```bash
command -v ruff &>/dev/null
```

If the primary tool is not available, skip with a clear message:

```text
WARNING: ruff not installed. Skipping Python linting.
Install with: pip install ruff
```

Do NOT fail the entire `/lint` run because one language's tool is missing.

---

## ABSOLUTE RULES

1. **Fix EVERYTHING** - No exceptions, no skips (within iteration limits)
2. **Format before lint** - Reduces style noise
3. **Iterate until convergence** - Max 5 iterations for lint, 3 for typecheck
4. **Tool-missing = skip with warning** - Never block on missing tools
5. **No questions** - Everything is automatic
6. **TaskCreate** - One task per language with progress tracking
