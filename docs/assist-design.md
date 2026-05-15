# `assist` — design of the RAG subsystem

> Status: design document. The implementation runs in the private production codebase.

`platformctl assist` is a retrieval-augmented-generation layer built into the CLI itself. It exists so that operators and AI agents can ask questions about the platform — "how do I onboard a new service?", "what does this Vault role mean?", "which runbook covers this failure mode?" — and get pointers into a curated corpus instead of grepping through markdown by hand.

This document describes the architecture in enough detail to communicate the design choices, without exposing implementation specifics that would tie the description to a particular cluster.

---

## Goals

1. **Retrieval should be measurable.** "Looks like it works" is not a release criterion; quality is gated in CI.
2. **The corpus is a contract, not a folder.** Every document in the corpus has structured metadata that downstream consumers can rely on.
3. **Lexical is the default; semantic is optional.** For platform bootstrap context, BM25 is cheap, deterministic, and audit-friendly. Hybrid retrieval is supported but treated as experimental.
4. **Same interface for humans and agents.** No separate API, no separate service — `assist` is a subcommand of the same CLI everyone else uses.
5. **The retrieval layer never executes anything.** Returned chunks are evidence pointers. The decision to run a command stays with the operator or the calling system.

---

## Non-goals

- This is not a chatbot. There is no built-in LLM call. Consumers of `assist` (an operator, a CI step, an external agent) decide how to use the retrieved chunks.
- This is not a vector database. The system orchestrates a corpus and provides a search interface; Qdrant, when used, is an optional backend.
- This is not the retrieval layer for end-user product search. A separate platform service (the "Retrieval API") covers that use case with a different stack, different SLOs and a different fusion strategy; it is out of scope for this document.

---

## Corpus as a contract

The corpus is the platform's runbook directory: structured markdown files, one per runbook, each opened by a YAML frontmatter block.

The frontmatter is **mandatory** and validated by JSON Schema (`runbook-frontmatter.schema.json`) before ingestion. The required fields are:

| Field | Purpose |
|---|---|
| `id` | Stable identifier; lookups via `assist runbook <id>` use this |
| `title` | Human-readable title |
| `domain` | Top-level area (network, security, data, observability, …) |
| `component` | Specific component within the domain |
| `lifecycle` | One of `draft`, `active`, `deprecated`, `archived` |
| `status` | Operational classification (`stable`, `experimental`, …) |
| `class` | Document class (`runbook`, `playbook`, `reference`, …) |
| `safety` | Safety classification, drives default visibility in agent retrieval |
| `retrieval_priority` | Numeric priority for tie-breaking in result ordering |
| `owners` | Maintainers (optional but encouraged) |
| `related` | Cross-references to other runbooks |
| `commands` | Executable snippets that auxiliary tooling can recognise |

A document without a valid frontmatter does not enter the index. This is enforced both in `assist validate-corpus` and in CI; corpus regressions therefore surface on the merge request, not on operations.

A `contentHash` of the chunk content is recorded so that upserts to any backend (Qdrant) are idempotent: re-ingesting an unchanged corpus is a no-op.

---

## Subcommands

