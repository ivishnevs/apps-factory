# App Factory Orchestrator

Реализованы Foundation, Agent Config и Task Engine: Go Core, Postgres, NATS JetStream, YAML-агенты и профили,
реестр skills, единый каталог policies, проекты/задачи с зависимостями, scheduler, Factory MCP, сообщения/inbox и нода
`factory-node` с Go-драйверами Codex и Claude (Claude — через per-run shim на Claude Agent SDK).
Core работает в Docker, Worker — на macOS рядом с workspace, Git и Xcode.

## Запуск

Нужны Go 1.25+, Docker Desktop с запущенным daemon, Python 3 для smoke script
и **Codex 0.162.1**, авторизованный через `codex login` с ChatGPT.
API key не используется. Worker отклоняет другие версии Codex до проверки их RPC-схемы.

```sh
cp .env.example .env # только если .env ещё не существует
# Замените оба значения случайными hex-строками; например: openssl rand -hex 32
chmod 600 .env
set -a
source .env
set +a
export NATS_URL="nats://factory:${NATS_PASSWORD}@127.0.0.1:4222"
make build
make up
./bin/factory config validate
./bin/factory doctor
./bin/codex-worker
# или нода; для агентов с runtime.provider: claude включите драйвер (нужны Node 22+ и вход через `claude` /login):
mkdir -p data && printf 'drivers:\n  claude: {enabled: true}\n' > data/node.yaml
./bin/factory-node run -config data/node.yaml
```

