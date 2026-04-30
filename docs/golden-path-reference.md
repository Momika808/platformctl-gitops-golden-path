# Golden Path Reference

This repository contains both:

1. `platformctl` CLI implementation (Go).
2. A reference Golden Path layout (sanitized) for two GitOps repos.

## Why Two Repos

Golden Path flow is cross-repo by design:

- `k8s` repo: app namespace, Flux wiring, VaultAuth/VaultStaticSecret manifests.
- `vault-control-plane` repo: Vault policy + kubernetes auth role contract.

## Ordered Flow

```text
platformctl new-app --auto
  -> create branch/MR in vault-control-plane
  -> create branch/MR in k8s
  -> wait CI both
  -> merge vault-control-plane first
  -> verify Vault role/policy existence
  -> merge k8s
  -> Flux reconcile
  -> doctor checks
```

## Safety Rules

- No direct apply from CLI for app lifecycle.
- Vault contract merges before k8s secret consumers.
- Delete is two-phase:
  - phase-1 prune
  - phase-2 wiring cleanup
  - vault cleanup last

## Reference Artifacts

- `examples/golden-path/k8s-repo` — sanitized k8s repo fragment
- `examples/golden-path/vault-control-plane-repo` — sanitized Vault IaC fragment
- `examples/golden-path/ci` — pipeline rule snippets
