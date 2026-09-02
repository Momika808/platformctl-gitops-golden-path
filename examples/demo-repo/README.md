# Demo repository pair

Two sanitized repositories that satisfy every open command of `platformctl`:

- `k8s/`: one layer (`11-demo`) with one application (`apps/demo`), Flux wiring,
  the gateway certificate, and the registry CA template.
- `vault-control-plane/`: the role and policy contract for that layer.

`scripts/demo.sh` copies this pair into a temporary directory, initialises git
in both, and runs validate, render, doctor, new-app, doctor on the new layer,
and delete-app. Nothing here talks to a cluster, a registry, or GitLab.

`apps/demo/generated/` is committed on purpose: `doctor` treats a difference
between committed and freshly rendered manifests as drift.