Claude: [docs/claude-runtime.md](docs/claude-runtime.md); нода: [docs/development.md](docs/development.md#node-process).

Core и NATS публикуют порты только на loopback. `.env` исключён из Git и Docker build context.
В этой рабочей копии `.env` уже создан со случайными значениями. Не заменяйте его при обычном запуске.
Postgres хранит БД в volume `postgres-data`, Core — blobs в `core-blobs` и только `core.incarnation` в `core-data`;
NATS — сообщения в `nats-data`.
`make down` останавливает контейнеры и сохраняет данные.

В другом терминале загрузите `.env`, затем:

```sh
mkdir -p .cache/demo-workspace
./bin/factory runs create demo-project demo "$PWD/.cache/demo-workspace" \
  'Reply with exactly FOUNDATION_OK. Do not use tools or modify files.'
./bin/factory runs list
./bin/factory runs get RUN_ID
./bin/factory runs events RUN_ID
./bin/factory runs cancel RUN_ID
```

Workspace — абсолютный путь **macOS**, не путь контейнера. Для продолжения передайте сохранённый
`session_id` последним аргументом `runs create`. Каждый run запускает отдельный app-server;
threads сохраняются самим Codex, их ID и результаты сохраняются Core.

## Проверки

```sh
make check
# Интеграционный тест: отдельный одноразовый NATS, без настоящей модели.
docker run -d --name appfactory-test-nats -p 127.0.0.1:14222:4222 nats:2.11.4-alpine -js
NATS_TEST_URL=nats://127.0.0.1:14222 go test -race ./internal/worker -run TestJetStream -v
# Тест удаляет свои streams: не направляйте его на рабочий broker.
docker rm -f appfactory-test-nats
# Опционально: настоящие ChatGPT-запуски start + resume. Core и Worker должны работать.
python3 scripts/smoke.py
# Две зависимые задачи, приёмка, разные threads и перезапуск Core:
python3 scripts/task-smoke.py --restart-core
# Manager создаёт задачи через MCP → executor submit → manager review:
python3 scripts/mcp-smoke.py
python3 scripts/smoke.py --mcp
# Вопрос агенту, inbox владельца и постоянный диалог:
python3 scripts/messaging-smoke.py --restart-core
```

Конфигурации: [docs/agent-configuration.md](docs/agent-configuration.md),
[docs/skills.md](docs/skills.md), [docs/policies.md](docs/policies.md).

```sh
./bin/factory agents get demo
./bin/factory agents authorize demo factory.release.publish
./bin/factory skills list demo
./bin/factory skills read demo openspec references/conventions.md
```

Пример агента: [configs/agents/demo.yaml](configs/agents/demo.yaml).
Архитектура: [docs/architecture.md](docs/architecture.md).
Протокол и ограничения: [docs/runtime-protocol.md](docs/runtime-protocol.md).
Разработка: [docs/development.md](docs/development.md).

Проекты, задачи, приёмка и восстановление: [docs/task-lifecycle.md](docs/task-lifecycle.md).
Результаты проверки: [docs/task-engine-verification.md](docs/task-engine-verification.md).
Factory MCP, токены запуска и инструменты: [docs/factory-mcp.md](docs/factory-mcp.md).
Сообщения, вопросы, inbox и команды владельца: [docs/messaging.md](docs/messaging.md).

Согласования точных действий и решения владельца: [docs/approvals.md](docs/approvals.md).
Утверждение версий артефактов и ожидание задач: [docs/artifact-approvals.md](docs/artifact-approvals.md).

## Границы этапа

Фабрика поддерживает прямые запуски и задачи через API/CLI и Factory MCP; сообщения, вопросы владельцу, inbox и согласования точных MCP-действий.
Версионные утверждения артефактов реализованы отдельно от приёмки задач.
Отчёты и Decision Registry сохраняются в Postgres; UI относится к следующим этапам.
Policies и grants проверяются при каждом вызове; одобрение не отменяет deny. Неизвестные YAML-поля отклоняются.
`doctor` проверяет доступность Core, его Postgres (`"database": "postgres 17.x"`) и соединение Core с NATS; доступность Worker
подтверждается сквозным запуском. `codex-worker` обрабатывает по одному run на процесс, нода — по одному на slot;
Core допускает не больше `execution.max_concurrent_runs` активных run'ов на агента; workspace проекта занимает один run за раз.
Агенты с `workspace.mode: worktree` работают в отдельном git worktree на задачу (ветка `af/<task>`; в конце run'а
незакоммиченная работа запечатывается в `refs/factory/wip/<task>`; перед стартом — проверка на расхождение;
`factory-node workspace reset`; janitor удаляет worktree закрытых задач). Задачи одного проекта идут параллельно
на ноде с `worktrees.enabled`; на ноде без worktree и в v1 — по одному.
Полный MVP из разделов 1–26 ещё не реализован.

Сбой run'а (ошибка runtime, таймаут, зависание, потеря Worker/ноды) повторяется автоматически: задача
возвращается в READY через 1, 5, затем 15 минут и продолжает в том же workspace и сессии. Три попытки
подряд без прогресса или 30 run'ов задачи за день — эскалация менеджеру (иначе владельцу) с ответами
`retry`/`cancel`; задача остаётся READY за паркой эскалации (статуса FAILED у задач больше нет; статус run'а — его
фаза QUEUED → CLAIMED → RUNNING → (CANCELLING) → ENDED, а как он закончился — только outcome: потерянный run или
run без итогового ответа runtime — `lost`). Run, потерянный на замолчавшей ноде, держит свою
задачу, workspace и сессию, пока нода не подтвердит, что его процесса нет; остальные задачи проекта идут.
Дневной бюджет считается по часовому поясу компании (`factory time-zone Europe/Berlin`, по умолчанию UTC).
Задачи с `"side_effects": true` или агенты с правилом политики `type: recovery, mode: inspect` (публикация,
деплой) не повторяются: неясный исход паркует только эту задачу эскалацией `inspect` (подробности:
docs/task-lifecycle.md). Ответить на неё можно и так:

```sh
./bin/factory runs resolve RUN_ID retry 'Workspace inspected; describe findings here'
```

`continue` — эффект случился, продолжить, `retry` — повторить, `cancel` — отменить задачу. Для потерянного run'а
без проверки (нода не вернётся) `runs resolve` освобождает его с этим ответом.

Persistent significant-action reports, the versioned Decision Registry, project context and paginated feed are available via API/CLI/MCP. See [reports and decisions](docs/reports-decisions.md).

OpenSpec в хранилище Core: [API/CLI/MCP, материализация на узлах и проверки](docs/openspec.md). Документы хранятся в Core, узлы кладут их в worktree; задачи привязываются к проверенным active changes.

Автономность менеджера: категория `technical` делегирована политикой; `manager-autonomous` работает без обращений к owner. Отказ владельца создаёт сохраняемое поручение автору предложения пересмотреть план. [Полномочия и ограничения](docs/reports-decisions.md#delegated-autonomy-and-owner-rejection), [сквозная проверка](docs/decision-autonomy-verification.md).

## Консоль владельца, ожидания, стоимость и Telegram

```sh
./bin/factory status | watch [PROJECT] [--attention] | log TASK | show TASK      # --json для скриптов
./bin/factory answer MSG OPTION|TEXT
./bin/factory comment TASK 'текст' [--urgent]     # ввод владельца; --urgent останавливает текущий run
./bin/factory run|retry TASK                       # отпустить задачу сейчас
./bin/factory pause|resume --all|--project P|--agent A|--quota-key K|--node N [REASON]
./bin/factory stop [REASON] / start                # аварийная остановка компании и её снятие
./bin/factory pending                              # вопросы и эскалации для владельца (GET /pending/count)
./bin/factory costs [--task T|--agent A] [--by task|agent|day|model|--day|--model] [--since 24h]
./bin/factory telegram bind                        # одноразовый код: отправьте боту /start КОД
```

Агент ждёт внешний мир только таймером: `factory_exit_wait {reason, wake_at|after, deadline}` (минимум 15 минут,
срок не дальше 14 дней, подряд не дальше первого срока, не больше `max_waits`), затем эскалация менеджеру. Каждый
закончившийся run получает строку стоимости (`run_costs`; цены — `configs/prices.yaml`). Telegram
(`FACTORY_TELEGRAM_TOKEN`, `FACTORY_TELEGRAM_PROJECT`, `FACTORY_TELEGRAM_DIGEST_AT`; свободный текст идёт как
`factory conversations say` — `owner.default_report` или `@name`)
присылает только то, что требует внимания, напоминания и дневную сводку. Подробности:
[docs/task-lifecycle.md](docs/task-lifecycle.md#owner-controls), [docs/messaging.md](docs/messaging.md#telegram),
[docs/reports-decisions.md](docs/reports-decisions.md#event-feed-and-costs).

## Терминальный UI владельца

```sh
./bin/factory ui                        # полноэкранный UI против Core (FACTORY_URL, FACTORY_TOKEN)
./bin/factory ui --demo                 # демо-компания без Core
./bin/factory ui --record state.json    # снимок состояния Core в fixture; --fixture state.json — просмотр без Core
./bin/factory ui --notify bell          # desktop (уведомление macOS, по умолчанию) | bell | off
```

Дашборд (дерево агентов `reports_to` с live-статусами и таймерами, задачи выбранного агента, статистика run'а с
шкалой контекста и sparkline'ами, общий поток активности, мини-карта доски), Kanban-доска с карточками и
действиями владельца (pause/resume/retry/run/cancel/reassign/reprioritize/comment/direct с подтверждением),
live-транскрипт run'а (`GET /runs/{id}/events/stream`), swimlanes «кто что кому передал», лента и inbox; строка
ответа на самый старый вопрос/согласование (`Y`/`N`/`A`), палитра команд `ctrl+k`, мышь. Горячие клавиши — `?`.
Вкладка `6 Chat` — переписка с прямыми подчинёнными владельца (`POST /messages`, `@name`, «… is replying», непрочитанные);
`t` на run'е — полный транскрипт сессии (рассуждения, каждый вызов инструмента с полным входом и результатом, из событий
Core или локального файла сессии Claude/Codex на машине узла, с редактированием секретов); счётчики ошибок инструментов
`✗3` на дереве, карточках и run'ах; `o` открывает PR задачи, `v` — предпросмотр артефакта перед согласованием; `n` на
доске — форма новой задачи; новые вопросы, согласования и эскалации — уведомлением macOS (`--notify`).
Подробности и снимки экранов: [docs/owner-ui.md](docs/owner-ui.md).
