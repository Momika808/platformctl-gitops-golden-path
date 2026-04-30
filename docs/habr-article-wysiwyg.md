# Как я сделал GitOps Golden Path CLI для Kubernetes: app lifecycle через MR/CI/Flux/Vault

Привет. В этой статье покажу практический кейс: как превратить onboarding/удаление приложения в Kubernetes из набора ручных runbook-шагов в один предсказуемый workflow через CLI.

Проект: `platformctl` (Go).

Идея простая: разработчик работает с понятными командами, а платформа остаётся в безопасной модели GitOps:

- изменения только через Git и Merge Request;
- CI валидирует;
- Flux применяет;
- Vault-контракт живёт в отдельном IaC-репозитории;
- никаких прямых `kubectl apply` из CLI для app lifecycle.

---

## Проблема

В реальном GitOps-кластере новый сервис часто требует ручных правок сразу в нескольких местах:

- namespace/layer manifests;
- Flux wiring (`Kustomization`);
- Vault role/policy;
- VaultAuth/VaultStaticSecret;
- проверки образа, health probes и ресурсов;
- отдельный delete-flow, который легко сломать.

Типичные последствия:

- `invalid role name` из Vault auth;
- image отсутствует в registry;
- расхождение generated-манифестов;
- хвосты после удаления (`Terminating`, finalizers, orphan resources).

---

## Цель

Сделать для платформы интерфейс уровня:

```bash
platformctl new-app ...
platformctl new-service ...
platformctl doctor ...
platformctl delete-app ...
```

Но при этом не ломать базовые принципы Platform Engineering:

- Git — source of truth;
- CI/MR — approval boundary;
- Flux — единственный механизм применения в кластер.

---

## Архитектура

```text
app spec / CLI input
        |
        v
   platformctl
        |
        +--> k8s repo MR
        |
        +--> vault-control-plane repo MR
        |
        v
    CI validation (both repos)
        |
        v
   merge vault first
        |
        v
   verify role/policy exists
        |
        v
   merge k8s
        |
        v
    Flux reconcile
        |
        v
     doctor checks
```

Ключевой момент: CLI не деплоит ресурсы напрямую, CLI генерирует и оркестрирует декларативные изменения.

---

## Что делает `platformctl`

Основные команды:

- `validate` — проверка схемы и контрактов app spec;
- `render` — генерация манифестов;
- `doctor` — preflight/external checks;
- `new-app` — onboarding приложения/namespace;
- `new-service` — onboarding сервиса;
- `delete-app` — безопасное двухфазное удаление;
- `infra kubelet-provider ...` — отдельный node-level контур;
- `export-public` — публичный sanitized export.

Пример workflow:

```bash
platformctl new-app --layer 13-game-engine --namespace game-engine --app game-engine --auto
platformctl new-service --layer 13-game-engine --namespace game-engine --name engine-api --image harbor.example.com/game/engine-api --tag main --port 8080
platformctl doctor --layer 13-game-engine
platformctl delete-app --layer 13-game-engine --namespace game-engine --auto --confirm game-engine
```

---

## Почему Vault repo отдельно

Это принципиально:

- k8s-репозиторий описывает потребителей секретов (`VaultAuth`, `VaultStaticSecret`);
- `vault-control-plane` описывает доступ (`role` + `policy`).

Из этого следует жёсткий порядок merge:

1. merge `vault-control-plane`;
2. подтвердить, что role/policy уже есть в Vault;
3. merge k8s repo;
4. ждать reconcile в Flux.

Именно этот порядок убирает классический фейл `invalid role name`.

---

## Самое важное: двухфазное удаление

Удаление сделано намеренно не «одним rm -rf».

### Phase 1: prune

- удаляем app resources из слоя;
- оставляем Flux Kustomization;
- ждём prune/termination.

### Phase 2: cleanup wiring

- удаляем layer dir;
- удаляем `ks-<layer>.yaml` и ссылки в kustomization.

### Phase 3: Vault cleanup

- удаляем role/policy контракт только после удаления k8s-consumers.

Safety gates:

- ownership labels обязательны;
- protected namespaces deny-list;
- PVC/data guard без явного confirm;
- force finalizers не запускается автоматически.

---

## Что дало ускорение

Бутылочное горлышко было не в Go-коде CLI, а в CI topology:

- очередь runner;
- лишние jobs;
- ненужные node-level триггеры;
- merge conflicts на длинных авто-флоу.

Что реально помогло:

- выделенный `fast-ci` runner для validate;
- `rules:changes` для отсечения лишних jobs;
- DAG через `needs` для параллельных validate;
- auto-rebase при conflict;
- структурированное логирование авто-флоу.

Пример измерения из тестового цикла удаления:

- полный авто-flow delete (k8s phase1 + phase2 + vault cleanup): ~12m29s.

---

## Ошибки, которые пришлось пройти

Коротко о реальных провалах, которые улучшили дизайн:

1. Vault role отсутствует в момент запуска k8s consumers.
2. Образ отсутствует в registry в момент reconcile.
3. Очередь runner и «лишние» pipeline paths съедают большую часть времени.
4. Одношаговое удаление оставляет хвосты и плохо отлаживается.

Итог: правильный процесс важнее «магической» команды.

---

## Что в публичном репозитории

Публичный репозиторий сделан как reference implementation:

- Go CLI ядро;
- docs;
- golden-path примеры для двух репозиториев;
- без реальных секретов, приватных IP и production adapters.

Ссылка:

[https://github.com/Momika808/platformctl-gitops-golden-path](https://github.com/Momika808/platformctl-gitops-golden-path)

---

## Вывод

`platformctl` не заменяет GitOps.

Он делает правильный GitOps-путь простым и воспроизводимым:

- меньше ручных runbook-операций;
- меньше межрепозиторного drift;
- понятный lifecycle приложения от создания до удаления;
- понятная граница ответственности между CLI, CI и Flux.

Если в одной фразе: меньше «kubectl руками», больше декларативного platform workflow.
