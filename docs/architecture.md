# Architecture

## Goal

Provide a developer-facing CLI for Kubernetes GitOps that simplifies onboarding/deletion without bypassing Git/MR/CI/Flux controls.

## Control Flow

```text
app spec / CLI input
        |
        v
   platformctl
        |
        +--> k8s repo branch/MR
        |
        +--> vault-control-plane branch/MR
        |
        v
      CI validation (both repos)
        |
        v
  ordered merge (vault -> k8s)
        |
        v
          Flux reconcile
        |
        v
          doctor checks
```

## Main Principles

1. Git is desired state; cluster is actual state.
2. CLI generates declarative changes, not imperative deploys.
3. Vault contract must be ready before k8s secret consumers.
4. Deletion is two-phase for safety and observability.
5. Infra/node rollouts are explicit and separated from app onboarding.

## Key Components

- `new-app`: scaffold namespace/layer + optional orchestration
- `new-service`: scaffold service manifests and config
- `doctor`: contract validation + preflight checks
- `delete-app`: prune-first, cleanup-second strategy
- `infra kubelet-provider`: GitLab pipeline bridge for node-level changes
