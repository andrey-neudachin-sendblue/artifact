# Edit-link release verification

Runtime commit: `e1b93166fc3df4cdbae5c24cf6e6c196515cab21`.
Deployment: `gg4eos7rjtitzvbl3vkzaulg`, completed 2026-10-29 UTC.

The Go race suite and vet passed. The disposable local browser suite passed all scenarios listed by `scripts/collaboration-e2e.mjs`. That suite uses no production database or owner credential. Its binary replacement, stale draft, sandbox, metadata and historical-version checks remain local proof.

The public HTTPS browser check used existing test artifact `Y5aC9zac`. A view-only visitor saw no editing controls and anonymous suggestion submission returned 401. A keyed visitor saved source as version 2, followed the latest-version link, and submitted feedback visible in the inbox. The original version remained byte-identical. Mobile layout fit the viewport. The same key failed against another artifact. Rotation disabled the old link and the replacement link opened successfully.

A second public HTTPS check used only the per-artifact edit key, with no owner credential: two simultaneous suggestions received separate IDs; the scoped agent read both, uploaded a proposed revision, fetched its before/after preview, accepted it as version 3, and dismissed the temporary second suggestion. Version 2 remained byte-identical. The browser test suggestion remains pending.

An existing formerly informative artifact (`xm4v0CNU`) also accepted a newly issued scoped key. The original demo (`M9DElAyJ`) retained suggestion 2 pending and suggestion 3 addressed. The upgrade preserved every one of the 14 pre-upgrade blob hashes, passed SQLite integrity_check, and retained the original data volume and `artifact-origin` network alias.

Backup: Workshop `/root/artifact-before-edit-keys.f6x6k93r`.
Rollback source: `andrey-neudachin-sendblue/artifact`, branch `feat/artifact-suggestions`, commit `50537f944b3721e9f72328674206fa061c742e1f`. The key table is additive. Rolling back would restore anonymous suggestion submission, so roll forward where possible.

No owner credential or per-artifact edit key is included in this record. The global shared skill and Studio CLI are updated; the old CLI binary is retained for rollback. Publication remains a user-directed action; possession of an edit key grants technical access but does not itself instruct an agent to publish.
