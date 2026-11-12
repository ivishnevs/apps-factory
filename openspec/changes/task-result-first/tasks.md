## 1. Result first

- [x] 1.1 Materials API: `result` (artifacts named by the last accepted submission or done exit) and the artifact id
      and version on each material; tests.
      Done: `GET /tasks/{id}/exits` lists the artifacts a submit or done exit named; `factory_exit_done` takes
      `artifacts` (registered with the run like a submission's); materials carry `artifact_id` and `result`, the result
      first in the order named (`internal/web/materials.go`, `result_test.go`, `core/exits_test.go`).
- [x] 1.2 Task page: the result leads (first image or document of the result, the rest under it); without a result
      the newest material with content; never the kind order.
      Done: `web/src/components/app/materials/task-result.ts` (`layout`), `task-materials.tsx`.
- [x] 1.3 "Earlier versions" fold for non-result materials, grouped by artifact, with approvals and board comments;
      rows name artifact and version.
      Done: folded `<details>`, groups by artifact (report files by report), the approval badge and its reason on
      each row, row titles "name · artifact version".

## 2. Docs and verify

- [x] 2.1 `docs/owner-ui.md`. Also `docs/explicit-exits-contract.md` (done exit `artifacts`).
- [x] 2.2 Replay task 2509ee3e with fakes: the final gallery leads; samples v1 and v2 with their approvals are under
      "Earlier versions"; the storyboard is there too.
      Done as `TestTaskResultFirst` / `TestTaskResultFromDone` (fixture client): the gallery is the only result and
      first, samples v1/v2 with frames apart by version, approvals with the board's comment, storyboard not the result.
      The web split itself has no test runner in `web/`; it is checked by type-check and build.
