> Полное ТЗ: разделы 1–26 из последнего варианта владельца и согласованные изменения от 2026-10-12.
> Раздел «Согласованные изменения» имеет приоритет над исходными формулировками разделов 1–26, включая требования TypeScript/SDK и доверенной проверки тестов.

## Согласованные изменения от 2026-10-12

### Runtime и размещение компонентов

- TypeScript в проекте не нужен.
- Go Core — единственный владелец состояния фабрики. Worker не становится вторым оркестратором.
- Core и NATS JetStream запускаются в Docker. SQLite — файл в постоянном volume Core; отдельный сервер базы данных не нужен.
- Codex Worker — отдельный нативный Go-процесс на macOS, рядом с Git, Xcode и workspace проектов.
- Worker получает команды через NATS и напрямую управляет официальным Codex app-server через двусторонний JSON-RPC. Python-обёртка для Codex не требуется.
- Для первой версии использовать stdio: Worker запускает `codex app-server` и передаёт структурированные RPC-сообщения. Не автоматизировать терминальный интерфейс Codex и не парсить его человекочитаемый вывод.
- Поддерживать RPC handshake, ответы, уведомления, server-initiated requests, запуск и возобновление threads, запуск и отмену turns, streaming событий и диагностику ошибок.
- Авторизация Codex — через ChatGPT-подписку владельца. Не подменять её платной API-key авторизацией без отдельного решения владельца.
- Перед реализацией проверить установленную версию Codex и её официальную RPC-схему; зафиксировать поддерживаемую версию и ограничения. Не выдумывать API.
- Требование исходного ТЗ «использовать только официальный TypeScript SDK» заменяется требованием использовать официальный Codex app-server API. Это явное согласованное изменение, а не скрытый fallback на CLI.
- В будущем Claude Worker может быть отдельным Python-сервисом. Контракт Go Core ↔ Worker должен оставаться нейтральным к runtime и языку реализации. Claude пока вне объёма.
- Будущий dashboard может запускаться в Docker. Web UI не входит в MVP.

### Два уровня цикла выполнения

- Внутренний цикл «модель → инструменты → результаты инструментов → модель» выполняет Codex app-server.
- Фабрика реализует внешний долговременный цикл: цели, динамическое планирование, задачи, запуск исполнителей, вопросы, ожидание ответов, возобновление, сдача результата, ревью и доработка.
- Не писать собственную замену внутреннему agent loop Codex. При этом внешний цикл оркестрации обязателен.
- План работы определяют агенты; Core валидирует и исполняет операции, а не содержит фиксированный workflow.

### Workspace, доступ и восстановление

- Для MVP — один активный writer на проект. Сразу предусмотреть интерфейс выделения workspace, чтобы позже добавить Git worktrees.
- Агенты в MVP считаются доверенными и получают полный доступ к системе, включая необходимые инструменты разработки, сеть и установку зависимостей.
- Не представлять фильтры Factory MCP как строгую изоляцию от обхода через терминал при полном системном доступе. Различать проверяемые Core операции и инструкции доверенным агентам.
- Сохранять согласования для явно защищённых действий фабрики. Не требовать подтверждения каждой обычной операции разработки.
- После сбоя с неизвестным результатом не выполнять слепой автоматический retry. Освободить слот выполнения, проверить состояние workspace и возможные побочные эффекты, затем принять решение о продолжении. Если результат установить нельзя — эскалировать владельцу.
- Постоянная логическая сессия менеджера с владельцем создаётся отдельно на каждый проект. Вопросы разработчиков обрабатываются независимыми запусками менеджера. Не выполнять конкурентные turns в одной SDK/RPC-сессии.

### Единая система policies

- Не создавать три независимых механизма policies, escalation и reporting и не дублировать одни правила в нескольких промптах.
- Использовать единый каталог policies, единые правила загрузки, наследования и приоритетов.
- Внутри policies различать типы правил: доступ/согласование; эскалация; отчётность.
- Правила доступа определяют разрешённые, запрещённые и требующие согласования операции.
- Правила эскалации определяют, какие решения агент принимает сам, какие направляет менеджеру и какие — владельцу.
- Правила отчётности определяют значимые события, о которых агент должен сообщить после выполнения.
- Агент подключает политики по имени; правила могут учитывать роль или конкретного агента.
- Context Builder собирает согласованную инструкцию из применимых правил. Core проверяет структурированные операции, поведенческие правила передаются доверенному агенту как инструкции.

### Отчёты о значимых событиях

- Помимо вопросов и ответов поддержать отчёты агентов о существенных действиях: установка зависимостей, изменение окружения, отклонение от плана, обнаруженная проблема, завершение этапа.
- Предусмотреть `factory.report.create` и ленту событий проекта, сохранение в SQLite, чтение через API/CLI и последующее отображение в dashboard.
- В отчёте указывать, что сделано, зачем, какие файлы/зависимости/настройки изменены, результат и требуется ли действие владельца. Связывать отчёт с проектом, задачей, запуском и артефактами, когда применимо.
- Информация о значимом действии сама по себе не блокирует работу. Вопрос или запрос approval оформляется отдельно, если того требуют policies.
- Наблюдаемые Worker технические события по возможности фиксировать автоматически. Не создавать отчёт на каждый несущественный шаг.

