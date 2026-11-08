## 1. Attachments on messages

- [ ] 1.1 Message and decision model: `attachments` (uploaded files with caption) and `materials` (artifact version or
      report attachment references); storage, API, forward keeps them; tests.
- [ ] 1.2 MCP tools (send, ask, forward, decision proposal) accept them; the node uploads worktree files like report
      attachments (redacted, `MaxUpload`); `visual_without_attachment` warning for image or video paths without
      attachments; tests.

## 2. Showing them

- [ ] 2.1 Inbox card, chat message and decision card: inline image thumbnails with captions, side by side for several,
      viewer on click; other kinds as material rows; artifact approvals show the version's content.
- [ ] 2.2 Preview renditions for large images; originals downloadable.
- [ ] 2.3 Telegram: images sent with the question as an album.

## 3. Agents

- [ ] 3.1 Tool descriptions and default manager, QA and artist prompts: visual questions and review requests attach the
      images.

## 4. Verify

- [ ] 4.1 Replay VP-2b on a sandbox: the producer's question shows classic | v1 | v2 side by side in the inbox, the chat
      and Telegram; the owner answers without opening GitHub.
