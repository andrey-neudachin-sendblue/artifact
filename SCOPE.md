# Artifact edit links

Mode: spec-locked. This replaces the informative/collaborative access split.
Base: deployment/feat/artifact-suggestions at 50537f944b3721e9f72328674206fa061c742e1f.
Worktree: artifact-edit-links; branch: codex/artifact-edit-links.

| Item | Status | Evidence |
| --- | --- | --- |
| Every artifact, including existing uploads, supports a per-artifact edit key | end-to-end verified | Local browser E2E and public HTTPS permission checks |
| Regular links cannot submit feedback or mutate content | end-to-end verified | Local browser E2E and public HTTPS permission checks |
| Edit links support suggestions and publishing revisions without the global owner token | end-to-end verified | Live key-only inbox/propose/accept/dismiss and simultaneous suggestion checks |
| Browser editor supports source editing and file replacement, with stale-save protection | end-to-end verified | Local browser source/binary/stale-save checks; live source save and latest-version navigation |
| Owner can issue edit links and revoke old links by rotating keys | end-to-end verified | Local and live browser revocation and replacement-link checks |
| CLI and skill document view/edit links, replacing the old type split | end-to-end verified | Installed CLI used in live tests; shared skill read back |
| End-to-end checks cover valid/missing/wrong/revoked/cross-artifact keys and preserved versions | end-to-end verified | Local browser suite plus live browser/API permission and immutable-source checks |
| Checkpoint committed and pushed; live rollout and verification | end-to-end verified | e1b9316 pushed and deployed; release evidence committed with this ledger |

Existing artifact contents, versions, suggestion statuses and public URLs must survive upgrade.
Keys authorize only their artifact; upload, deletion, listing and key management remain owner-only.

## Validation and release

- `go test ./... -race`, `go vet ./...`, and `git diff --check` passed.
- Local browser E2E passed, including source saves, binary replacement, stale drafts, mobile feedback, iframe isolation, version pinning, and key revocation.
- Live browser test passed on `Y5aC9zac`: missing-key writes rejected; keyed source save and suggestions worked; cross-artifact and revoked keys rejected; replacement key worked. A separate live key-only CLI proposal/accept/dismiss flow passed, including simultaneous suggestions.
- Formerly informative artifact `xm4v0CNU` accepted a scoped key without changing its content or legacy type. Original demo `M9DElAyJ` retained suggestion 2 pending and suggestion 3 addressed.
- Live runtime: `e1b93166fc3df4cdbae5c24cf6e6c196515cab21`, Coolify deployment `gg4eos7rjtitzvbl3vkzaulg`, existing hostname/volume/origin alias preserved.
- Backup: Workshop `/root/artifact-before-edit-keys.f6x6k93r`. All 14 original blobs matched after upgrade and SQLite integrity_check passed.
- CLI installed locally with the prior binary retained as `artifact-before-edit-links`. Shared `SKILL.md`, `REFERENCE.md` and deployment record updated. No edit key or owner token is committed.
- Test evidence is summarized in `docs/edit-links-release.md`. The existing test artifact is now version 3; its test feedback remains available for review.
