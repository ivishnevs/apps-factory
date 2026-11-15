Status: accepted 2026-11-15. Proposed the same day after the owner asked why an agent's question never reached them;
the owner's decisions and the discussion are recorded below.

## Why

On 2026-11-15 a developer's question (an Android integration check blocked by a host tool) went up the chain:
developer → product-manager → ceo. The ceo's manager is the owner, so the ceo forwarded it. In one model turn it made
two calls: `factory_question_forward` with only a `note`, and `factory_exit_done` ("forwarded the question to the
board"). Core refused the forward with `brief_required: title, ask, after, recommendation` (a question that reaches
people needs a brief) and nothing was sent. Half a second later the exit, already planned, arrived and Core accepted it:
the run ended. The ceo saw the refusal only afterwards and wrote "I'll forward it again with a brief on my next run",
but its triggers were already handled, so there was no next run. The question stayed PENDING addressed to the ceo, the
owner never saw it, and the developer's task and its parent stayed blocked, showing "needs you" on the board with
nothing in the inbox.

Three gaps, each enough to lose a question:

1. **The tool does not say up front that a brief is needed.** `factory_question_forward` reads "forward … to your
   manager, with a note"; `brief` is optional in the schema because the same tool is used between agents. Whether a
   brief is required is known only when Core refuses.
2. **Core accepts an exit while a call of the same run was refused and not fixed.** An exit means "my work here is
   done"; with a refused forward it is not. Models batch calls in one turn, so prompts cannot prevent this.
3. **A question addressed to an agent stops waking it once its run handled the trigger,** whether or not the agent
   answered or forwarded it. Any run that ends without acting on the question (this exit, a crash, a timeout) strands
   it.

The third is the guarantee; the first two make the failure rare and visible.

## What Changes

- **A pending question keeps its recipient awake.** A question addressed to an agent that is still PENDING (not
  answered, forwarded, withdrawn or expired) keeps a trigger on the recipient's task or conversation: a run that ends
  without acting on it does not mark it handled, and the recipient runs again (with the usual retry backoff and the
  question-chain limits). The brief of the next run names the question and, when there was one, the refusal of the
  last attempt (e.g. `brief_required` with the missing fields).
- **No exit past a refused call the agent has not resolved.** Core records, per run, refused calls of the tools that
  send or forward to someone (`factory_question_forward`, `factory_owner_ask`, `factory_message_ask`,
  `factory_message_send`, approval requests) when the refusal is one the agent can fix (`brief_required`, a too-long or
  invalid field). Until a later call with the same `request_id` succeeds, an exit of that run (`done`, `handoff`,
  `submit`, `wait`) is refused with `unresolved_refusal`, naming the call, its refusal and what to do: fix and call
  again with the same `request_id`. Refusals that are decisions, not mistakes (`question_limit`, a closed task, missing
  authority), do not hold the exit. An `ask` or `block` exit is still accepted (the agent hands the problem up).
- **An exit after a decision refusal is checked by the agent's manager.** When a run had a refusal that is a decision
  (`question_limit`, a closed task, missing authority) and the agent then exits `done`, `handoff`, `submit` or `wait`,
  Core accepts the exit, marks it on the task ("the question to <agent> was refused by <reason>; the agent exited
  without asking its manager") and sends the agent's manager a non-blocking notice with the task, the refused call and
  the exit. A manager agent runs on it and may reopen the task, answer what the agent wanted to ask, or leave it; when
  the manager is a person the notice is an attention item in the feed, not an inbox question.
- **The brief is required in the schema when the call reaches people.** For an agent whose manager is a person (owner
  or member), the people-facing tools list `brief` as required and their description starts with it; for other agents
  the schema stays as it is and Core keeps refusing when an item would reach people without one.

Out of scope: the content of briefs (people-topics), and the question-chain limits (question-chain-limit).

## The incident in detail (2026-11-15, advent-gift)

1. 14:38–14:54: a host watchdog left from the previous orchestrator killed the developer's headless emulator four
   times and stopped a Gradle daemon (it does not see Factory runs). Fixed on the host the same day; not part of this
   change.
2. 15:33: the developer stopped the integration check (`b22ec987`) and asked how to continue (three options). The
   question went to product-manager, who judged it the owner's infrastructure and forwarded it to the ceo with a
   recommendation. The task showed "waiting for an answer from ceo".
3. 15:34:40: the ceo woke. Its manager is the owner. In one model turn (one message, two tool uses) it called
   `factory_question_forward` with only a `note` and `factory_exit_done` ("forwarded the question to the board").
4. 15:34:47.3: Core refused the forward: `brief_required: title, ask, after, recommendation`. Nothing was sent.
5. 15:34:47.8: the exit, planned before the refusal came back, was accepted; the run ended. The ceo's last text said
   the question had not reached the owner and that it would forward it with a brief on its next run.
6. The ceo's triggers were handled, so it never ran again. The question stayed PENDING with the ceo; the owner's inbox
   had nothing; the board showed the task and its parent as blocked and "needs you".

Why the ceo sent no brief cannot be read from the run (its reasoning is redacted). The tool's own wording asks for "a
note"; the brief rule is in a long shared tail of the description; and a note alone is enough when an item joins a
topic people already have open, so the agent may have expected that. Why it exited before seeing the refusal: models
batch calls in one turn; this is not a race in Core (the calls ran in order and each answer was right on its own), and
prompts cannot reliably prevent it, so Core must.

## What was discussed

- The owner first assumed a brief is always required. It is required only when an item reaches people; the same tools
  are used between agents, where it is not. Making it required in the schema for agents reporting to a person helps,
  but an agent can still omit it, so Core must not depend on it: hence the exit hold and, as the guarantee, pending
  questions that keep waking their recipient.
- Holding every exit after any refusal would trap agents: after `question_limit` Core tells the agent to decide within
  its authority, and a hold would refuse the `done` of an agent that did exactly that. So decision refusals do not
  hold the exit.
- But an agent that cannot decide may give up and exit `done` anyway. Core cannot tell the two apart. A mark on the task
  is seen reliably only by a reviewer, and tasks without review would go unchecked, so the agent's manager is notified
  and checks it.

## Owner decisions (2026-11-15)

1. `ask` and `block` exits stay allowed with an unresolved fixable refusal: the agent may hand the problem up instead of
   fixing it, and it stays visible.
2. The exit hold covers only fixable refusals (missing brief, too-long or invalid field). Decision refusals
   (`question_limit`, closed task, missing authority) do not hold the exit; an exit after one is marked on the task and
   sent to the agent's manager as a non-blocking notice (an attention feed item when the manager is a person).
