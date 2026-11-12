Status: implemented 2026-11-12 (bug found on tvorog).

## Why

A CEO run on mac2 was lost; its node kept resending the run's last events and Core answered 500 each time:
`invalid byte sequence for encoding "UTF8": 0xd1 0xe2`. Core cut the run's Russian error text at 512 bytes
(`excerpt512`) through a two-byte Cyrillic letter and appended "…", and Postgres refused the string. The run could
never end, so its conversation task stayed held by a lost run ("its runtime may still run") and the CEO could not
answer the owner. Four more places cut text by bytes the same way (node event lines, brief excerpts, node connect
fields, unsaved WIP notes).

## What Changes

- Every byte-limited excerpt in storage ends on a rune boundary (`cutUTF8`), so stored text is always valid UTF-8.

## Impact

- `internal/storage/text.go`, `retry.go`, `node_events.go`, `brief.go`, `node_connects.go`, `workspaces.go`; test
  `TestCutUTF8`.
