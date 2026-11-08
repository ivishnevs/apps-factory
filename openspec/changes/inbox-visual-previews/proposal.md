Status: proposed 2026-11-08 by the owner ("what I have to approve must be visible right away in the inbox and in the
producer's message, not somewhere I have to go to"). Nothing implemented.

## Why

Visual approvals reach the owner as text. On 2026-11-08 the producer asked for a verdict on Storm Towers v2 (question
`5d068f9f`, VP-2b): "The image is docs/qa/visual-polish/towers/side_by_side_v2.jpg on that branch, with columns
classic | v1 | v2". To answer, the owner had to open PR #218 on GitHub, find the branch and the file. The same holds
for art reviews, screenshot checks and design sign-offs: the thing to judge lives in a worktree or a PR, the question
holds a path.

Factory already has the parts:

- report attachments: a run names files of its worktree, the node uploads them (credentials redacted) into Core's
  blob store (`factory_report_create` `attachments`, core-owned-project-data);
- artifact versions with uploaded content (artifact-approvals);
- a material viewer in the web (task Materials, Library) that shows images, PDFs and documents
  (`internal/web/materials.go`, `web/src/components/app/materials`).

What is missing: messages (questions, review requests, notifications) and decisions cannot carry files, and the inbox
and chat do not show any.

## What Changes

- **Attachments on messages and decisions.** `factory_message_send`, `factory_question_ask` (and forward) and decision
  proposals accept `attachments` (files of the run's worktree, `{path, caption}`, uploaded by the node like report
  attachments) and `materials` (references to existing ones: an artifact version, a report attachment). They are
  stored with the message; a forward keeps them.
- **Visible where the owner answers.** The inbox card, the chat message and the decision card show attached images
  inline (thumbnails in the order given, captions, click opens the existing viewer full size); other kinds show as
  material rows (PDF, document, video link). An approval of an artifact version shows that version's content.
  Telegram sends the images with the question (an album) when the company has Telegram.
- **Comparison layout.** Two or more images with captions render side by side (or as a swipe on mobile), so "classic
  | v1 | v2" needs no composite image.
- **Agents attach by default.** The tool descriptions and the default manager, QA and artist prompts say: a question
  or review request about something visual attaches the images; a path in the text is not enough. When a message to
  the owner names image or video files (`.png`, `.jpg`, `.webp`, `.gif`, `.mp4`) without attachments, the tool answer
  warns `visual_without_attachment` (non-blocking) so the agent can resend with them.
- **Limits.** Size and count per message as for report attachments (`MaxUpload`); images larger than a display size
  get a preview rendition; originals stay downloadable.

Not in scope: editing or annotating images in the inbox, video playback beyond a link, attachments from people.

## Impact

Messaging model and storage (attachments, material references), Factory MCP tools (send, ask, forward, decision
proposal), node upload path (reuse of report attachments), inbox, chat and decision cards (web, TUI shows links),
Telegram delivery, default prompts. Specs: `messaging`. Related: `decision-approval-ux`, `human-review-visibility`,
`artifact-approvals`, `telegram-reply-routing`.