### Приёмка и утверждение артефактов

- Для MVP доверять заявлению агента о выполнении работы и результатах проверок. Отдельный обязательный доверенный механизм запуска сборок/тестов не требуется.
- Это отменяет противоположное требование шага 8 исходного раздела 20.
- Сдача результата по-прежнему может переводить задачу в IN_REVIEW; менеджер принимает или отклоняет работу согласно policies.
- Отдельные артефакты, например дизайн и макеты, могут требовать утверждения владельцем.
- Различать завершение работы исполнителем и утверждение артефакта. Прикреплять артефакт к задаче, создавать запрос в inbox владельца и привязывать approval к конкретной версии артефакта.
- Существенно изменённая версия артефакта требует нового утверждения, если оно требуется правилами.
- Зависимые задачи могут ожидать утверждения артефакта. Не зашивать обязательную цепочку Design → Approval → Development в Core.

### Объём первого задания

Реализовать Foundation: Go Core, SQLite, NATS JetStream, загрузку YAML-агентов и минимальный нативный Go Codex Worker через официальный app-server RPC. Продемонстрировать запуск одного агента, получение событий и сохранение результата. Остальные этапы пока не реализовывать. Каждый этап должен собираться и проходить проверки перед переходом к следующему.

### Статус на момент передачи

Реализация остановлена по просьбе владельца. Созданы только `go.mod` (`module appfactory`, Go 1.24.1), начальная структура `openspec/` с change `foundation` и рабочая `.cache/`. Исходный код сервисов ещё не написан, зависимости NATS не установлены. Установленный Codex — 0.162.1. Docker CLI и Compose доступны, но Docker daemon на момент проверки не был запущен. У команд этой сессии действовал `CODEX_SANDBOX_NETWORK_DISABLED=1`; `go get` завершался ошибкой DNS. Это ограничение сессии разработки, а не требование к будущей фабрике.

---

# Техническое задание: App Factory Orchestrator

## 1. Цель проекта

Разработать локальную систему оркестрации автономных AI-агентов для создания программных продуктов.

Система должна работать на macOS и позволять создавать команды AI-агентов, которые:

- Настраиваются декларативно через YAML.
- Используют разные AI runtime.
- Получают задачи и самостоятельно выполняют их.
- Создают новые задачи и делегируют работу другим агентам.
- Общаются друг с другом через инструменты фабрики.
- Задают вопросы владельцу, когда не могут самостоятельно принять решение.
- Используют общий реестр skills.
- Работают параллельно в независимых сессиях.
- Сохраняют состояние между перезапусками.
- Поддерживают долговременные проекты с OpenSpec.
- Применяют настраиваемые правила доступа и ограничения.
- Не требуют изменений исходного кода для добавления нового агента.

На первом этапе реализовать только **Codex Runtime**.

Архитектура должна предусматривать подключение Claude Agent SDK и других runtime в будущем.

### Основной принцип

**Оркестратор не должен содержать жёстко заданных сценариев разработки приложений.**

Например, в коде не должно быть последовательности:

```text
Manager → iOS Developer → QA → Release
```

Вместо этого Manager должен иметь возможность динамически создавать задачи, назначать исполнителей, изменять план и инициировать проверки.

Go Core отвечает за корректное выполнение этих операций, но не за принятие продуктовых решений.

---

# 2. Архитектура

Реализовать систему из трёх основных компонентов.

```text
                      USER
                       │
                       ▼
                 Factory API
                       │
                       ▼
                 GO FACTORY CORE
                       │
          ┌────────────┼────────────┐
          │            │            │
          ▼            ▼            ▼
        SQLite       OpenSpec     YAML Config
          │            │            │
          └────────────┼────────────┘
                       │
                       ▼
                 NATS JetStream
                       │
                       ▼
                CODEX WORKER
                 TypeScript
                       │
                       ▼
                Codex SDK
                       │
                       ▼
                Factory MCP
                       │
                       ▼
                 GO FACTORY CORE
```

## 2.1. Go Factory Core

Основной сервис.

Отвечает за:

- Загрузку и валидацию конфигураций.
- Управление проектами.
- Управление задачами и зависимостями.
- Управление сессиями и запусками агентов.
- Очередь работ.
- Коммуникацию между агентами.
- Решения и approvals.
- Применение политик.
- Интеграцию OpenSpec.
- Предоставление Factory API и MCP.
- Публикацию событий.
- Восстановление состояния после перезапуска.

**Go Core не должен напрямую вызывать Codex SDK или Claude SDK.**

## 2.2. Codex Worker

Отдельный сервис на TypeScript.

Использовать официальный Codex SDK.

Отвечает за:

