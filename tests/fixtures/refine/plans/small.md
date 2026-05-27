---
slug: small
risk: low
loc_estimate_max: 100
touches_public_api: false
touches_security_surface: false
touches_dev_infra: false
---

# Small plan

A trivial change. Add a `--verbose` flag to `script.sh`.

## Steps

1. Edit script.sh: parse `--verbose`.
2. Add 1 bats test.
3. Commit.
