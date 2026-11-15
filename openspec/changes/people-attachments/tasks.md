## 0. Decisions

- [x] 0.1 Owner accepts or changes D1–D6 in proposal.md; update the specs to the chosen options (2026-11-15: all
      recommendations accepted).

## 1. Core

- [x] 1.1 `POST /uploads`: a person's upload (session or owner token) into the company blob store, type and size
      checks (D1, D2), quota; returns sha256, size, name, preview; unsent uploads deleted after 24 h (D5); tests.
- [x] 1.2 `attachments` on person messages (`POST /topics/{topic}/messages`, inbox answers), comments, reopen and
      ask-lead: each names an upload of this company; stored as `messaging.Attachment`; at most 10; tests for owner,
      member, a foreign upload refused, an already used upload reusable.
- [x] 1.3 Comment model and reads carry attachments; comment feed items name them.
- [x] 1.4 Run manifest: attachments of the run's task (its comments and messages filed under it) or, for a
      conversation run, of the messages in the brief's history, as inputs under
      `.factory/inputs/msg-<id>/<n>/<name>` / `cmt-<id>/<n>/<name>`; `RunBlob` serves them with the run's lease;
      tests for a task run, a conversation run, and a retry on another node that gets the files again.
- [x] 1.5 Brief: each attachment listed with its path and caption next to its message or comment; tests.
      Done 2026-11-15 (1.1–1.5, 2.1): `POST /uploads`, `uploads` table and daily sweep, person attachments on
      messages (`/topics/{id}/messages`, `/messages`, ask-lead), comments and reopen; manifest and brief lines; a
      conversation run gets the images of the latest 20 people's messages to its agent; open board comments of other
      tasks bring theirs. Inbox answers carry attachments with Telegram (3.3).

## 2. Node

- [x] 2.1 Manifest path check accepts the attachment paths; materialization test with a message attachment.

## 3. Surfaces

- [x] 3.1 Web chat composer: attach button, paste, drag-and-drop, thumbnails with remove, upload progress; send
      disabled while uploading; works for every conversation.
- [x] 3.2 Web task comments (and reopen, ask-lead): the same attach control; comments show attachments with
      `MessageVisuals`.
- [x] 3.3 Telegram (D3): a photo or image document sent as a reply is uploaded and attached to the answer, caption as
      text; tests with a fake Bot API.
- [x] 3.4 CLI: `factory tasks comment ID TEXT --attach FILE…` and the chat command's `--attach`.
- [x] 3.5 TUI: comment attachments shown as file lines like message attachments.

## 4. Tests and docs

- [x] 4.1 Web tsc, lint and component tests with fakes (no live runs); Go tests.
- [x] 4.2 Docs: messaging, owner comments, runtime protocol (manifest inputs).
      Done 2026-11-15: surfaces 3.1–3.5 (web chat and comments, Telegram photos and image files, CLI `--attach`,
      TUI file lines); answers carry images (web inbox answer UI not changed: the chat covers it). The web has no
      component test runner: tsc and eslint; Go tests cover Core, the TUI client, Telegram with a fake Bot API.