- Получение команд запуска через NATS.
- Создание Codex-сессий.
- Передачу инструкций, контекста и конфигурации.
- Подключение Factory MCP.
- Выполнение агентных задач.
- Streaming событий.
- Обработку ошибок.
- Отмену запусков.
- Сохранение идентификаторов сессий.
- Возобновление работы, когда это поддерживается SDK.
- Применение runtime-ограничений.

Worker не должен управлять состоянием задач в SQLite напрямую.

Он взаимодействует с Go Core через события и Factory API/MCP.

## 2.3. NATS JetStream

Использовать для асинхронной коммуникации между сервисами.

Требования:

- Durable consumers.
- Подтверждение обработки сообщений.
- Повторная доставка.
- Идемпотентная обработка.
- Correlation IDs.
- Версионирование сообщений.
- Поддержка нескольких экземпляров worker.

Использовать JetStream для критически важных команд и событий, а не обычный ephemeral Pub/Sub.

---

# 3. Структура репозитория

Предлагаемая структура:

```text
app-factory/
├── cmd/
│   └── factory/
│       └── main.go
│
├── internal/
│   ├── config/
│   ├── agents/
│   ├── projects/
│   ├── tasks/
│   ├── scheduler/
│   ├── runs/
│   ├── sessions/
│   ├── messaging/
│   ├── decisions/
│   ├── approvals/
│   ├── policies/
│   ├── skills/
│   ├── openspec/
│   ├── events/
│   ├── nats/
│   ├── mcp/
│   ├── api/
│   └── storage/
│
├── workers/
│   └── codex/
│       ├── src/
│       │   ├── main.ts
│       │   ├── runner.ts
│       │   ├── sessions.ts
│       │   ├── events.ts
│       │   └── config.ts
│       ├── package.json
│       └── tsconfig.json
│
├── agents/
│   ├── manager.yaml
│   ├── ios-developer.yaml
│   └── qa.yaml
│
├── profiles/
│   └── developer.yaml
│
├── prompts/
│   ├── manager.md
│   ├── ios-developer.md
│   └── qa.md
│
├── skills/
│   ├── registry.yaml
│   ├── openspec/
│   │   └── SKILL.md
│   └── swiftui/
│       └── SKILL.md
│
├── policies/
│   ├── global.yaml
│   └── engineering.yaml
│
├── company/
│   ├── strategy.md
│   └── product-principles.md
│
├── projects/
│
├── migrations/
├── docs/
├── examples/
├── Makefile
└── README.md
```

Структуру разрешается улучшать, если есть обоснованные причины.

Не создавать абстракции и пакеты без практической необходимости.

---

# 4. Конфигурация агентов

## 4.1. Общие требования

Каждый агент определяется YAML-файлом.

Добавление нового агента не должно требовать изменений Go-кода.

Поддержать:

- Идентификатор.
- Описание.
- Runtime.
- Модель.
- Prompt.
- Skills.
- Capabilities.
- Tools.
- Approvals.
- Policies.
- Communication.
- Context.
- Execution limits.
- Наследование профилей.

Пример:

```yaml
apiVersion: factory/v1
kind: Agent

metadata:
  name: ios-developer
  description: Senior iOS Developer

spec:
  extends: developer

  runtime:
    provider: codex
    model: default

  prompt:
    file: prompts/ios-developer.md

  capabilities:
    - ios.development
    - ios.swiftui
    - ios.testing

  skills:
    include:
      - swiftui
      - openspec

    discover:
      enabled: true
      categories:
        - ios
        - testing

  tools:
    mode: denylist

    deny:
      - factory.release.publish
      - factory.project.delete

  approvals:
    mode: selective

  communication:
    can_message:
      - manager
      - qa

    can_create_tasks: true

    can_assign_tasks: false

  context:
    include:
      - current_task
      - relevant_specs
      - relevant_decisions

  execution:
    max_concurrent_runs: 1
    timeout: 2h
    max_retries: 3

  policies:
    - global
    - engineering
```

## 4.2. Режимы доступа к инструментам

Поддержать:

```yaml
tools:
  mode: allowlist
  allow:
    - factory.task.read
    - factory.message.send
```

```yaml
tools:
  mode: denylist
  deny:
    - factory.release.publish
```

```yaml
tools:
  mode: unrestricted
```

Семантика:

- `allowlist`: доступны только перечисленные инструменты.
- `denylist`: доступны все инструменты, предоставленные runtime и интеграциями, кроме запрещённых.
- `unrestricted`: отсутствует фильтрация на уровне списка инструментов.

**Режим по умолчанию — `denylist`.**

При этом обязательные системные ограничения не могут быть отменены конфигурацией агента.

Особенно важно: запрет Factory MCP-инструмента не является полноценным запретом действия, если агент может выполнить то же самое через терминал. Реальные ограничения на файловую систему, сеть, секреты и публикацию должны обеспечиваться также на уровне окружения worker.

Если worker не может применить обязательное ограничение, он должен отклонить запуск с понятной ошибкой.

