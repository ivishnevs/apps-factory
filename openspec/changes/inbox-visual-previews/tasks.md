## 1. Attachments on messages

- [x] 1.1 Message and decision model: `attachments` (uploaded files with caption) and `materials` (artifact version or
      report attachment references); storage, API, forward keeps them; tests.
      `messaging.Attachment`/`MaterialRef` on messages, `decisions.Propose`/`Decision`; Core checks blobs and resolves
      references in the project (`storage/visuals.go`), the forward keeps and appends; retention counts them.
      Tests: `TestVisualMaterialsForwardAndDecisions`, `TestRetentionKeepsMessageAttachments`.
- [x] 1.2 MCP tools (send, ask, forward, decision proposal) accept them; the node uploads worktree files like report
      attachments (redacted, `MaxUpload`); `visual_without_attachment` warning for image or video paths without
      attachments; tests. `factory_message_send`, `factory_message_ask`, `factory_owner_ask`,
      `factory_question_forward`, `factory_decision_propose`; tests `TestVisualVerdictInInbox`, `TestNamesVisuals`.

## 2. Showing them

- [x] 2.1 Inbox card, chat message and decision card: inline image thumbnails with captions, side by side for several,
      viewer on click; other kinds as material rows; artifact approvals show the version's content.
      `web/src/components/app/materials/visuals.tsx` (swipe on a phone); the approval card already previewed the
      version, its question now carries it as a material for the chat; TUI lists them as links (`TestVisualLines`).
- [x] 2.2 Preview renditions for large images; originals downloadable. `internal/preview` (standard library), stored as
      `preview` on the attachment; `TestRender`.
- [x] 2.3 Telegram: images sent with the question as an album. `sendMediaGroup` (`sendPhoto` for one) before the
      question; `TestTelegramQuestionAlbum` against a fake Bot API.

## 3. Agents

- [x] 3.1 Tool descriptions and default manager, QA and artist prompts: visual questions and review requests attach the
      images. `prompts/manager.md`, presets game-producer, product-owner, qa, game-qa, artist-2d, designer.

## 4. Verify

- [ ] 4.1 Replay VP-2b on a sandbox: the producer's question shows classic | v1 | v2 side by side in the inbox, the chat
      and Telegram; the owner answers without opening GitHub.
      Done with fakes: `TestVisualVerdictInInbox` (fake runtime, adapter upload, preview, inbox, chat conversation,
      answer) and `TestTelegramQuestionAlbum` (album in order before the question, fake Bot API). Blocker: the check
      with the real Telegram bot and a look at the live web inbox/chat need a sandbox Core with a bot and the owner;
      not done here.
