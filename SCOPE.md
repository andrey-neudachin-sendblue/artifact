# Artifact edit links

Mode: spec-locked. This replaces the informative/collaborative access split.
Base: deployment/feat/artifact-suggestions at 50537f944b3721e9f72328674206fa061c742e1f.
Worktree: artifact-edit-links; branch: codex/artifact-edit-links.

| Item | Status | Evidence |
| --- | --- | --- |
| Every artifact, including existing uploads, supports a per-artifact edit key | locally tested | Go permission tests and local browser E2E |
| Regular links cannot submit feedback or mutate content | locally tested | Go permission tests and local browser E2E |
| Edit links support suggestions and publishing revisions without the global owner token | locally tested | Local browser E2E and source review |
| Browser editor supports source editing and file replacement, with stale-save protection | locally tested | Local browser E2E and source review |
| Owner can issue edit links and revoke old links by rotating keys | locally tested | Local browser E2E and source review |
| CLI and skill document view/edit links, replacing the old type split | locally tested | Local browser E2E and source review |
| End-to-end checks cover valid/missing/wrong/revoked/cross-artifact keys and preserved versions | locally tested | Local browser E2E and source review |
| Checkpoint committed and pushed; live rollout and verification | in progress | Local tests passed; rollout pending |

Existing artifact contents, versions, suggestion statuses and public URLs must survive upgrade.
Keys authorize only their artifact; upload, deletion, listing and key management remain owner-only.
