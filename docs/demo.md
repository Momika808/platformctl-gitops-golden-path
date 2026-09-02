# Demo

One command, no cluster, no registry, no GitLab:

```bash
scripts/demo.sh
```

Needs `go`, `git` and `kubectl` (only for `kubectl kustomize` inside `doctor`).
The script builds the CLI, copies `examples/demo-repo` into a temporary
directory, initialises git in both repositories and runs:

| Step | Command | What it proves |
|---|---|---|
| 1 | `validate --all` | ServiceApp specs pass the schema |
| 2 | `render --all` | one spec becomes HelmRelease, values ConfigMap, image automation; output is deterministic |
| 3 | `doctor --all` | layer wiring, namespace labels, Vault role and policy, gateway cert SAN, `kubectl kustomize` of the layer |
| 4 | `new-app` | a new layer is scaffolded and registered in the Flux kustomizations; nothing is applied |
| 5 | `doctor --layer` | the scaffold builds with kustomize |
| 6 | `delete-app` | the two-phase delete plan for an existing layer, with ownership checks |

The same flags in CI: `.github/workflows/ci.yml`, job `test`, step `Run demo`.

What the demo does not show: merge request creation, CI waiting and merge
ordering (`--auto`), Harbor image checks and cluster capacity checks. Those
need a GitLab, a registry and a cluster.
