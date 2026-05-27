---
slug: weird
risk: low
loc_estimate_max: "8000-12000"
touches_public_api: false
touches_security_surface: false
touches_dev_infra: false
---

# Plan with non-numeric loc_estimate_max

The pre-v1.3 mistake — a string range where a number is expected.
AUTO mode must default to FULL.
