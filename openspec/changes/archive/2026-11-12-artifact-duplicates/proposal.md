Status: accepted 2026-11-06 by the owner; implemented 2026-11-07. The cleanup of the game project's existing duplicates (2.3) was closed 2026-11-11 by the owner: replaced by artifact-retention.

## Why

The company library of the game project held 57 artifacts on 2026-11-06, most of them redundant, nearly all from one
task (2d5baba4, concept sheets of 4 classes, artist-2d):

- each of 4 review rounds re-uploaded all 4 sheets and a ~57 MB zip, although the rumbler and mender sheets were
  byte-identical from v1 to v4; icons M2-D9 v1 and v2 were uploaded two minutes apart in one run;
- the zip of v4 holds 99 files (73 PNG: orthographic views, line-ups, poses, contact sheet, in-game screenshots), yet
  only its 4 `*_sheet.png` are in the Library's Images; the other 69 images are visible only inside the archive;
- every new version raised a new owner approval while the older ones (icons v1, v2; concepts v3) stayed open in the
  Inbox, although the agent waits only for the latest.

## What Changes

- **No duplicate versions.** Registering an artifact version whose content hash equals the artifact's latest version
  does not create a new version: Core answers with the existing one (the agent is told it was unchanged). Identical
  files under different artifacts are stored once (blobs are already content-addressed) and shown once in the Library
  with the places they are used.
- **Images from archives in the Library.** When a zip (or tar) artifact is registered, Core lists its images and shows
  them in Library → Images as part of that archive version (name, size, preview, "from character_concepts_v4.zip"),
  without unpacking them into separate artifacts; size and count limits apply, and archives are checked for unsafe
  paths before listing.
- **Superseded approvals close themselves.** When a newer version of an artifact is submitted for approval, the open
  approval requests of its older versions are closed as "superseded by vN" and leave the Inbox; the agent waiting for
  them is told. Approving an older version still works if someone does it on purpose before the close.
- **Library shows versions as one item.** An artifact with several versions is one card with its version history,
  not one card per version.

## Impact

Core artifact registration (hash check), archive listing (zip and tar readers, limits), approval lifecycle
(superseded), Library and Inbox in the web, agent tool answers. Spec: `artifact-approvals`. Related:
`approval-delegation`.

## Implementation (2026-11-07)

- `factory_artifact_upload` / `factory_artifact_register` of content equal to the artifact's latest version answer
  with that version and `"unchanged": true`; no new version, no new approval. (Submissions keep registering the
  versions they name, so their references stay valid.)
- Uploaded zip and tar (`.tar`, `.tar.gz`, `.tgz`) versions list their images (png, jpg, webp, gif, svg; up to 1,000;
  archives up to 512 MiB; absolute, `..` and `__MACOSX` paths skipped); `GET .../artifacts/{id}/{version}/entry?path=`
  serves one, and the Library shows them as images "from pack.zip v4".
- A new version's approval request closes the older versions' open requests as `SUPERSEDED` (the agent gets the
  answer "superseded by vN"); they leave the Inbox.
- The Library shows one card per artifact (the newest version, "v4 · 4 versions") with the older versions on it.

2.3 listed to the owner (2026-11-07): of 130 versions in the game project, 2 contents are stored more than once —
gnome-arena-rumbler-concept-sheet v1–v4 and gnome-arena-mender-concept-sheet v1–v4 are byte-identical (6 redundant
versions, 12.7 MB). Removing the redundant versions waits for the owner's yes; new uploads no longer duplicate.
