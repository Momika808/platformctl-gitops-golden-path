# Черновик статьи для Хабра

## Заголовок

Как я сделал GitOps Golden Path CLI для Kubernetes: app lifecycle через MR/CI/Flux/Vault

## Проблема

В GitOps-кластере создание нового приложения часто превращается в ручной квест по нескольким репозиториям:

- namespace/layer manifests
- Flux wiring
- Vault role/policy
- image/probe/resource checks
- cleanup/delete flow

Это даёт drift и повторяющиеся инциденты: missing role, image not found, broken dependencies.

## Идея

Сделать CLI (`platformctl`), который:

1. генерирует декларативные изменения;
2. создаёт MR в нужные репозитории;
3. ждёт CI;
4. мержит в правильном порядке;
5. оставляет Flux единственным механизмом применения.

## Архитектура

`platformctl` связывает:

- k8s config repo
- vault-control-plane repo
- GitLab MR/CI
- Flux reconcile
- doctor checks

Ключевой принцип: no direct apply.

## Команды

```bash
platformctl new-app ... --auto
platformctl new-service ...
platformctl doctor --layer ...
platformctl delete-app ... --auto
```

## Удаление (самое важное)

`delete-app` сделан как двухфазный процесс:

- phase-1: prune ресурсов
- phase-2: cleanup слоя и wiring
- затем cleanup Vault контракта

Это снижает риск orphan-ресурсов и поломки секретного контура.

## Метрики и эффект

Что сравниваем:

- ручные шаги до/после
- среднее время lifecycle
- количество фейлов по типам
- доля успешных прогонов без ручного вмешательства

## Ошибки, которые пришлось пройти

- неправильный порядок merge между Vault и k8s
- очереди runner и нецелевые jobs
- конфликтные MR при длинных auto-flow

## Итог

CLI не заменяет GitOps, а упрощает его использование:

- разработчику проще и быстрее
- платформе безопаснее и предсказуемее
- операционно — меньше ручных runbook'ов
