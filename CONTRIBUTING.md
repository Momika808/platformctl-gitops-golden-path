# Contributing

Thanks for your interest in `platformctl`.

## Development Workflow

1. Create a feature branch.
2. Keep changes scoped and testable.
3. Run formatting and tests locally.
4. Open a PR with a clear change summary.

## Local Checks

```bash
gofmt -w ./cmd ./internal
go test ./...
```

Optional security check:

```bash
gitleaks detect --source . --no-git --redact
```

## Design Principles

- GitOps-first: no direct apply path for app lifecycle.
- Keep safety checks explicit (ownership labels, protected namespaces, staged delete).
- Prefer deterministic render output and test fixtures.

## Commit Style

Use concise, imperative commit messages:

- `feat: add ...`
- `fix: handle ...`
- `docs: clarify ...`
- `test: cover ...`
