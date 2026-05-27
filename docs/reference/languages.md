# Supported Languages

25 languages are installed via DevContainer features. Each is an independent `install.sh` script in `.devcontainer/features/languages/`.

## Overview

| Language | Linter | Formatter | Tests | Security |
|----------|--------|-----------|-------|----------|
| **Python** | ruff, pylint | ruff | pytest | bandit |
| **Go** | golangci-lint | gofumpt | go test, gotestsum | gosec |
| **Node.js** | eslint | prettier | jest | npm audit |
| **Rust** | clippy | rustfmt | cargo test, cargo-nextest | cargo-deny |
| **Java** | checkstyle | — | junit | — |
| **C/C++** | clang-tidy | clang-format | googletest | cppcheck, valgrind |
| **C#** | fxcop | dotnet-format | nunit | — |
| **Ruby** | rubocop | rubocop | rspec | bundler-audit |
| **PHP** | phpstan | php-cs-fixer | phpunit | composer audit |
| **Kotlin** | detekt | ktlint | junit | — |
| **Swift** | swiftlint | swiftformat | xtest | — |
| **Scala** | scalastyle | scalafmt | scalatest | — |
| **Elixir** | credo | mix format | exunit | — |
| **Dart/Flutter** | dart analyzer | dartfmt | flutter test | — |
| **R** | lintr | styler | testthat | — |
| **Perl** | perl::critic | perl::tidy | — | — |
| **Lua** | luacheck | stylua | — | — |
| **Fortran** | fprettify | fprettify | — | — |
| **Ada** | — | — | — | — |
| **Pascal** | — | — | — | — |
| **Assembly** | — | — | — | — |
| **MATLAB/Octave** | — | — | — | — |
| **COBOL** | — | — | — | — |
| **VB.NET** | — | — | xunit | — |

## Advanced Features

### WebAssembly Support

Go (via TinyGo), Rust, Node.js (via AssemblyScript), and C# support WebAssembly compilation.

### Desktop Support

Go (Wails), Rust (Tauri), Node.js (Electron), C#, Swift, and Dart/Flutter support desktop application development.

### Version Management

Python (pyenv), Ruby (rbenv), PHP, Node.js (nvm), and Swift allow version selection via feature options in `devcontainer.json`.

## How It Installs

Each language is an `install.sh` script that:

1. Detects the architecture (amd64/arm64)
2. Downloads precompiled binaries (or compiles if unavailable)
3. Installs the linter, formatter, and test tools in parallel
4. Uses `feature-utils.sh` for version detection via the GitHub API

Installations are parallelized (`&` + `wait`) to reduce build time.
