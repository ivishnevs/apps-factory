## 1. D1 — approval decisions name who decided

- [x] 1.1 `approval_decided` and `artifact_approved` triggers carry the decider (`approvalDecider`): the owner, or the
      delegate with its own rank; the text says "by <agent> (delegated: <setting>)" (`approvalDecidedBy`); the owner's
      decision is unchanged. `internal/storage/triggers.go`, `approvals.go`.
- [x] 1.2 The answer Core writes to a delegated approval's question is the delegate's (`messaging.Reply.Delegate`,
      never set from a request). `internal/storage/messages.go`.
- [x] 1.3 Tests: `TestDelegatedApprovalAttribution` (reject and approve by a delegate: trigger actor, text,
      `AuthorityInputSince` author and rank, the wait still overridden, the answer's sender, `artifact_approved` on a
      task that requires the version); `TestApprovalDecisionWakesRequester` keeps the owner's case.
- [x] 1.4 Checked the other "by the owner" texts: the project creation note (`createdNote`) names an action
      approval, which is never delegated (only artifact approvals are); the TUI and Telegram defaults are the owner's
      own decisions. Nothing else to change.

## 2. D2 — artifacts named in messages to people go with them

- [x] 2.1 Core finds registered artifacts named in the text (`artifactMentions`: URI, id@version, "id version",
      bare id with `-`/`_`/digit = latest) and adds them as materials (`mentionedMaterials`) to messages to the owner
      (`SendMessage`), forwarded questions (`ForwardQuestion`, body and note) and decision proposals (`ProposeDecision`,
      text and `artifacts`); explicit materials first, no duplicates, within `MaxVisuals`.
      `internal/storage/artifact_links.go`. Tests: `TestArtifactMentions`, `TestArtifactLinks`.
- [x] 2.2 Web: "In the Library" links under a message's visuals and "Open in the Library" in the viewer opened from
      a message; the Library takes `?artifact=` (API `GET /api/materials?artifact=`), shows that card and opens it.
      Test: `TestPageArtifact` (Go side); `tsc --noEmit` and eslint on the changed files.
- [x] 2.3 TUI: the inbox lists a message's artifacts with a `v` hint; `v` previews them one per press (no y/n from a
      message's preview). Test: `TestMessageArtifactPreview`.

## 3. Verify

- [ ] 3.1 Look at the live inbox (web and TUI) with a lead's question that names an artifact: the item shows the
      artifact and its Library link opens the card. Done with fakes only (no live runs); the look at the live web is
      the owner's.