## 4.3. Наследование

Поддержать `extends`:

```yaml
spec:
  extends: developer
```

Правила:

- На первом этапе достаточно одного уровня наследования.
- Scalar-поля дочернего агента переопределяют профиль.
- Списки skills можно расширять.
- Обязательные policies нельзя удалить.
- Запрещённые глобальными policies инструменты нельзя разрешить через наследование.
- Циклические ссылки должны выявляться при валидации.

---

# 5. Общий реестр skills

Реализовать единый каталог навыков.

```yaml
skills:
  swiftui:
    path: swiftui/SKILL.md
    description: SwiftUI development
    categories:
      - ios
      - development

  openspec:
    path: openspec/SKILL.md
    description: OpenSpec specification management
    categories:
      - planning
      - documentation
```

## Требования

1. Skills хранятся в общем реестре.
2. Агент может явно подключать skills.
3. Агент может обнаруживать дополнительные skills по категориям.
4. Подробное содержимое skills загружается по запросу.
5. Не загружать все `SKILL.md` в prompt при старте.
6. Поддержать дополнительные файлы skill: `references/`, `scripts/`.
7. Проверять существование skills при загрузке конфигурации.

Предусмотреть Factory MCP-инструменты:

```text
factory.skills.list
factory.skills.search
factory.skills.read
```

Формат навыков должен быть совместим с распространённым подходом Agent Skills (`SKILL.md` с YAML frontmatter).

Если официальный Codex SDK предоставляет нативную интеграцию skills, использовать её там, где это практично. Не дублировать встроенные возможности SDK без необходимости.

---

# 6. Проекты и OpenSpec

Каждый проект должен иметь отдельную рабочую директорию.

Пример:

```text
projects/
└── finance-app/
    ├── project.yaml
    ├── openspec/
    │   ├── specs/
    │   └── changes/
    └── app/
```

Go Core должен:

- Создавать проекты.
- Хранить метаданные.
- Предоставлять путь к workspace.
- Связывать задачи с OpenSpec changes.
- Давать агентам доступ к актуальным спецификациям.

**OpenSpec — источник продуктовых требований и истории изменений, но не runtime-хранилище задач.**

SQLite хранит статусы, исполнителей, зависимости, попытки выполнения и сообщения.

Не дублировать полный текст OpenSpec-документов в базе.

На первом этапе интеграция может быть файловой: создание структуры, чтение и проверка ссылок. Не нужно реализовывать собственный аналог OpenSpec.

---

# 7. Система задач

## 7.1. Модель задачи

Минимальные поля:

```text
id
project_id
title
description
status
priority
assignee
created_by
parent_task_id
openspec_change
created_at
updated_at
version
```

Дополнительные сущности:

```text
task_dependencies
task_attempts
task_comments
task_artifacts
```

## 7.2. Статусы

```text
BACKLOG
   │
   ▼
READY
   │
   ▼
IN_PROGRESS
   │
   ├── BLOCKED
   │
   ▼
IN_REVIEW
   │
   ├── REWORK
   │
   ▼
DONE
```

Также предусмотреть `CANCELLED` и `FAILED`.

Переходы должны проверяться Go Core.

Агент не может произвольно установить любой статус.

## 7.3. Зависимости

Поддержать DAG зависимостей.

Задача становится доступной для запуска, когда завершены её обязательные зависимости.

Не допускать циклических зависимостей.

## 7.4. Динамическое планирование

Manager должен иметь возможность:

- Создать задачу.
- Изменить задачу.
- Назначить исполнителя.
- Добавить зависимости.
- Разбить задачу на подзадачи.
- Запросить ревью.
- Принять или отклонить результат.

Не реализовывать фиксированный workflow разработки.

---

# 8. Agent Runs и Sessions

Разделить три сущности:

**Agent** — конфигурация специалиста.

**Session** — контекст и история взаимодействия.

**Run** — конкретный запуск агента.

Пример:

```json
{
  "run_id": "run-123",
  "agent_id": "ios-developer",
  "task_id": "TASK-42",
  "session_id": "session-88",
  "status": "running"
}
```

## Требования

- Один агент может иметь несколько независимых сессий.
- Один агент может выполнять несколько запусков, если разрешено конфигурацией.
- Сессии разных задач не должны смешиваться.
- Для диалога владельца с менеджером должна существовать постоянная логическая сессия.
- Для вопросов разработчиков создавать отдельные запуски менеджера.
- Сохранять SDK session/thread identifiers, если они доступны.
- Не считать SDK-сессию единственным источником состояния.
- После перезапуска восстанавливать задачи и ожидающие сообщения из SQLite.

Каждый запуск должен сохранять:

```text
run_id
agent_id
task_id
session_id
runtime_provider
config_hash
status
attempt
started_at
finished_at
result
error
```

Также сохранять версию конфигурации и набор skills, использованных при запуске.

---

# 9. Коммуникация между агентами

Реализовать асинхронную систему сообщений.

Минимальная модель:

