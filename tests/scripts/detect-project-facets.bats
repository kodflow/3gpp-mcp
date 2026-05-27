#!/usr/bin/env bats
# detect-project-facets.bats — Skills Architecture v1.3 (PR2a)
# WHY: pin the new facets emitted by detect-project.sh so the router has
# stable inputs to condition on.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  SCRIPT="$REPO_ROOT/.devcontainer/images/.claude/scripts/detect-project.sh"
  TMP="$(mktemp -d)"
}
teardown() { rm -rf "$TMP"; }

run_detect() { bash "$SCRIPT" "$TMP"; }

@test "TestDetectProjectFacet_Cloud" {
  cat > "$TMP/main.tf" <<'TF'
provider "aws" { region = "us-east-1" }
TF
  out=$(run_detect)
  echo "$out" | jq -e '.cloud | contains(["aws"])'
}

@test "TestDetectProjectFacet_Container" {
  : > "$TMP/Dockerfile"
  out=$(run_detect)
  echo "$out" | jq -e '.container | contains(["docker"])'
}

@test "TestDetectProjectFacet_K8s" {
  mkdir -p "$TMP/manifests"
  out=$(run_detect)
  [ "$(echo "$out" | jq -r '.k8s')" = "true" ]
}

@test "TestDetectProjectFacet_OS" {
  out=$(run_detect)
  echo "$out" | jq -e '.os | inside(["linux","macos","windows","unknown"])' \
    || echo "$out" | jq -e '(.os | type) == "string"'
}

@test "TestDetectProjectFacet_CI" {
  mkdir -p "$TMP/.github/workflows"
  : > "$TMP/.github/workflows/ci.yml"
  out=$(run_detect)
  [ "$(echo "$out" | jq -r '.ci')" = "github" ]
}

@test "TestDetectProjectFacet_TestFrameworks" {
  cat > "$TMP/package.json" <<'JSON'
{"name":"x","devDependencies":{"@playwright/test":"^1.0.0"}}
JSON
  out=$(run_detect)
  echo "$out" | jq -e '.test_frameworks | contains(["playwright"])'
}
