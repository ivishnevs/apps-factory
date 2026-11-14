## 1. Chain limit

- [x] 1.1 Count a chain per D1 (agent pair, task or conversation, 24 h, since the owner's last message on the task;
      people and Factory excluded); refuse the ninth with `question_limit` and a hint; the owner is never refused.
- [x] 1.2 Core `reply` answers an `ExitRefusal` with its status and body (message tools get the reason and hint).
- [x] 1.3 A question escalated by the old clarification limit stays answerable by its recipient.
- [x] 1.4 Tests: nine questions between a pair refused; review rounds across two days pass; an owner message restarts
      the count; questions to the owner and approval requests do not count; an exit ask refused with 409; an old
      limit-escalated question answered by its recipient.

## 2. Docs

- [x] 2.1 docs/messaging-verification.md and the factory_message_send / factory_exit_ask descriptions mention the limit.
