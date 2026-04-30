# platformctl

GitOps Golden Path CLI for Kubernetes.

`platformctl` automates app lifecycle across GitOps repositories while keeping Git + MR + CI + Flux as the only apply path.

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

## Safety Model

- No direct `kubectl apply` path in app lifecycle commands
- Git/MR/CI remain approval boundary
- Two-phase delete flow for k8s, then Vault cleanup
- Ownership label checks before destructive actions

## Quick Start

```bash
go build -o bin/platformctl ./cmd/platformctl

./bin/platformctl validate --help
./bin/platformctl render --help
./bin/platformctl doctor --help
./bin/platformctl new-app --help
./bin/platformctl delete-app --help
```

## Example Workflow

```bash
platformctl new-app --layer 13-game-engine --namespace game-engine --app game-engine --auto
platformctl new-service --layer 13-game-engine --namespace game-engine --name engine-api --image harbor.example.com/game/engine-api --tag main --port 8080
platformctl doctor --layer 13-game-engine
platformctl delete-app --layer 13-game-engine --namespace game-engine --auto --confirm game-engine
```

## Repository Structure

- `cmd/platformctl` — CLI commands and orchestration
- `internal/appspec` — app spec model + validation
- `testdata` — golden render fixtures
- `docs` — architecture, lifecycle, and article draft
- `examples` — sanitized usage examples

## License

Apache-2.0
