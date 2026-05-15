# platformctl — GitOps Golden Path

> **Статус:** публичный design-showcase / альфа. Этот репозиторий демонстрирует архитектурные идеи внутреннего платформенного CLI, который работает в production-кластере. Это **не** готовый open-source продукт — полная реализация, включая RAG-подсистему `assist`, оркестрацию деплоев и observability-подкоманды, живёт в приватной кодобазе. Этот репозиторий — портфолио-артефакт для разговоров с работодателями и референс для похожих платформенных инициатив.

`platformctl` — это Go CLI, который автоматизирует жизненный цикл приложений и сервисов в Kubernetes через строгий GitOps-пайплайн. Принцип простой: **Git + Merge Request + CI + Flux — это единственный путь до кластера**. Сама CLI никогда не делает `kubectl apply` — она генерирует манифесты, валидирует их по схемам, открывает MR в нужных репозиториях, а дальше Flux приводит кластер к нужному состоянию.

Этот README описывает **полный дизайн**, а не только подмножество кода, которое сейчас в репозитории. Каждая команда имеет маркировку:

- `[open]` — реализация присутствует в этом репозитории
- `[private]` — реализовано и работает в production-платформе, дизайн описан здесь, но код пока не выложен

---

## Зачем это существует

У платформенных команд есть повторяющаяся проблема: с ростом кластера онбординг нового приложения требует ручных правок в двух-трёх разных репозиториях (манифесты, политики Vault, переменные CI), запуска ad-hoc проверок и удержания в голове постоянно меняющегося чек-листа. Каждая команда изобретает свой способ это делать, и ошибки копятся.

`platformctl` сводит это к одному Go-бинарю с несколькими принципиальными гарантиями:

1. **Разделение read-plane и write-plane.** Read-команды (`validate`, `render`, `doctor`, `assist`, `metrics` и т.д.) идемпотентны и безопасны — агенты и люди могут их вызывать сколько угодно. Write-команды (`new-app`, `delete-app`, `secrets sync-gitlab-ci` и т.д.) по умолчанию работают в режиме dry-run и требуют явных `--apply --confirm`, чтобы что-то мутировать.
2. **Никакой прямой мутации кластера.** Записи идут через Git: команда открывает Merge Request в соответствующем репозитории, CI его валидирует, Flux применяет. CLI — это фабрика манифестов, не обёртка над kubectl.
3. **Schema-first.** Каждая спецификация, с которой работает CLI (`appspec`, `service-app`, `product-deployment`, `runbook-frontmatter`, контракты Retrieval API и т.д.), имеет JSON Schema. CI отклоняет невалидные спецификации до того, как они дойдут до кластера.
4. **Agent-friendly output.** Каждая команда поддерживает `--output text | json | minimal-json`. Режим `minimal-json` срезает API-envelope и не-обязательные поля, уменьшая потребление токенов LLM-агентами на 30–50%.
5. **RAG как полноценная подсистема.** Встроенная подкоманда `assist` даёт retrieval-слой над корпусом runbook: schema-aware ingestion, lexical-поиск как стабильный default, eval по golden-вопросам как CI-gate. Операторы и AI-агенты пользуются одним и тем же retrieval-интерфейсом.

---

## Карта команд

### Read-plane (идемпотентные)

| Команда | Статус | Назначение |
|---|---|---|
| `validate [--all]` | `[open]` | Валидация `appspec` / service-спецификаций по JSON Schema |
| `render [--all]` | `[open]` | Рендеринг сгенерированных манифестов из канонических источников |
| `doctor [--all\|<check>]` | `[open]` | Health-проверки: CNI, kube-context, CA/auth registry, policy-reports, lifecycle |
| `config <view/use-context/init/validate>` | `[private]` | Multi-context runtime-конфигурация |
| `assist <search/runbook/explain/diagnose/eval/validate-corpus/export-qdrant/prepare-qdrant-upsert/upstream-refresh>` | `[private]` | RAG-подсистема над корпусом runbook (см. `docs/assist-design.ru.md`) |
| `docs suggest` | `[private]` | Подсказка, какие docs/runbook затронуты изменением |
| `hubble <status/observe/why-dropped>` | `[private]` | Network observability через Cilium Hubble |
| `logs <status/query>` | `[private]` | Loki-запросы из CLI |
| `metrics <status/query/app>` | `[private]` | PromQL-запросы с app-aware агрегацией |
| `upgrade plan` | `[private]` | Парсинг плана апгрейдов на основе Renovate-данных |
| `deploy <init/validate/scaffold/promote/status/ci-generate>` | `[private]` | Workflow продуктового деплоя |
| `observe collect` | `[private]` | Локальный evidence-bundle на инциденты |
| `export-public` | `[open]` | Хелпер для зеркалирования curated-подмножества в публичный репозиторий (вот этот) |

