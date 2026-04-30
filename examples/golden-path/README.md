# Golden Path Example (Sanitized)

This directory shows the *resulting declarative changes* that `platformctl` would generate/orchestrate.

## app

- app: `game-engine`
- namespace: `game-engine`
- layer: `13-game-engine`
- vault role: `vso-game-engine`

## k8s repo fragment

`k8s-repo/` includes:

- namespace and service account
- VaultAuth and VaultStaticSecret
- Flux Kustomization file for the layer

## vault-control-plane fragment

`vault-control-plane-repo/` includes:

- role descriptor in `roles.d/`
- policy file in `policies/`

## merge order

1. Merge vault-control-plane MR.
2. Verify Vault role/policy exists.
3. Merge k8s MR.
4. Wait Flux reconcile and run doctor.
