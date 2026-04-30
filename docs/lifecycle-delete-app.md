# delete-app Lifecycle (Two-Phase)

## Why Two Phases

Deleting app layer and platform wiring in one step is risky:

- resources can be left in `Terminating`
- finalizers may block namespace deletion
- Vault cleanup can happen before consumers are gone

Two phases make the process predictable.

## Phase 1: k8s Prune

- remove app resources from layer
- keep Flux Kustomization present
- wait for prune/termination progress

## Phase 2: k8s Cleanup

- remove layer directory
- remove Flux ks wiring for the layer

## Vault Cleanup

Run only after k8s consumers are removed:

- delete Vault role descriptor
- delete Vault policy file/contract entries

## Safety Gates

- ownership labels required
- protected namespaces deny-list
- PVC/data guard unless explicit confirm flags
- no automatic finalizer force removal
