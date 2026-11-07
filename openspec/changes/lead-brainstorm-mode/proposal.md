Status: decided 2026-11-06 by the owner (see "Decision"); implemented 2026-11-07 except the evals (see "Implementation").

## Why

In conversations with the people who run the company (owners and admins) the team lead turns ideas into tasks too
quickly: an idea or a loose wish becomes a backlog of work before the lead has understood it, challenged it or
agreed a plan. The opposite failure is just as bad: a lead that asks about everything wears people out. The owner
wants a lead that thinks with them first, decides on its own what clearly fits the company's direction, and says
plainly what it understood and what it started, so people can stop or correct it in time.

## What Changes (idea, to be designed)

- **Brainstorm first with people.** When an owner or admin writes to the lead with an idea, a goal or a proposal
  (not an explicit "create this task: …" with a full description), the lead does not create tasks yet. It asks the
  questions that matter, proposes options and trade-offs, challenges weak points, and states the plan it would
  follow. Only when the goal, scope and plan are clear does it take the work and decompose it into tasks.
- **Explicit requests go straight to work.** "Create a task: …" with a clear description is done as asked, without
  a discussion.
- **Decide within the company's direction.** Choices that fit the company's mission, the project's goals and
  recorded decisions are the lead's to make; it does not ask about them. Only real product direction, scope,
  money and conflicts with recorded decisions go back to people.
- **Say what it understood and what it started.** Before (or right when) work starts, the lead posts in the chat a
  short summary: how it understood the request, the plan, the decisions it took on itself, and the tasks it is
  creating, so people can see it at a glance and stop or redirect it. A "stop / change this" reply cancels or
  adjusts the tasks it just created.
- **Applies to every team's lead**, whatever its prompt, as Factory behaviour for lead conversations with owners
  and admins (a policy or built-in instructions for the manager role), not only as a prompt of one team.

## Open questions

Answered on 2026-11-06 (see "Decision").

## Impact

Lead instructions (built-in policy for the manager role), conversation briefs, possibly a "plan summary" message
type in the chat and a way to undo just-created tasks; company settings for the mission. Related:
`people-attribution` (the lead knows which person it talks to), `human-review-visibility`.

## Decision (2026-11-06)

- **Direction lives on the project, not the company.** Each project has its goal and direction (a field in the
  project's settings next to its rules); the lead judges "fits the direction" against the project's, plus recorded
  decisions.
- **Instructions only.** Core does not block task creation; the behaviour is the lead's built-in instructions plus
  evals.
- **Explicit "go" for big plans.** A plan of more than about 5 tasks waits for an explicit go; smaller ones start, and
  the lead always posts its summary (what it understood, the plan, decisions it took, tasks created).
- **No interrogation (owner).** Brainstorming is not asking questions on every message. The lead asks only when an
  idea or goal is genuinely unclear and the answer changes what it would do; then once, all questions together, with
  its recommended answer for each so the person can just say "ok". Clear requests, follow-ups, small changes and
  anything decidable within the project's direction and recorded decisions are done without questions. When in
  doubt, the lead acts on its best reading and says what it assumed.

## Implementation (2026-11-07)

- Projects have `direction` (goal and direction), edited on the project page next to the rules; every brief carries
  it with "decide what fits it yourself; ask people only about what does not".
- Conversations with a person carry the built-in lead behaviour: clear requests done as asked; ideas discussed with
  options and a plan; questions only when the answer changes the plan, once, batched, each with a recommended answer;
  decisions within the direction taken without asking; a summary of understanding, plan, own decisions and created
  tasks; more than about 5 tasks wait for an explicit go; stop or change on just-created tasks is honoured.
- Not done: 2.2 (a dedicated plan-summary message in the chat: the summary is an ordinary reply for now) and the evals
  2.3 / 2.5, which need runs of real agents and a scoring harness; to do when the eval harness exists.