```text
message_id
project_id
task_id
sender
recipient
conversation_id
message_type
body
status
created_at
```

Типы сообщений:

```text
question
answer
notification
review_request
review_result
escalation
```

Пример:

Developer вызывает:

```json
{
  "recipient": "manager",
  "task_id": "TASK-42",
  "question": "Should registration be mandatory?",
  "blocking": true
}
```

Go Core:

1. Сохраняет вопрос.
2. Проверяет полномочия отправителя.
3. Определяет получателя.
4. Создаёт отдельную работу для Manager.
5. Отправляет команду в NATS.
6. Получает ответ.
7. Сохраняет ответ.
8. Возобновляет Developer, если задача была заблокирована.

Не держать worker бесконечно ожидающим ответа пользователя.

Если ответ требует длительного ожидания, запуск должен завершиться или перейти в управляемое состояние ожидания, а дальнейшая работа — возобновиться отдельным запуском.

### Защита от бесконечных диалогов

Предусмотреть:

- Ограничение количества автоматических уточнений.
- Обнаружение повторяющихся вопросов.
- Тайм-ауты.
- Эскалацию нерешённых вопросов.
- Correlation ID для каждой цепочки сообщений.

---

# 10. Общение с владельцем

Для MVP реализовать через HTTP API.

Telegram пока не обязателен.

Необходимые операции:

```text
POST /projects
GET  /projects

POST /conversations/{id}/messages
GET  /conversations/{id}/messages

GET  /inbox
POST /inbox/{id}/answer

GET  /tasks
GET  /tasks/{id}

GET  /runs
GET  /runs/{id}
```

Пользователь должен иметь возможность:

1. Создать проект.
2. Написать менеджеру.
3. Получить ответ.
4. Ответить на вопрос менеджера.
5. Посмотреть задачи и запуски.

Для структурированных вопросов предусмотреть модель:

```json
{
  "title": "Product clarification",
  "questions": [
    {
      "id": "audience",
      "type": "single_choice",
      "question": "Who is the target audience?",
      "options": [
        "General users",
        "Professionals"
      ]
    }
  ]
}
```

На первом этапе достаточно типов:

```text
text
single_choice
multiple_choice
confirmation
```

Не нужно реализовывать полноценный конструктор форм.

---

# 11. Decision Registry

Создать механизм хранения значимых решений.

Пример:

```text
decision_id
project_id
topic
status
value
rationale
created_by
approved_by
version
created_at
updated_at
```

Статусы:

```text
proposed
under_discussion
approved
superseded
rejected
```

Требования:

- Различать предложения и утверждённые решения.
- Сохранять основания решений.
- Предоставлять агентам актуальные утверждённые решения.
- Не позволять параллельным запускам незаметно перезаписывать друг друга.
- Использовать optimistic concurrency control.

Менеджер должен обращаться к Decision Registry перед повторным вопросом владельцу.

---

# 12. Approvals и политики

Реализовать:

```yaml
approvals:
  mode: selective

  require:
    - factory.release.publish
    - factory.secrets.access_production
    - external.purchase
```

Поддержать:

- `never`
- `selective`
- `always`
- `inherit`

Это конфигурационные режимы, которые интерпретирует фабрика. Они не должны автоматически отключать обязательные проверки безопасности.

## Глобальные ограничения

На первом этапе обязательно защитить:

- Удаление проектов.
- Публикацию приложений.
- Доступ к production credentials.
- Платные внешние операции.
- Административное изменение конфигурации фабрики.

Подтверждение должно быть привязано к конкретному действию и его параметрам, а не выдавать бессрочное разрешение на произвольные операции.

Разрешённые операции разработки — чтение и изменение файлов проекта, сборки, тесты, локальные Git-коммиты — должны выполняться без постоянных вопросов при условии корректной изоляции workspace.

---

# 13. Factory MCP

Go Core должен предоставлять MCP-интерфейс для агентов.

Минимальные инструменты:

### Tasks

```text
factory.task.get
factory.task.list
factory.task.create
factory.task.update
factory.task.assign
factory.task.submit
factory.task.accept
factory.task.reject
```

### Messaging

```text
factory.message.send
factory.message.ask
factory.message.reply
```

### Projects

```text
factory.project.get
factory.project.context
```

### Decisions

```text
factory.decision.list
factory.decision.get
factory.decision.propose
factory.decision.record
```

### Owner

```text
factory.owner.ask
factory.owner.request_approval
```

### Skills

```text
factory.skills.list
factory.skills.search
factory.skills.read
```

Каждый вызов должен проверять:

- Идентичность агента.
- Права.
- Принадлежность к проекту.
- Допустимость операции.
- Версию изменяемого объекта, если применимо.

Не доверять `agent_id`, переданному самой моделью. Идентичность должна определяться сервером по выданным для запуска credentials.

---

# 14. Событийная модель NATS

Использовать subjects:

