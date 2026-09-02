#!/usr/bin/env bash
# End-to-end demo of every open command on the sanitized repository pair in
# examples/demo-repo. Runs in a temporary copy: the working tree is untouched.
# Needs: go, git, kubectl (for `kubectl kustomize` inside doctor). No cluster.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for tool in go git kubectl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing tool: $tool" >&2; exit 1; }
done

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BIN="$WORK/platformctl"
echo "== build"
go build -o "$BIN" "$ROOT/cmd/platformctl"

cp -R "$ROOT/examples/demo-repo/." "$WORK/repos/"
K8S="$WORK/repos/k8s"
VAULT="$WORK/repos/vault-control-plane"
for repo in "$K8S" "$VAULT"; do
  git -C "$repo" init -q
  git -C "$repo" -c user.name=demo -c user.email=demo@example.invalid add -A
  git -C "$repo" -c user.name=demo -c user.email=demo@example.invalid commit -q -m "demo baseline"
done

step() { printf '\n== %s\n' "$1"; }

step "validate: ServiceApp specs against the schema"
"$BIN" validate --all --repo-root "$K8S"

step "render: canonical spec -> generated manifests (deterministic)"
"$BIN" render --all --repo-root "$K8S"

step "doctor: layer wiring, Vault contract, cert SAN, kustomize build (no cluster, no registry)"
"$BIN" doctor --all --repo-root "$K8S" --skip-harbor-image-check --skip-capacity-check

step "new-app: scaffold a new layer locally (no MR, no apply)"
"$BIN" new-app --layer 12-newapp --namespace newapp \
  --vault-secret-path harbor/robots/newapp-pull \
  --harbor-ca-template clusters/homelab/11-demo/harbor-oci-ca.yaml \
  --repo-root "$K8S"

step "doctor on the scaffolded layer"
"$BIN" doctor --layer 12-newapp --repo-root "$K8S" --skip-harbor-image-check --skip-capacity-check

step "what new-app changed in the k8s repository"
git -C "$K8S" status --short

step "delete-app: two-phase plan for the existing layer (plan only, no MR)"
"$BIN" delete-app --layer 11-demo --namespace demo --skip-runtime-checks --repo-root "$K8S"

printf '\nDemo finished: every step exited 0.\n'