| Subcommand | Purpose |
|---|---|
| `assist search --q "<topic>"` | Lexical BM25 search over the corpus; default operator entry point |
| `assist runbook <id>` | Fetch the full text of a runbook by its frontmatter id |
| `assist explain` | Generate an explanation of a target object (e.g. a manifest) with citations from the corpus |
| `assist diagnose [--profile <name>]` | Run a structured diagnostic with corpus context attached |
| `assist eval` | Run the golden-question evaluation harness, emit metrics |
| `assist validate-corpus` | Validate frontmatter and chunking across every document |
| `assist export-qdrant` | Produce a corpus dump in Qdrant-ingestible shape |
| `assist prepare-qdrant-upsert` | Batch the export into upsert payloads of the configured size |
| `assist upstream refresh` | Refresh chunks from an upstream documentation source (e.g. an external project's docs) into the same chunk format |

---

## Retrieval layers

### Default: lexical BM25

In a bootstrap context — answering "where do I look?" for an operator or agent — predictability matters more than maximum recall. BM25 is therefore the default:

- in-process tokeniser + inverted index built from the chunked corpus
- IDF scoring, no model dependency, no external service required
- latency well within agent loop budgets
- results are reproducible across runs and easy to audit

### Optional: hybrid via Qdrant

For corpora that span enough material to benefit from semantic recall (paraphrases, foreign-language ingestion, larger upstream document sets), `assist` can route queries through a hybrid backend:

- dense vectors via a TEI (text-embeddings-inference) sidecar, configurable model (typically a multilingual embedding such as `BAAI/bge-m3`)
- sparse vectors generated alongside dense
- fusion via Reciprocal Rank Fusion (or DBSF), chosen at config time
- optional cross-encoder rerank on the top-N candidates

This path is **read-plane only** and explicitly marked experimental in the platform's operator documentation: hybrid retrieval is used to find source material, not to drive command execution.

### Upstream corpus

Some questions require knowledge that does not live inside the platform itself — for example, how a specific external tool's API behaves. `assist upstream refresh` pulls chunked content from external documentation sources into a parallel index under `.platformctl-state/assist/upstream/`, scoped per product and per version. Queries can opt into upstream sources via flags.

---

## Evaluation as a CI gate

This is, in many ways, the **most important** part of the design. Retrieval is fragile: a single corrupted frontmatter or a malformed chunk can silently degrade recall. The harness exists to catch that on the merge request.

### Mechanism

- A `testdata/queries.yaml` file lists curated golden queries: question text and the expected target (path + section).
- `assist eval` runs every query against the configured retrieval path and computes:
  - **Recall@3** — fraction of queries where the expected target is in the top-3 results
  - **MRR@5** — mean reciprocal rank within the top-5
  - **p95 latency** across the query set
- The command exits non-zero when results fall below thresholds passed as flags (`--min-recall-at-3`, `--max-p95-latency-ms`).
- CI invokes `assist eval` on every MR that touches the corpus, schemas or retrieval code.

### Output

`assist eval --output minimal-json` emits per-query rank and pass/fail flags so that CI logs are diff-friendly and human reviewers can see which queries regressed.

### Why this matters

Without an eval gate, RAG becomes "looks plausible" software. With it, retrieval is held to the same engineering standards as any other CI-gated subsystem: a regression that ships is a regression that escaped the test, and the fix flows through the same merge-request pathway as a code bug.

---

## Token economy

Every `assist` subcommand supports `--output text | json | minimal-json`. The `minimal-json` mode was added explicitly for LLM agents:

- removes the API envelope (`apiVersion`, `kind`, `metadata.*`) that downstream consumers do not need
- drops nullable per-result fields when empty
- keeps only the fields agents actually consume: matched text, path, score, frontmatter essentials

In measured agentic workflows, switching from `json` to `minimal-json` cut per-call payload by 60–78%. For a workflow that calls `assist search` and `assist runbook` dozens of times in a session, that compounds into meaningful token savings.

---

## Boundaries with the broader RAG portfolio

This subsystem (`L1` in the broader portfolio framing) is one of three retrieval contexts the platform team treats as distinct:

| Layer | Audience | Retrieval | Status |
|---|---|---|---|
| `assist` (this document) | Operators, AI agents | Lexical default; experimental hybrid | Production, CI-gated |
| Retrieval API | Product applications | Hybrid + optional rerank | Foundation; awaits product consumers |
| AI-assisted observability | Third-line support | Vector store of knowledge; SQL-aggregated facts | Parallel domain |

Conflating them is a common mistake: each has different SLOs, corpora, and operational constraints. `assist` is deliberately the simplest of the three.