```text
factory.commands.agent.run
factory.commands.agent.cancel

factory.events.agent.started
factory.events.agent.completed
factory.events.agent.failed

factory.events.message.created
factory.events.message.answered

factory.events.task.created
factory.events.task.updated
factory.events.task.submitted

factory.events.approval.requested
factory.events.approval.resolved
```

Общий envelope:

```json
{
  "event_id": "evt-123",
  "schema_version": 1,
  "type": "agent.run.requested",
  "correlation_id": "corr-123",
  "causation_id": "evt-100",
  "occurred_at": "2026-10-12T12:00:00Z",
  "payload": {}
}
```

## Надёжность

Обязательно реализовать:

- Transactional outbox для SQLite → NATS.
- Идемпотентную обработку команд.
- Повторную доставку.
- Run leases.
- Heartbeats для долгих запусков.
- Обнаружение потерянных запусков.
- Безопасное восстановление после перезапуска.

Не обещать exactly-once выполнение SDK-запросов. При падении worker может оказаться неизвестно, успел ли агент выполнить внешнее действие.

Поэтому операции с побочными эффектами должны иметь собственную идемпотентность или требовать повторной проверки перед retry.

---

# 15. Codex Worker

Реализовать TypeScript-сервис на официальном Codex SDK.

## Обязанности

1. Подключиться к NATS.
2. Получить `agent.run.requested`.
3. Проверить и нормализовать RunSpec.
4. Создать или продолжить Codex-сессию.
5. Подключить Factory MCP.
6. Передать prompt и контекст.
7. Запустить выполнение.
8. Публиковать события.
9. Сохранить результат.
10. Корректно обработать отмену и ошибки.

## Важные требования

**Не использовать CLI как основной интеграционный интерфейс.**

Не писать собственную реализацию Codex agent loop.

Использовать официальные SDK API.

Если какая-то необходимая функция недоступна в SDK, явно документировать ограничение и предусмотреть интерфейс расширения. Не подменять молча SDK вызовами CLI.

### Runtime abstraction

В Go определить нейтральную модель:

```go
type RuntimeProvider string

const (
    RuntimeCodex  RuntimeProvider = "codex"
    RuntimeClaude RuntimeProvider = "claude"
)
```

Но не создавать сейчас фиктивный Claude Worker.

Достаточно, чтобы архитектура позволяла зарегистрировать новый provider через конфигурацию и отдельный consumer.

Внутренний контракт запуска не должен содержать Codex-специфичных полей на верхнем уровне. Специальные настройки провайдера можно хранить в `runtime_options`.

---

# 16. Scheduler

Go Scheduler должен:

- Находить задачи READY.
- Проверять зависимости.
- Проверять доступность исполнителя.
- Учитывать лимиты параллельности.
- Создавать Agent Run.
- Отправлять команду через outbox.
- Обрабатывать результаты.
- Переводить задачи в соответствующие статусы.
- Повторять неудачные запуски согласно политике.
- Не запускать одну задачу дважды из-за повторной доставки сообщения.

Никакой бизнес-логики вида «после Developer всегда запускаем QA» в Scheduler быть не должно.

Если нужна проверка QA, Manager создаёт соответствующую задачу или назначает reviewer.

# 17. Контекст и память

Реализовать Context Builder.

Он должен собирать контекст запуска из:

- Prompt агента.
- Текущей задачи.
- Проектных документов.
- OpenSpec.
- Актуальных решений.
- Релевантных сообщений.
- Каталога доступных skills.

Не передавать агенту:

- Историю всех проектов.
- Все сообщения фабрики.
- Полное содержимое всех skills.
- Секреты, не необходимые для задачи.

История диалога и состояние проекта должны храниться отдельно.

Для MVP не требуется vector database или семантическая память.

# 18. CLI и локальный запуск фабрики

Хотя CLI не используется как интеграция с Codex, собственный CLI для управления фабрикой нужен.

Примеры команд:

```text
factory init
factory config validate
factory agents list
factory projects create finance-app
factory tasks list --project finance-app
factory runs list
factory doctor
```

`factory doctor` должен проверять:

- Подключение к NATS.
- Доступность SQLite.
- Корректность конфигураций.
- Доступность Codex Worker.
- Наличие необходимых зависимостей.
- Доступность Factory MCP.

Предусмотреть простой способ запуска всех локальных компонентов через Makefile или аналогичный dev-script.

# 19. Тестирование

Обязательно реализовать unit- и integration-тесты.

## Unit tests

Проверить:

- Загрузку YAML.
- Наследование профилей.
- Валидацию skills.
- Allowlist/denylist.
- Обязательные policies.
- Переходы статусов задач.
- Проверку зависимостей.
- Optimistic concurrency.
- Idempotency.

## Integration tests

Проверить:

- Go Core ↔ NATS.
- NATS ↔ Codex Worker.
- Factory MCP ↔ Go Core.
- Создание задачи → запуск агента → получение результата.
- Вопрос разработчика → запуск менеджера → ответ.
- Перезапуск worker во время выполнения.
- Повторную доставку события.
- Запрет неавторизованного MCP-вызова.

