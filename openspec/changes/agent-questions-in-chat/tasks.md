## 1. Questions in the lead's chat

- [x] 1.1 Core: a project's open owner questions and approvals with the asking agent and task, for the chat; the
      lead's conversation brief carries them and their answers; tests.
      Core's open list is `GET /inbox?project=` (in the overview): each item names its sender, task and conversation,
      approvals pair by message. The conversation brief of an agent talking with the owner lists the project's other
      agents' open questions to people (asker, task, message id, options) and those people answered since its last
      run with the answer (`teamQuestions`, brief.go). `TestAgentQuestionsInChat`.
- [x] 1.2 Web chat: cards for other agents' open questions and approvals in the owner's chat with the project lead,
      answerable in place; answering closes the Inbox item; tests.
      Items no chat shows go to the chat with the project's lead (team lead, else default report) as the Inbox's own
      cards under the newest message, headed "<agent> asks" and the task; counted in "asks you"
      (`inbox/open-items.tsx`, `chat/thread.tsx`, `chat/use-chats.ts`). The answer is the Inbox's answer action, so
      the item closes everywhere (covered by the Core replay; the web has no test runner: lint, types, build).

## 2. Task page

- [x] 2.1 The task's open question or approval with options and answer box on the task page; "Open in inbox" stays
      as a link. ("Waiting for your answer" under the status, `TaskOpenItems`.)

## 3. Options for listed choices

- [x] 3.1 `factory_owner_ask` and the escalation tool: `options` documented and expected for choices; a hint when a
      question lists choices without options; agent instructions; tests.
      `factory_owner_ask` and `factory_message_ask` take `options` (a single_choice question `choice`);
      `factory_exit_ask` already did. A question listing choices without options is refused with reason
      `choices_without_options` and the hint (`messaging.ListsChoices`); `options: []` sends it as free text. The
      web's "Something else…" sends the person's own words as a free-text answer (Core refuses unlisted options). Factory
      MCP instructions and the tool descriptions ask for options. `TestListsChoices`, `TestAgentQuestionsInChat`.

## 4. Docs and verify

- [x] 4.1 `docs/messaging.md`, `docs/owner-ui.md`.
- [x] 4.2 Replay 2026-11-11 with fakes:
      - App Release asks for the asc profile with four options;
      - the card shows in the tracely CEO chat and on the task page with four buttons;
      - answering "Stashy" in the chat closes the Inbox item and wakes App Release.
      `TestAgentQuestionsInChat` (internal/core): demo (reports to manager) asks the owner; the listed-only version
      is refused, the four-option one is sent; it is open in the project's list the chat and the task page read; the
      manager's conversation brief carries it; the owner's answer "Stashy" closes it, App Release's next run has the
      answer, the manager's next brief shows it answered. The web rendering itself is checked by types and build only.
