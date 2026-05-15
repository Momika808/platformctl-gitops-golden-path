# Architecture

> Status: design document. Describes the full production CLI; not all subsystems below are present in this public repository.

## Guiding principles

1. **GitOps boundary.** `platformctl` never mutates the cluster directly. All state changes flow through Git → Merge Request → CI → Flux. The CLI is a manifest factory, not a kubectl wrapper.
2. **Read-plane / write-plane separation.** Read commands are idempotent and free of side effects. Write commands are dry-run by default; mutation requires explicit `--apply --confirm` (or `--auto` in scripted contexts where the operator has already made the decision).
3. **Schema-first.** Every artifact the CLI produces or consumes is governed by a JSON Schema. Validation happens at three points: in the CLI itself, in CI, and in the receiving repository.
4. **Agent-friendly output.** Every read command supports three output modes (`text`, `json`, `minimal-json`) so that humans, scripts, and LLM agents can each consume the same data efficiently.
5. **RAG as a subsystem, not a service.** The retrieval layer for runbooks is a set of subcommands on the same CLI, not a separate microservice. Operators and AI agents use the same interface.

---

## High-level shape

```
                    ┌─────────────────────────────────────────────────────┐
                    │                  platformctl (Go CLI)               │
                    │                                                     │
                    │  ┌──────────────┐         ┌───────────────────┐    │
                    │  │  Read-plane  │         │   Write-plane     │    │
                    │  │              │         │ (dry-run default) │    │
                    │  │ validate     │         │                   │    │
                    │  │ render       │         │ new-app           │    │
                    │  │ doctor       │         │ new-service       │    │
                    │  │ config       │         │ new-product       │    │
                    │  │ assist *     │         │ delete-app        │    │
                    │  │ docs         │         │ secrets *         │    │
                    │  │ hubble       │         │ registry-ca *     │    │
                    │  │ logs         │         │ runners *         │    │
                    │  │ metrics      │         │ infra *           │    │
                    │  │ upgrade      │         │ harbor-robot      │    │
                    │  │ deploy:read  │         │ deploy:write *    │    │
                    │  │ observe      │         │ bootstrap         │    │
                    │  │ export-public│         │                   │    │
                    │  └──────┬───────┘         └─────────┬─────────┘    │
                    │         │                           │              │
                    │         │  outputformat (text /     │              │
                    │         │   json / minimal-json)    │              │
                    │         └─────────────┬─────────────┘              │
                    │                       │                            │
                    └───────────────────────┼────────────────────────────┘
                                            │
                  ┌─────────────────────────┼──────────────────────────┐
                  │                         ▼                          │
                  │              JSON Schema validation                │
                  │   (appspec, service-app, product-deployment,       │
                  │    runbook-frontmatter, retrieval-api contracts,   │
                  │    assist outputs, probe outputs, cross-repo)      │
                  └─────────────────────────┬──────────────────────────┘
                                            │
                  ┌─────────────────────────┼──────────────────────────┐
                  │                         ▼                          │
                  │           GitLab API: opens MRs in:                │
                  │  - cluster/k8s (manifests, Flux Kustomizations)    │
                  │  - cluster/vault-control-plane (Vault policies)    │
                  │  - other repositories on demand                    │
                  └─────────────────────────┬──────────────────────────┘
                                            │
                                            ▼
                  ┌────────────────────────────────────────────────────┐
                  │                      CI                            │
                  │   - lint, schema validation, build, push           │
                  │   - assist eval (Recall@3, MRR@5, p95)             │
                  │   - doctor checks against staging                  │
                  └─────────────────────────┬──────────────────────────┘
                                            │
                                            ▼  (merge)
                  ┌────────────────────────────────────────────────────┐
                  │                  Flux on cluster                   │
                  │             reconciles desired state               │
                  └────────────────────────────────────────────────────┘
```

`*` indicates subsystems whose implementation currently lives in the private codebase.

---

## Read-plane

The read-plane exists to **make the cluster legible** to humans, scripts and agents without granting them mutation rights. Every read command:

- is idempotent
- has bounded latency (no long-running cluster operations)
- supports three output formats
- never mutates anything, including local filesystem state, except for cache directories under `.platformctl-state/`

The most consequential read subcommand is `assist`, described in detail in [`assist-design.md`](assist-design.md). It exposes a retrieval layer over the runbook corpus, with a stable lexical default and an optional experimental hybrid path for finding source material.

---

## Write-plane

The write-plane is where the discipline matters. Every write command:

- **Defaults to dry-run.** Running it without `--apply --confirm` (or `--auto`) prints the proposed diff and exits.
- **Never mutates the cluster.** Writes are proposed as Git changes via Merge Requests.
- **Validates first.** Schema validation runs before anything goes to GitLab.
- **Is auditable.** Every write command has a stable identifier and emits structured logs for correlation in observability.

This design is intentional: the cost of a mistake on a write command is recoverable (close the MR, fix the spec, reopen) precisely because the mutation never reached the cluster directly.

---

## Manifest factory pattern

Onboarding a new service is the canonical write flow:

1. Operator runs `platformctl new-app <name>`.
2. The CLI loads templates (embedded), composes a service `appspec`, validates against `appspec.schema.json`.
3. It renders the resulting manifests (Deployment, Service, ServiceMonitor, NetworkPolicy, etc.) into the platform's standard layout under `examples/golden-path/`.
4. It computes diffs against the current state of the `cluster/k8s` repository.
5. By default it prints the diff. With `--apply --confirm` it opens an MR via the GitLab API.
6. CI on that MR re-runs `validate` + `doctor` + targeted tests. Reviewers merge.
7. Flux on the cluster reconciles.

The CLI is therefore a **factory of well-formed Git proposals**, not an actor on the cluster.

---

## Agent-friendly output

Every read command accepts `--output text | json | minimal-json`:

- `text` — the default; human-readable, ~50–1400 tokens per typical command.
- `json` — full JSON with API envelope and metadata; on average ~1.91x the size of text.
- `minimal-json` — strips the envelope and non-essential fields; ~30–50% of the full JSON size.

The `minimal-json` mode was added after measuring that agentic workflows (Claude, Codex) routinely call CLI subcommands dozens of times per task. Cutting per-call payloads by 60–78% measurably reduces token spend without losing the structure agents actually use.

---

## RAG subsystem (summary)

See [`assist-design.md`](assist-design.md) for the full design. In short:

- The corpus is the platform's runbook directory, each runbook tagged with a structured YAML frontmatter (`id, title, domain, component, lifecycle, status, class, safety, retrieval_priority`).
- The default retrieval path is **lexical BM25**, deliberately chosen for stability and auditability in bootstrap contexts.
- An **experimental hybrid path** exists for finding source material across larger corpora; results are treated as evidence pointers, not as instructions.
- A **golden-question evaluation harness** runs in CI: 37 curated queries with expected target paths/sections; `--min-recall-at-3` and `--max-p95-latency-ms` flags fail the pipeline on regression.

---

## What is intentionally not in this repository

- Vendor-specific clients (Qdrant SDK calls, Loki/Prometheus query construction) are described but not exposed, because they leak internal endpoints and would require config placeholders that obscure the design.
- The CI templates that bind the CLI to the platform's GitLab and Harbor instances are described in `roadmap.md` but not committed here.
- Internal hostnames, IP ranges and product names have been deliberately omitted from this repository.

The goal of this repo is to communicate **the architecture**, not to be a turnkey installation.
