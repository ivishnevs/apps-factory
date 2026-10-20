# Согласования версий артефактов

Артефакт — ссылка `{id, version, uri}` в пределах проекта. В реестре также сохраняются
задача и запуск, создавшие версию, автор и время. Для существующих ID/версии URI
неизменяем: другая ссылка требует новой версии. Регистрация и task submission не
утверждают артефакт; приёмка задачи также не выдаёт утверждение.

## Производитель: designer, developer или другой агент

1. Зарегистрировать версию через `factory_artifact_register`:

```json
{"artifact":{"id":"landing-mockup","version":"v2","uri":"git:COMMIT:design/landing.svg"},"request_id":"register-mockup-v2"}
```

2. Прикрепить её к структурированному `factory_task_submit` и/или запросить утверждение:

```json
{"artifact_id":"landing-mockup","artifact_version":"v2","reason":"Review the revised layout","blocking":false,"request_id":"approve-mockup-v2"}
```

Второй JSON — аргументы `factory_artifact_request_approval`. Он создаёт постоянный
вопрос в inbox с `approval_id` и точной ссылкой версии. Обычно производитель завершает
задачу, оставляя `blocking: false`: ожидание требуется следующей задаче, а не дизайнеру.
`blocking: true` поддерживает существующий механизм вопроса/продолжения текущей задачи:
после запроса нужно завершить turn, не опрашивать inbox внутри него.

`factory_artifact_register` привязан к текущей задаче/run; caller не передаёт чужую
идентичность. Ссылки в structured submission также регистрируются атомарно. Операция
submission с артефактами требует прав и на `factory.task.submit`, и на
`factory.artifact.register`; скрытая регистрация не позволяет обойти policy deny.
Запрос согласования требует права написать owner и не требует согласования самого
себя. Deny для этого инструмента действует.

`factory_artifact_list` перечисляет реестр проекта; `factory_artifact_get` принимает
`artifact_id` и `artifact_version`, возвращает запись и последнее решение. Все агенты
проекта могут читать утверждение артефакта через `factory_approval_get`, если policy
разрешает инструмент. Grants конкретных действий остаются в области автора/задачи.

## Зависимая задача

`tasks create/update` и `factory_task_create/update` принимают:

```json
{
  "required_artifacts": [
    {"id":"landing-mockup","version":"v2","uri":"git:COMMIT:design/landing.svg"}
  ]
}
```

Можно объявить ссылку до регистрации версии. `ready` с отсутствующим, ожидающим,
отклонённым, отозванным или просроченным утверждением переводит задачу в BLOCKED
с причиной `artifact:`. Она не создаёт run и не занимает Worker/writer. Когда все
требуемые версии утверждены, Core возвращает задачу в READY автоматически; затем
применяются обычные зависимости задач, доступность агента и writer.

Это два независимых условия: `dependencies` требует DONE, а `required_artifacts`
требует утверждения конкретной версии. Утверждение артефакта не принимает задачу,
и DONE производителя не утверждает артефакт. Без `required_artifacts` никакого
обязательного этапа дизайна/утверждения нет. Названия designer/QA не зашиты в Core.

В run сохраняются `approved_artifacts`: точная ссылка, `approval_id` и версия решения.
Эти данные добавляются к prompt. Core проверяет их внутри транзакции dispatch и ещё
раз при claim, непосредственно перед стартом runtime. Отзыв отменяет ещё не начавшийся
QUEUED run и возвращает его задачу к ожиданию. Новое утверждение требует нового run
с новым снимком входных данных. Уже начавшийся RUNNING run сохраняет разрешённый на
момент старта снимок; отзыв не откатывает его побочные эффекты и не прерывает его.

Ручные блокировки, парки эскалаций и CANCELLED/DONE не снимаются автоматически.
Обновление зависимой задачи на новую версию возможно только по обычным правилам
редактирования, с актуальной версией task и без активного/неизвестного run.
Одобрение v1 не разрешает v2; задачи, явно закреплённые за v1, продолжают использовать v1.

## Решение владельца и CLI

Используется общий inbox и интерфейс [согласований](approvals.md):

```sh
./bin/factory inbox list PROJECT
./bin/factory approvals get APPROVAL_ID
./bin/factory approvals decide APPROVAL_ID decision.json
```

```json
{"version":1,"action":"approve","reason":"Reviewed this exact version","request_id":"owner-layout-v2"}
```

`reject` оставляет потребителей заблокированными; `revoke` запрещает новые старты по
утверждению. Ответ через обычный inbox answer не утверждает артефакт. Решение и
пробуждение/остановка очереди сохраняются атомарно. По одной версии может быть только
один активный запрос/утверждение. После отказа, отзыва или истечения срока можно
создать новый явный запрос; прежние решения остаются в истории. Изменённое содержимое
должно получить новую версию, а не повторное утверждение старой.

Срок ожидания решения по умолчанию 24 часа, максимум 7 дней. Истечение срока закрывает
вопрос как EXPIRED, без разрешения. Принятое утверждение артефакта имеет `expires_at: 0`
и сохраняется до отзыва; срок вопроса не отменяет уже утверждённый контент.

Команды для реестра:

```sh
./bin/factory artifacts list PROJECT
./bin/factory artifacts get PROJECT ARTIFACT_ID VERSION
./bin/factory artifacts register PROJECT register.json
./bin/factory artifacts request-approval PROJECT request.json
```

Для owner register JSON содержит `artifact`, `task_id`, необязательный `run_id` и
`request_id`. Для owner request-approval — `artifact_id`, `artifact_version`, `reason`,
`request_id`, необязательные `agent` и `timeout_seconds`. Автор запроса по умолчанию —
автор регистрации или assignee исходной задачи; owner может явно выбрать настроенного
агента. Owner API не создаёт blocking-запрос от имени активного run.

HTTP: `GET/POST /projects/PROJECT/artifacts`,
`GET /projects/PROJECT/artifacts/ID/VERSION`,
`POST /projects/PROJECT/artifacts/approval`; все требуют owner token.
События: `factory.events.artifact.registered` и существующие `approval.*`,
`task.artifact_waiting`. История и outbox сохраняются в SQLite schema v6.

## Границы и проверка

Core хранит и утверждает ссылки, не загружает и не проверяет содержимое URI.
Используйте commit/content-addressed URI (например, SHA256), а не изменяемый URL
без версии. Ответственность за новую версию при изменении содержимого остаётся у
доверенного агента; Factory MCP не является файловой изоляцией.

Предыдущие task_artifacts и submissions сохраняются при миграции. Историческую ссылку
нужно явно зарегистрировать перед новым утверждением, если она ещё не попала в реестр;
миграция не выбирает за владельца URI из потенциально неоднозначных старых ссылок.
Регистрация также проверяет старые ссылки: если они расходятся по URI для одной
версии в проекте, используйте новую версию. Исторический URI не перезаписывается.

`make check` проверяет версии, policy, независимость приёмки, ожидание, перезапуск,
отказ/отзыв/сроки и карантин. `scripts/artifact-approvals-smoke.py --restart-core`
проверяет реальным Codex v1 approve → QA, v2 reject → ожидание, v3 approve → QA.
Нужны Core/NATS и native Worker. Результаты — [verification](artifact-approvals-verification.md).
