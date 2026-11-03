## Decisions
Require explicit access allow for factory.decision.decide.CATEGORY in both captured and current policy. Default allow and escalation instructions confer no authority. Persist actual decider, category, authority rules and both configuration hashes. Keep owner recording protected. Atomically supersede only a policy decision of the same category with an explicit expected ID/version. Pending owner review, owner rejection and an active owner-approved decision prevent autonomous takeover of the topic. After rejection, alternatives for that topic go through owner review.

Owner rejection atomically creates one durable review_result message to the non-owner proposal author. The existing scheduler waits for the writer slot and starts a separate communication run. The recipient revises plans, proposes alternatives or blocks affected unfinished work with reasons. Completed effects are not automatically rolled back. Missing recipients use existing owner escalation. UNKNOWN quarantine remains unchanged.

## Limitations
Topic matching is canonical textual matching, not semantic equivalence. Agent task revision is model behavior; durable notification and authority checks are enforced. Factory permissions constrain gateway calls, not arbitrary host shell execution by trusted native Codex.
