# Roadmap

> Status: living document. Describes the relationship between this public repository and the production codebase, and what is planned to move between them.

The production version of `platformctl` lives in a private platform repository. This public repository is a **design showcase**: it communicates the architecture and houses a curated subset of the code. The two are intentionally kept in sync at the design level, even when the implementation diverges.

This document exists so that anyone reading the public repo — a hiring manager, a curious engineer, a contributor — knows what is here today, what is coming, and what will stay private.

---

## Currently in the public repo (v0.x alpha)

- Application specification model (`internal/appspec`) — the shared data structure that anchors every onboarding flow.
- `new-app` / `new-service` / `delete-app` flows — the simplest write-plane commands, with dry-run defaults.
- `validate` / `render` / `doctor` skeletons — enough to demonstrate the read-plane discipline.
- `infra` (skeleton) — placeholder for the kubelet-provider operations.
- `export-public` — the command used to mirror parts of the internal repository into this public one.
- Examples under `examples/golden-path/`.
- CI workflows under `.github/workflows/`.
- Apache-2.0 license, `SECURITY.md`.
- Design documentation under `docs/`: this file, `architecture.md`, `assist-design.md`, `demo.md`.

---

## Planned for the public repo (anonymised, no internal endpoints)

The following items exist in the private codebase and will be ported to this repository once they can be cleanly decoupled from internal infrastructure. The order below reflects the rough priority for portfolio value, not a delivery commitment.

### Tier 1 — high-signal design surfaces

- **`outputformat` package** — the implementation of `text` / `json` / `minimal-json` flag handling, with example wiring on a couple of commands. This is the cheapest piece to publish and demonstrates the token-economy concern.
- **`assist` skeleton with lexical retriever** — corpus chunker, frontmatter extractor, in-process BM25 retriever, JSON Schema for outputs. Without Qdrant integration, without upstream-corpus, with a sample runbook corpus.
- **`assist eval` harness** — golden-question dataset format, evaluator, CLI flags for thresholds. With a small example corpus and ten or so golden queries; consumers can plug in their own.
- **JSON Schemas** — `appspec`, `service-app`, `runbook-frontmatter`, `assist-*` outputs. Schemas are reusable as-is in other projects.

### Tier 2 — architectural completeness

- **Hybrid retrieval interface** — the contract between `assist` and an external retrieval API (request/response JSON Schemas, the Go interface). Implementations remain pluggable.
- **`docs suggest`** — change-impact analysis for documentation. Useful as a CI step in any docs-heavy repo.
- **`config` subcommand** — the multi-context configuration model.
- **`deploy validate` / `deploy ci-generate`** — the deploy spec validator and the child-pipeline generator, generalised so they do not depend on a specific GitLab instance.

### Tier 3 — substantial cross-cutting work

- **`hubble` / `logs` / `metrics` / `observe`** — these wrap internal Cilium / Loki / Prometheus endpoints and would need both decoupling and example configurations to be useful publicly.
- **`upgrade plan`** — currently tied to a specific Renovate dashboard format; a generic version is worth doing but requires more abstraction.
- **`new-product`** — the most complex onboarding flow; touches multiple repositories and would need a public reference layout to be meaningful.

---

## Will stay private

These pieces are tightly coupled to a specific internal infrastructure and would either (a) require so much abstraction that the public version stops being useful, or (b) leak sensitive operational details.

- **`secrets sync-gitlab-ci`** — Vault path conventions, GitLab project IDs.
- **`registry-ca sync`** — CA secret name conventions and trust-store layout specific to the internal Harbor.
- **`harbor-robot create`** — robot account naming conventions.
- **`runners` lifecycle** — runner registration tokens, naming conventions.
- **`bootstrap`** — cluster bootstrap; entirely environment-specific.
- **The internal corpus of runbooks** — these reference internal products, hostnames, and infrastructure. The schema is public; the content is not.

The design of each of these is documented (or will be) in `architecture.md` or in dedicated design docs.

---

## How to read this repository for a hiring conversation

1. Start with the [README](../README.md) for the full design context and the command map. The `[open]` / `[private]` labels are the truth: do not assume a command labelled `[private]` is present in this tree.
2. Read [`architecture.md`](architecture.md) for the read/write plane discipline and the manifest factory pattern. This is the core idea.
3. Read [`assist-design.md`](assist-design.md) for the RAG subsystem — corpus contracts, lexical-as-default, the CI-gated evaluation harness. This is the most distinctive subsystem.
4. Skim the existing `cmd/platformctl` and `internal/appspec` code to see how the public skeleton lays out the read/write plane.
5. Cross-reference against the operator's own experience: which parts of this design do they recognise, which would they push back on, which would they extend.

The intent is that an interviewer can spend twenty minutes here and leave with a clear, falsifiable picture of how the platform team operates and what the engineering judgement is — independent of whatever production code lives behind the labels.