### Write-plane (gated, dry-run по умолчанию)

| Команда | Статус | Safety-модель |
|---|---|---|
| `new-app <name>` | `[open]` | dry-run по умолчанию; `--auto` запускает полный flow |
| `new-service <name>` | `[open]` | dry-run по умолчанию |
| `new-product <name> --gitlab-path <path>` | `[private]` | Phase-4 автономный онбординг продукта (открывает Vault MR + product MR) |
| `delete-app <ns>/<app>` | `[open]` | `--auto-merge` только под явный GO |
| `secrets sync-gitlab-ci` | `[private]` | Ротация переменных GitLab CI из Vault; dry-run по умолчанию |
| `registry-ca sync` | `[private]` | Синхронизация registry CA-секрета по кластеру |
| `runners <list/reconcile/rotate/revoke>` | `[private]` | Lifecycle GitLab CI runners |
| `harbor-robot create` | `[private]` | Создание robot-аккаунта Harbor registry |
| `infra kubelet-provider` | `[private]` | Talos infra-операции |
| `bootstrap` | `[private]` | Bootstrap кластера; break-glass only |

---

## Что репозиторий демонстрирует сейчас

- **Модель спецификации приложения** (`internal/appspec`) — структура данных и правила валидации, на которых строится всё остальное.
- **Flow команды `new-app`** (`cmd/platformctl`) — как onboarding-команда композирует спецификации, рендерит манифесты и выдаёт результат, пригодный для CI-driven MR.
- **GitOps-граница** — намеренный выбор: `platformctl` никогда не применяет ничего сама, только предлагает изменения для Flux.
- **Разделение read/write-plane** — даже на этой ранней стадии форма команд отражает операционную дисциплину.
- **Примеры** (`examples/golden-path/`) — канонический layout сервиса в платформенном репозитории.

---

## Что описано, но пока не выложено

Репозиторий намеренно не содержит следующих частей production-имплементации:

- RAG-подсистема `assist` (lexical-retriever, Qdrant-экспортёр, golden-question evaluator, schema-aware валидатор корпуса). Архитектура описана в `docs/assist-design.ru.md`.
- Оркестрация деплоев (семейство команд `deploy`) — scaffolding, promotion, status tracking, генерация child CI-пайплайнов.
- Подкоманды операций (`hubble`, `logs`, `metrics`, `observe`) — они оборачивают внутренние эндпоинты и потребуют нетривиальной развязки.
- Plumbing для секретов и registry (`secrets`, `registry-ca`, `harbor-robot`) — тесно завязаны на конкретную внутреннюю инфраструктуру.

Эти модули будут переезжать в публичный репозиторий итеративно; см. `docs/roadmap.ru.md`.

---

## Design-документы

- [`docs/architecture.ru.md`](docs/architecture.ru.md) — read/write plane, паттерн «фабрика манифестов», GitOps-граница
- [`docs/assist-design.ru.md`](docs/assist-design.ru.md) — RAG-подсистема: lexical-as-default, schema-aware corpus contracts, CI-gated retrieval eval
- [`docs/roadmap.ru.md`](docs/roadmap.ru.md) — что публикуется, когда и что остаётся приватным
- [`docs/demo.md`](docs/demo.md) — прохождение flow `new-app`

---

## Non-goals

- Этот CLI **не** замена `kubectl`. Он никогда не обращается к Kubernetes API для мутирующих операций.
- Это **не** замена Flux. Flux остаётся единственным reconciler-ом.
- Публичный репозиторий **не** turnkey-инсталляция платформы. Это design-референс.

---

## Лицензия

Apache-2.0. См. [`LICENSE`](LICENSE).

## Security

См. [`SECURITY.md`](SECURITY.md) для политики ответственного раскрытия уязвимостей.
