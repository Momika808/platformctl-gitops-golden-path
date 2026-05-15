# platformctl — GitOps Golden Path

> **Status:** public design showcase / alpha. This repository demonstrates the architectural ideas of an internal platform CLI that runs in a production cluster. It is **not** a finished open-source product — the full implementation, including the RAG assist subsystem, deployment orchestration, and observability subcommands, lives in a private codebase. This repo is intended as a portfolio artifact for hiring conversations and as a reference for similar platform efforts.

`platformctl` is a Go CLI that automates the lifecycle of applications and services on Kubernetes through a strict GitOps pipeline. The principle is simple: **Git + Merge Request + CI + Flux is the only path to the cluster.** The CLI never `kubectl apply`s anything itself — it generates manifests, validates them against schemas, opens MRs in the relevant repositories, and lets Flux reconcile the desired state.

This README describes the **full design**, not just the subset of code currently in this repository. Each command is labelled:

- `[open]` — implementation present in this repository
- `[private]` — implemented and running in the production platform, design described here but code not yet open-sourced

---

## Why this exists

Platform teams hit a recurring problem: as a cluster grows, onboarding a new application requires manual edits in two or three different repositories (manifests, Vault policies, CI variables), running ad-hoc validation, and remembering an ever-changing checklist. Each team reinvents its own way of doing this, and mistakes pile up.

`platformctl` collapses that into a single Go binary with a few opinionated guarantees:

1. **Read-plane / write-plane separation.** Read commands (`validate`, `render`, `doctor`, `assist`, `metrics`, …) are idempotent and safe — agents and humans can call them freely. Write commands (`new-app`, `delete-app`, `secrets sync-gitlab-ci`, …) are dry-run by default and require explicit `--apply --confirm` to mutate anything.
2. **No direct cluster mutation.** Writes go through Git: the command opens a Merge Request in the appropriate repository, CI validates it, and Flux applies it. The CLI is a manifest factory, not a kubectl wrapper.
3. **Schema-first.** Every spec the CLI handles (`appspec`, `service-app`, `product-deployment`, `runbook-frontmatter`, retrieval-api request/response, …) has a JSON Schema. CI rejects invalid specs before they reach the cluster.
4. **Agent-friendly output.** Every command supports `--output text | json | minimal-json`. The `minimal-json` mode strips API envelopes and non-essential fields, reducing tokens consumed by LLM agents by 30–50%.
5. **RAG as a first-class subsystem.** A built-in `assist` subcommand provides a retrieval layer over a corpus of runbooks: schema-aware ingestion, lexical search as the stable default, golden-question evaluation as a CI gate. Operators and AI agents share the same retrieval interface.

---

## Command map

### Read-plane (idempotent)

| Command | Status | Purpose |
|---|---|---|
| `validate [--all]` | `[open]` | Validate `appspec` / service specs against JSON Schemas |
| `render [--all]` | `[open]` | Render generated manifests from canonical sources |
| `doctor [--all\|<check>]` | `[open]` | Health checks: CNI, kube-context, registry CA/auth, policy-reports, lifecycle |
| `config <view/use-context/init/validate>` | `[private]` | Multi-context runtime configuration |
| `assist <search/runbook/explain/diagnose/eval/validate-corpus/export-qdrant/prepare-qdrant-upsert/upstream-refresh>` | `[private]` | RAG subsystem over the runbook corpus (see `docs/assist-design.md`) |
| `docs suggest` | `[private]` | Suggests which docs/runbooks are impacted by a change |
| `hubble <status/observe/why-dropped>` | `[private]` | Network observability via Cilium Hubble |
| `logs <status/query>` | `[private]` | Loki queries from the CLI |
| `metrics <status/query/app>` | `[private]` | PromQL queries with app-aware aggregation |
| `upgrade plan` | `[private]` | Renovate-driven upgrade plan parsing |
| `deploy <init/validate/scaffold/promote/status/ci-generate>` | `[private]` | Product deployment workflow |
| `observe collect` | `[private]` | Local evidence bundle on incidents |
| `export-public` | `[open]` | Helper for mirroring a curated subset to a public repo (this one) |

### Write-plane (gated, dry-run by default)

| Command | Status | Safety model |
|---|---|---|
| `new-app <name>` | `[open]` | dry-run default; `--auto` runs full flow |
| `new-service <name>` | `[open]` | dry-run default |
| `new-product <name> --gitlab-path <path>` | `[private]` | Phase-4 autonomous product onboarding (opens Vault MR + product MR) |
| `delete-app <ns>/<app>` | `[open]` | `--auto-merge` only under explicit GO |
| `secrets sync-gitlab-ci` | `[private]` | Rotates GitLab CI variables from Vault; dry-run default |
| `registry-ca sync` | `[private]` | Syncs registry CA secret across the cluster |
| `runners <list/reconcile/rotate/revoke>` | `[private]` | GitLab CI runner lifecycle |
| `harbor-robot create` | `[private]` | Harbor registry robot account creation |
| `infra kubelet-provider` | `[private]` | Talos infrastructure operations |
| `bootstrap` | `[private]` | Cluster bootstrap; break-glass only |

---

## What this repo currently demonstrates

- **Application specification model** (`internal/appspec`) — the data structure and validation rules that anchor everything else.
- **The `new-app` flow** (`cmd/platformctl`) — how an onboarding command composes specs, renders manifests, and produces output suitable for a CI-driven MR.
- **GitOps boundary** — the deliberate choice that `platformctl` never applies anything itself, only proposes changes for Flux to reconcile.
- **Read/write plane separation** — even at this early stage, command shape reflects the operational discipline.
- **Examples** (`examples/golden-path/`) — the canonical layout of a service in the platform repository.

---

## What is described but not yet open-sourced

The repository deliberately does not contain the following parts of the production implementation:

- The `assist` RAG subsystem (lexical retriever, Qdrant exporter, golden-question evaluator, schema-aware corpus validator). See `docs/assist-design.md` for the architecture.
- Deployment orchestration (`deploy` family) — scaffolding, promotion, status tracking, child CI pipeline generation.
- Operations subcommands (`hubble`, `logs`, `metrics`, `observe`) — these wrap internal endpoints and would require non-trivial decoupling.
- Secrets and registry plumbing (`secrets`, `registry-ca`, `harbor-robot`) — tightly coupled to specific internal infrastructure.

These will move to the public repository incrementally; see `docs/roadmap.md`.

---

## Design documents

- [`docs/architecture.md`](docs/architecture.md) — read/write plane, manifest factory pattern, GitOps boundary
- [`docs/assist-design.md`](docs/assist-design.md) — RAG subsystem: lexical-as-default, schema-aware corpus contracts, CI-gated retrieval evaluation
- [`docs/roadmap.md`](docs/roadmap.md) — what gets published, when, and what stays private
- [`docs/demo.md`](docs/demo.md) — walk-through of the `new-app` flow

---

## Non-goals

- This CLI is **not** a `kubectl` replacement. It never talks to the cluster API for mutating operations.
- It is **not** a Flux replacement. Flux remains the only reconciler.
- The public repository is **not** a turnkey installable platform. It is a design reference.

---

## License

Apache-2.0. See [`LICENSE`](LICENSE).

## Security

See [`SECURITY.md`](SECURITY.md) for the responsible disclosure policy.