Для обычных CI-тестов использовать fake runtime, не требующий реального обращения к модели.

Отдельно предусмотреть opt-in end-to-end тест с настоящим Codex SDK.

# 20. MVP: обязательный сквозной сценарий

После реализации должен работать следующий сценарий.

## Шаг 1. Пользователь создаёт проект

```text
factory projects create habit-tracker
```

## Шаг 2. Пользователь отправляет идею менеджеру

```text
Build a simple habit tracker iOS application.
```

## Шаг 3. Manager Agent

Manager:

- Читает стратегию компании.
- Уточняет действительно важные вопросы.
- Создаёт OpenSpec change.
- Формирует задачи.
- Назначает задачу iOS Developer.

На этом этапе Manager тоже работает через Codex SDK.

## Шаг 4. Developer Agent

Developer:

- Получает задачу.
- Читает OpenSpec.
- Использует нужные skills.
- Начинает реализацию.

## Шаг 5. Developer задаёт вопрос

Например:

```text
Should users be able to create habits without registration?
```

## Шаг 6. Manager отвечает

Go Core создаёт независимый запуск Manager.

Manager:

- Читает проектные решения.
- Отвечает самостоятельно, если может.
- Иначе создаёт вопрос владельцу.

## Шаг 7. Developer продолжает

После ответа Developer получает обновлённый контекст и продолжает задачу.

## Шаг 8. Developer сдаёт работу

Developer вызывает:

```text
factory.task.submit
```

С результатом:

```json
{
  "task_id": "TASK-42",
  "summary": "Implemented habit creation screen",
  "commit": "abc123",
  "checks": [
    "build passed",
    "tests passed"
  ]
}
```

Go Core переводит задачу в IN_REVIEW.

Не считать текстовое заявление агента о прохождении проверок достаточным доказательством. Для автоматической приёмки нужны результаты проверок, полученные доверенным механизмом выполнения.

> Изменено владельцем после обсуждения: для MVP доверять агентам; обязательный доверенный механизм проверок не нужен. См. согласованные изменения в начале документа.

## Шаг 9. Manager проверяет

Manager принимает или отклоняет задачу.

При отклонении создаётся запрос на доработку.

## Шаг 10. Система продолжает работу

Scheduler находит следующие готовые задачи и запускает их.

Этот сценарий является главным критерием готовности MVP.

# 21. Что не входит в MVP

Не реализовывать сейчас:

- Claude Runtime.
- Telegram.
- Web UI.
- App Store Connect.
- Автоматическую публикацию.
- Маркетинговых агентов.
- Vector database.
- Kubernetes.
- Redis.
- Temporal.
- Собственную LLM memory system.
- Сложный визуальный workflow editor.
- Биллинг и учёт стоимости токенов.
- Многоуровневую иерархию организаций.

Однако архитектура не должна препятствовать добавлению этих возможностей позже.

# 22. Порядок реализации

Работу разбить на этапы.

| Этап | Результат |
| --- | --- |
| 1. Foundation | Go-сервис, SQLite, NATS, конфигурация |
| 2. Agent Config | YAML, profiles, skills, policies |
| 3. Runtime | TypeScript Codex Worker через официальный SDK |
| 4. Task Engine | Задачи, зависимости, scheduler, runs |
| 5. Factory MCP | Инструменты для агентов |
| 6. Messaging | Вопросы, ответы, независимые запуски |
| 7. Decisions & Approvals | Решения и взаимодействие с владельцем |
| 8. OpenSpec | Проектная документация и изменения |
| 9. E2E | Полный сценарий Manager → Developer → Review |

Каждый этап должен иметь работающие тесты.

Не начинать следующий этап, пока предыдущий не собирается и не проходит проверки.

> Изменено после обсуждения: Runtime — Go Worker с официальным app-server RPC; минимальная runtime-интеграция входит уже в первое задание Foundation. См. согласованные изменения.

# 23. Требования к качеству реализации

Код должен быть:

- Простым.
- Модульным.
- Хорошо типизированным.
- Тестируемым.
- Без ненужных зависимостей.
- С понятными ошибками.
- С поддержкой graceful shutdown.
- С structured logging.
- С возможностью диагностики запусков.

Использовать context.Context в Go для отмены и тайм-аутов.

Не хранить секреты в YAML.

Не использовать глобальное изменяемое состояние без необходимости.

Не допускать прямого доступа агентов к SQLite.

Не смешивать Factory Core и Agent Runtime.

Не реализовывать собственный аналог Hermes, Paperclip или Codex SDK.

# 24. Документация

Подготовить:

```text
README.md

docs/
├── architecture.md
├── agent-configuration.md
├── skills.md
├── policies.md
├── runtime-protocol.md
├── messaging.md
├── task-lifecycle.md
├── development.md
└── adding-runtime.md
```

Особенно важно adding-runtime.md.

Он должен объяснять, как в будущем добавить Claude Worker без изменения существующего Task Engine и Factory MCP.

# 25. Definition of Done

