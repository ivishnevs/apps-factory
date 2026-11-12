Status: proposed 2026-11-12 by the owner ("why didn't I see the final screenshots in the task as the result?");
implemented 2026-11-12. Implementation decisions: `factory_exit_done` gained an optional `artifacts` argument (before,
only submit could name a result); the result is the last submit or done exit that names any artifacts (a done exit
naming none keeps the submission's); pull requests stay visible next to the result, not under "Earlier versions";
without a result only an artifact's older versions fold away.

## Why

Seen on 2026-11-12 in tvorog, task `2509ee3e` (Tracely iOS 1.2: App Store screenshots), Launch Creative. The task
produced 19 materials:
- PR #152;
- the final gallery `gallery-preview-1.2.png` (`screenshots-gallery@ios-1.2-final-v1`), which the agent attached to its
  final submission as the result;
- two approval samples (v1, v2), each with a preview, a full-res zip and six frames named `01-hero.png` …
  `06-variety.png`;
- the storyboard.

The finished task's page leads with one material chosen by kind (`task-materials.tsx`: document, then page, PDF, image,
…), so the **storyboard** became the big card and the final gallery was one row among 18, next to six `01-hero.png`
rows of older samples. The owner could not tell the result from the drafts and thought the board's last comment ("the
first screenshot without the callout") had been ignored, though the final set had it.

## What Changes

- **The result leads.** A finished task's lead card is what the agent attached to its last accepted submission or done
  exit (the artifacts it named as the result); with several, the first image or document of them, the others right
  under it. Without attached artifacts, the newest material with content leads, never the kind order.
- **Drafts fold away.** Materials that are not part of the result (approval samples, earlier versions of an artifact,
  superseded files) go under "Earlier versions" (folded), grouped by artifact, newest first, each with its approval
  and the board's comment when there is one.
- **Same names stay apart.** Rows show the artifact and version with a file ("01-hero.png · sample v2"), so frames of
  different versions are distinguishable.
- Materials API: each material says whether it belongs to the task's result (`result: true`, from the last submission
  or done exit) and which artifact and version it comes from.

## Impact

- Core / web BFF: materials of a task carry `result` and the artifact id and version.
- Web: `task-materials.tsx` lead card and an "Earlier versions" fold; the Library keeps its full list.
- Docs: `docs/owner-ui.md` (task materials). Related: `inbox-visual-previews`.
