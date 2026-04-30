# Demo

## Quick Local Demo

```bash
go build -o bin/platformctl ./cmd/platformctl

./bin/platformctl validate --all --repo-root /path/to/k8s-repo
./bin/platformctl render --all --repo-root /path/to/k8s-repo
./bin/platformctl doctor --layer 13-game-engine --repo-root /path/to/k8s-repo

./bin/platformctl new-app \
  --layer 13-game-engine \
  --namespace game-engine \
  --app game-engine \
  --auto

./bin/platformctl delete-app \
  --layer 13-game-engine \
  --namespace game-engine \
  --auto \
  --confirm game-engine
```

## Expected UX

- deterministic scaffold output
- explicit CI wait states
- safe merge ordering for Vault and k8s repos
- two-phase delete lifecycle
