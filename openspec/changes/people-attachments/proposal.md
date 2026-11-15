Status: accepted 2026-11-15. Proposed the same day by the owner ("make it possible to attach images in the chat with
the CEO, and in task comments too"); the owner accepted every recommendation of D1–D6. Nothing implemented yet.

## Why

Agents can show people images: since `inbox-visual-previews` a message, a forwarded question or a decision proposal
carries attachments (worktree files uploaded by the node) and materials, and the inbox, the chat and Telegram show
them inline. The other direction is missing. A person writing to an agent can send only text:

- **Chat** (`POST /topics/{topic}/messages`, the lead chat and every agent conversation) takes `body` only.
- **Task comments** (`POST /tasks/{id}/comments`, `tasks.Comment`) take `text` only; so do reopen, ask-lead and
  directives.
- **Telegram** replies are read as text; a photo sent to the bot is dropped.

So a person who wants to show the CEO a competitor's screen, a bug on their phone or a sketch has to describe it in
words or put the file somewhere and paste a link the agent cannot open. The agent cannot see what the person sees.

## What Changes

- **People attach images where they write.** The web chat composer and the task comment box get an attach button,
  paste and drag-and-drop. The files are uploaded to Core first (`POST /uploads`, the person's session, the company's
  blob store and quota), then the message or comment names them. The same works for every agent conversation, not only
  the lead's, for reopen and ask-lead (which are comments), and for any person who may write there (owner or member).
- **The same attachment model as the agents'.** A person's message or comment stores `attachments` exactly like an
  agent's message (`messaging.Attachment`: name, caption, sha256, size; Core renders the preview), so every surface
  that already shows an agent's images shows a person's the same way: chat bubble, task comments, inbox, TUI (as a
  file line with a link to the web). Not redacted: the person chose to share it.
- **The agent sees the image.** Before every run the node downloads from Core the attachments of the run's task (its
  comments and the messages filed under it) or, for a conversation run, of the messages in the brief's history, and
  places them in the working directory under `.factory/inputs/` (the existing manifest the node already downloads
  with the run's lease and caches by digest). Core keeps the original; the working directory holds a copy per run, so
  a retry or a later run on another node gets the files again. The brief lists each one next to its text:
  `[image] .factory/inputs/msg-…/1/screen.png — caption`. The runtime reads it with its own image tool (Claude Code reads images; Codex views local
  images). No new agent tool. Conversation runs and task runs alike; a project without a repository still has a
  working directory to materialize into.
- **Agents can pass it on.** A materialized attachment is a file of the worktree, so an agent forwarding the person's
  message attaches it with the existing `{path, caption}` path; forwarding a question already keeps its attachments.
- **Telegram (D3).** A photo (or an image sent as a file) the person sends to the company bot as a reply is attached
  to the message it answers, with the photo's caption as the text.
- **CLI.** `factory tasks comment ID TEXT --attach FILE…` and the same flag on the owner chat command.

Not in scope: attaching arbitrary documents (D1), attaching from the TUI (it shows attachments; uploading from a
terminal is the CLI's job), editing or deleting a sent attachment, images in task descriptions at creation.

## Decisions

- **D1 File types.** (a) images only: PNG, JPEG, WebP, GIF, HEIC converted to JPEG on upload; (b) images and PDF;
  (c) any file. **Recommendation: (a)** — what was asked, previews and every runtime handle it; PDF and other files
  are a follow-up once there is a case.
- **D2 Limits.** Per file the blob limit (64 MB) is far above what a screenshot needs. **Recommendation:** at most 10
  files per message or comment, 20 MB per file, counted against the company's blob quota; images over 2560 px on
  the long side get a preview (as agents' attachments do) and the original stays downloadable and is what the agent
  gets.
- **D3 Telegram.** (a) in this change; (b) later. **Recommendation: (a)** — the owner answers from the phone most of
  the time, and the reply routing (`telegram-reply-routing`) already knows which message a reply answers.
- **D4 What the agent gets.** (a) before every run the node downloads the task's or conversation's attachments from
  Core into the working directory, plus a brief line (above); (b) a tool to download attachments on demand.
  **Recommendation: (a)** — the agent sees it without deciding to look, and it reuses the materialization the node
  already has; (b) would add a tool and a round trip for every image. A run that dies and moves to another node loses
  nothing: Core holds the original and the next node downloads it again.
- **D5 Uploads that are never sent.** An upload not named by a message or comment within 24 h is deleted.
  **Recommendation:** yes, by the existing blob cleanup.
- **D6 Retention.** Attachments live as long as their message or comment and follow `run-data-retention` /
  `artifact-retention` rules for messages; they are not Library materials. **Recommendation:** yes; a person who wants
  an image kept as a project material asks an agent to register it as an artifact.

### Owner decisions 2026-11-15

All six recommendations accepted: images only (D1); 10 files, 20 MB each (D2); Telegram photos in this change (D3);
the node downloads the task's or conversation's attachments before every run (D4, clarified after the owner asked
what happens when a run moves to another node); unsent uploads deleted after 24 h (D5); attachments live with their
message or comment, not in the Library (D6).

## Impact

- Core: `POST /uploads` (person session, company quota), `attachments` on the owner/member message, comment, reopen
  and ask-lead requests; validation that each named upload belongs to the company and is unsent; comment storage
  gains attachments; run manifest includes the attachments of the run's triggers; brief lines.
- Node: manifest paths for attachments (`.factory/inputs/msg-<id>/<n>/<name>`, `cmt-<id>` for comments); the
  existing path check accepts them.
- Web: attach button, paste, drag-and-drop, upload progress and removal before sending, in the chat composer and the
  task comment box; comments show attachments.
- Telegram: photos and image documents in replies.
- CLI: `--attach`.
- TUI: attachments of comments shown like messages' (a file line).
- Docs: messaging, owner comments, runtime protocol (manifest).
