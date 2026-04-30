# platformctl

[![CI](https://github.com/Momika808/platformctl-gitops-golden-path/actions/workflows/ci.yml/badge.svg)](https://github.com/Momika808/platformctl-gitops-golden-path/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8)](./go.mod)

GitOps Golden Path CLI for Kubernetes.

`platformctl` automates app lifecycle across GitOps repositories while keeping Git + MR + CI + Flux as the only apply path.

## What Is Included In This Repo

1. **Go CLI** (`cmd/platformctl`, `internal/appspec`)
2. **Golden Path reference** (`examples/golden-path/*`)
3. **Architecture + lifecycle docs** (`docs/*`)

If you asked "where is golden path?" — it is in `examples/golden-path` and `docs/golden-path-reference.md`.

## What It Solves

Without a platform CLI, onboarding a service usually means manual edits across multiple repos:

- Namespace and app layer manifests
- Flux `Kustomization` wiring
- Vault role/policy contract
- Image + probe + resource checks
- Deletion cleanup across k8s and Vault

`platformctl` turns this into repeatable commands with validation and safe ordering.

## Core Commands

- `validate` — schema checks for app specs
- `render` — generate manifests
- `doctor` — static and external checks
- `new-app` — scaffold new application layer
- `new-service` — scaffold service in existing app
- `delete-app` — safe two-phase deletion
- `infra kubelet-provider ...` — node-level operation trigger/status/logs
- `export-public` — sanitize/export public edition

## Golden Path (Cross-Repo)

- `k8s` repo receives Namespace/Flux/VaultAuth/VaultStaticSecret manifests
- `vault-control-plane` repo receives Vault role/policy descriptors
- merge order: **vault first**, then **k8s**, then Flux reconcile

Details: `docs/golden-path-reference.md`.

## Quick Start

```bash
go build -o bin/platformctl ./cmd/platformctl
./bin/platformctl --help
```

## Example Workflow

```bash
platformctl new-app --layer 13-game-engine --namespace game-engine --app game-engine --auto
platformctl new-service --layer 13-game-engine --namespace game-engine --name engine-api --image harbor.example.com/game/engine-api --tag main --port 8080
platformctl doctor --layer 13-game-engine
platformctl delete-app --layer 13-game-engine --namespace game-engine --auto --confirm game-engine
```

## Demo

- quick commands: `docs/demo.md`
- local check script: `scripts/run-local-checks.sh`

## Non-goals

- `platformctl` does **not** apply resources directly to cluster as deployment mechanism.
- `platformctl` does **not** replace Flux.
- `platformctl` does **not** store runtime secret payloads.
- Public repository does **not** include private production adapters.

## Repository Structure

- `cmd/platformctl` — CLI commands and orchestration
- `internal/appspec` — app spec model + validation
- `testdata` — golden render fixtures
- `docs` — architecture, lifecycle, article draft
- `examples/golden-path` — sanitized cross-repo Golden Path reference

## License

Apache-2.0