MVP считается готовым, если:

1. Фабрика запускается локально на macOS.
2. Агенты добавляются через YAML.
3. Работает наследование профилей.
4. Работает общий реестр skills.
5. Поддерживаются allowlist, denylist, unrestricted.
6. Обязательные policies реально применяются.
7. Codex Worker использует официальный SDK.
8. Go Core и Worker общаются через NATS JetStream.
9. Задачи сохраняются в SQLite.
10. Работают зависимости задач.
11. Работают параллельные независимые запуски Manager.
12. Агенты могут обмениваться сообщениями.
13. Manager может отвечать Developer.
14. Manager может задавать вопросы владельцу.
15. Поддерживается возобновление работы после ответа.
16. Решения сохраняются и версионируются.
17. Работает Factory MCP.
18. OpenSpec используется для спецификаций.
19. После перезапуска система восстанавливает незавершённую работу.
20. Сквозной сценарий Manager → Developer → Question → Answer → Submit → Review проходит успешно.

> Пункт 7 актуализирован согласованными изменениями: официальный Codex app-server RPC вместо SDK.

# 26. Инструкция агенту-исполнителю

Этот блок я бы добавил в конец ТЗ как непосредственную инструкцию Codex.

## Implementation instructions

You are implementing App Factory Orchestrator from the specification above.

Before writing code:

1. Inspect the repository and existing project structure.
2. Verify the current official Codex SDK API and its capabilities.
3. Identify SDK limitations that affect the proposed architecture.
4. Propose a concise implementation plan.
5. Create an OpenSpec change describing the implementation.

During implementation:

- Follow the architecture described in the specification.
- Keep Go Core independent from AI runtime implementations.
- Use the official Codex SDK, not CLI subprocess integration.
- Implement features incrementally.
- Write tests alongside the implementation.
- Prefer simple solutions over speculative abstractions.
- Do not introduce additional infrastructure without a concrete need.
- Document deviations from the specification.
- Keep the repository in a buildable state.

Important architectural constraints:

- Agent workflows must be dynamic, not hard-coded.
- Agents must be configurable through YAML.
- Skills must be reusable and lazily loaded.
- Agent communication must be asynchronous and durable.
- All state-changing operations must be validated by Go Core.
- NATS messages must be processed idempotently.
- The system must support additional runtime providers in the future.

Execution strategy:

Implement the foundation first, then the runtime integration, then task orchestration, messaging, and finally the end-to-end scenario.

Do not attempt to implement all components simultaneously.

When a requirement is ambiguous, choose the simplest design that preserves the stated architectural constraints. Record the decision.

When a requirement conflicts with the actual SDK capabilities, do not invent an API or silently replace the SDK with CLI. Document the limitation and propose the smallest compatible alternative.

Final deliverables:

- Working source code.
- Database migrations.
- Example agent configurations.
- Example skills.
- Automated tests.
- Local development commands.
- Architecture documentation.
- Demonstration of the complete end-to-end scenario.

> Эта инструкция применяется с учётом согласованных изменений: Go Worker и официальный Codex app-server RPC вместо TypeScript SDK. На первом задании ограничиться Foundation.

---

## Ещё три решения, которые я бы зафиксировал до старта

Первое — Go Core должен быть единственным владельцем состояния фабрики. NATS доставляет события, Worker выполняет агентов, но только Core решает, какие задачи существуют, кто их выполняет и что считается завершённым. Это убережёт нас от нескольких независимых источников истины.

Второе — Codex Worker не должен становиться вторым оркестратором. Пусть он управляет SDK-сессиями и локальным выполнением. Создание задач, межагентная коммуникация и согласования проходят через Factory MCP. Иначе мы быстро получим две конкурирующие системы делегирования.

Третье — я бы сразу предусмотрел Git worktrees для параллельных разработчиков. Если два агента одновременно редактируют один iOS-проект в общей директории, конфликты практически неизбежны. У каждого изменяющего код запуска должен быть собственный workspace или управляемая блокировка. Для MVP можно начать с одного активного writer на проект, но интерфейс выделения workspace стоит заложить сразу.

## И небольшая поправка к объёму

Это ТЗ описывает целевую архитектуру и полноценный MVP, а не задачу на один короткий запуск Codex.

Я бы не отдавал агенту команду «реализуй всё целиком». Лучше передать ему это ТЗ как основную спецификацию и первым заданием попросить:

«Реализуй Foundation: Go Core, SQLite, NATS JetStream, загрузку YAML-агентов и минимальный Codex Worker на официальном SDK. Продемонстрируй запуск одного агента, получение событий и сохранение результата. Остальные этапы пока не реализуй».

Так мы сначала проверим самое рискованное место архитектуры — взаимодействие Go, NATS и Codex SDK — и не построим поверх него несколько тысяч строк кода до первой реальной проверки.

> Первое задание согласовано с поправкой: нативный Go Worker через официальный Codex app-server RPC. Текст выше сохранён как исходная формулировка владельца.

