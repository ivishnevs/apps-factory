Status: draft 2026-11-15, for the owner's review. Raised by the owner from the tvorog Unsealed onboarding the same day
("three questions about the same problem — isn't this a design error?").

## Why

On 2026-11-15 one stuck task in tvorog (c66e839e, "Audit and onboard Unsealed", review failing at setup) reached the
owner as three different cards for one cause:

1. **Inbox, as a topic:** "ceo: tasks stopped after failed runs / How should they continue? / Try again · Cancel".
   The title names the agent and a generic group. Nothing in it says which task or what went wrong.
2. **Task page, under "Waiting for your answer":** the same message, rendered raw: "3 attempts in a row without
   progress; last run setup_failed: … Answer with one of: retry, cancel." It has no link to the Inbox topic. The owner
   could not tell that the two cards were the same question, and reported it as missing from the Inbox.
3. **Inbox, under "Reviews for you":** the task offered as the owner's review (Accept · Send back), although its
   reviewer was the CEO agent. The web derives this lane itself (`IN_REVIEW && clearer.who === "owner"`). The task's
   park on the escalation made its clearer "owner", so the lane mistook "waits for your answer" for "waits for your
   review".

The class: **Core decides what is asked of a person in one place (people topics, `people-topics`), but half of the
surfaces don't use it.** They derive their own "needs you" items from raw data: messages, task verdicts, clearers.
Each derivation is a little different, so one cause shows up as several items with different texts, and the owner
cannot tell them apart.

What derives its own items today:
- The Inbox reviews lane (from `clearer`). This differs from Core's own `ownerReviewsQuery`, which excludes parked
  tasks and tasks with a reviewer.
- The task page's open items (raw messages and approvals by task, no topic dedup, no topic link).
- Home "Needs you" and the "for you" stat (raw inbox plus `needsOwner(clearer)`).
- The board's "you" view and the project counts.
- The task headline ("Escalated to you", "Waiting for your review", from `clearer`).
- The task's review buttons (`IN_REVIEW && !reviewer`, a third rule).
- Telegram, which sends a raw `⚠ …` line for `task.parked.*` rows alongside the topic item for the same cause.

Core itself never creates review items: `PathReview` is defined but never set, so owner reviews live outside topics.
That is why the web had to derive them.

## What Changes

### 1. Core owns the owner's items

- **Everything a person is asked to act on is an item in their people topics.** That covers questions, approvals,
  decisions and escalations (as today), and also **reviews where the person is the reviewer**. Core creates a
  `review` item when a task enters review with a person as its reviewer (the existing `ownerReviewsQuery` rule) and
  settles it when the review ends.
- **Core exposes each task's open items.** The task view carries `person_items`: for each open item of the task, its
  topic, kind, the person it waits for and its actions. "This task needs you" means exactly "this task has an open item
  that waits for you".
- **Alerts stay alerts.** Idle, no ready node and the other alerts carry no answer and are not people items. They keep
  their own lane from Core's `/alerts`. A task that has an open person item is not also alerted for the same cause.

### 2. Every surface renders Core's items and derives nothing

- **Inbox:** topics from Core, as today. The reviews lane is Core's review items (the `clearer` rule is removed). The
  raw decide/read lists keep only what is outside topics, as today.
- **Task page:** "Waiting for your answer" renders the task's `person_items` with the same card as the Inbox (brief,
  actions) and a link "Open in Inbox" to `#topic-<id>`. An answer given here is the same answer as in the Inbox.
- **Home, board, project counts, nav badge:** "for you" counts and lists are the tasks with `person_items` waiting for
  the viewer, and the badge stays equal to the Inbox (`inbox-badge-parity`).
- **Task headline and review buttons:** "Waiting for your review" and Accept / Send back appear only when the task's
  open item is a review that waits for the viewer. An escalated task shows "Waiting for your answer: <item title>".
- **Telegram:** a cause that has a topic item is sent once, as that item. The raw `⚠` line is sent only for attention
  rows with no item (alerts, node rows).
- The web's `clearer`-based helpers (`needsOwner`, the reviews filter, humanize's owner cases) are removed rather than
  kept beside the new field.

### 3. One cause, one item, in people words

- **An escalation's title names the task and the cause.** For example "Audit and onboard Unsealed: review can't
  start", not "ceo: tasks stopped after failed runs". The ask and the answers' effects stay. The group key (agent and
  reason) still groups repeated escalations of one agent into one topic, and each item then names its task.
- **No technical text for people on any surface.** The raw escalation body ("Escalation … on task …", "Answer with
  one of: …") stays for agents and logs. People surfaces show the brief, including the task page, which today shows
  the raw body.
- **Escalation templates are complete.** Every escalation reason has its situation, group and answer-effect texts,
  checked by a test. Today `event_wait_deadline` is looked up as `event_deadline`, `wait_cycle` has no entry, and the
  `wake` answer has no effect text.

Out of scope: why the task got stuck and who should fix it. That is `contracts-at-the-boundary`, which sends such a
failure back to the implementer instead of to the owner at all.

## What was discussed

- **Fix the three symptoms one by one** (filter the reviews lane by reviewer, add a link on the task page, retitle
  the escalation). Rejected as the whole fix: the next derived surface would drift the same way. The rule is that Core
  computes the items and surfaces render them. The three fixes fall out of it.
- **Make alerts people items too.** Not now: alerts have no answer. A card with no action would add noise to the
  Inbox, and the alert lane already comes from Core.
- **No migration.** Open topics stay as they are. Review items are created for tasks that enter review after the
  change, and tasks already in the owner's review get their item on the next sweep.

## Impact

- Core: review items in people topics, `person_items` on the task view, escalation title and templates, Telegram
  dedup.
- Web: Inbox reviews lane, task page open items, Home, board, project counts, headline and review buttons render
  `person_items`; `clearer`-based owner derivations removed.
- TUI: its Inbox and task panes read the same items (it already reads topics).
- Specs: owner-web, messaging, review-policy, event-feed.
