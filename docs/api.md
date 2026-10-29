# HTTP API

All `/api/*` endpoints require `Authorization: Bearer $ARTIFACT_TOKEN`. Scoped edit keys also authorize info, update and collaboration APIs; upload, list, deletion and key management require the owner token.
All artifact-serving routes are public and unauthenticated.

A `401` is returned before the request body is read, so a bad token never
consumes an upload.

---

## `POST /api/upload`

Creates a new artifact. Responses include `url` (view-only) and `edit_url` (a scoped capability). Legacy `type` is retained for compatibility and no longer controls edit access. The type
is fixed at creation. Both raw and multipart uploads accept the type in query
or form parameters. Unknown types return `400 bad_request`.

Accepts the file two ways.

**Raw body** — preferred for agents and `curl`:

```console
$ curl -sS -X POST \
    -H "Authorization: Bearer $ARTIFACT_TOKEN" \
    --data-binary @report.html \
    "$ARTIFACT_ENDPOINT/api/upload?filename=report.html"
```

**Multipart** (`multipart/form-data`): the file in the `file` field, with
optional `filename` and `slug` fields.

| Parameter | Where | Required | Notes |
| --- | --- | --- | --- |
| `filename` | query or form | Yes | Used for content-type resolution and `Content-Disposition`. In a multipart request, falls back to the file part's own filename. |
| `slug` | query or form | No | See the URL scheme in the README. |

An explicit `filename` field or query parameter wins over the multipart part's
filename. The filename is sanitized before storage: `filepath.Base`, control
characters and path separators stripped, `.`/`..`/empty rejected, capped at 255
bytes. It is stored in SQLite only and never appears in a filesystem path.

Response `201 Created`:

```json
{
  "id": "aB3xK9qZ",
  "slug": null,
  "url": "https://artifact.example.com/aB3xK9qZ",
  "raw_url": "https://artifact.example.com/aB3xK9qZ/raw",
  "version": 1,
  "version_url": "https://artifact.example.com/aB3xK9qZ@1",
  "filename": "report.html",
  "content_type": "text/html; charset=utf-8",
  "size": 20481,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "created_at": "2026-07-26T16:02:11Z",
  "updated_at": "2026-07-26T16:02:11Z"
}
```

When the artifact has a slug, `url`, `raw_url`, and `version_url` are built
from the slug. The ID form always resolves as well.

A `201` means the bytes are fsynced to disk and the metadata row is committed.

---

## `PUT /api/artifacts/{id}`

Replaces content, creating version `n+1`. `{id}` may be an ID or a slug. Same
request forms as upload; a `slug` parameter is ignored, because a slug belongs
to the artifact rather than to a version.

Response `200 OK` with the same body shape, `version` incremented and
`updated_at` refreshed. The artifact's URL is unchanged and prior `@n` URLs
return their original bytes forever.

---

## `DELETE /api/artifacts/{id}`

Deletes the artifact, all versions, and all blobs. Response `204 No Content`
with no body. Deleting an already-deleted artifact is `404`.

The artifact's row is retained as a tombstone so its ID and slug stay retired.

---

## `GET /api/artifacts`

Lists artifacts, newest-updated first.

| Parameter | Default | Notes |
| --- | --- | --- |
| `limit` | 50 | Values above 200 are capped at 200. Non-numeric or `< 1` is `400`. |
| `cursor` | — | Opaque cursor from the previous response. |

```json
{
  "artifacts": [ { "id": "aB3xK9qZ", "…": "same shape as upload" } ],
  "next_cursor": "eyJ1IjoiMjAyNi0wNy0yNlQxNjowMjoxMVoiLCJpIjoiYUIzeEs5cVoifQ"
}
```

`next_cursor` is `null` on the last page.

---

## `GET /api/artifacts/{id}`

Metadata for one artifact, including full version history. `{id}` may be an ID
or a slug.

```json
{
  "id": "aB3xK9qZ",
  "slug": null,
  "url": "https://artifact.example.com/aB3xK9qZ",
  "latest_version": 2,
  "created_at": "2026-07-26T16:02:11Z",
  "updated_at": "2026-07-26T17:14:03Z",
  "versions": [
    { "n": 1, "filename": "report.html", "content_type": "text/html; charset=utf-8",
      "size": 20481, "sha256": "e3b0…", "created_at": "2026-07-26T16:02:11Z",
      "url": "https://artifact.example.com/aB3xK9qZ@1" },
    { "n": 2, "filename": "report.html", "content_type": "text/html; charset=utf-8",
      "size": 21033, "sha256": "a94a…", "created_at": "2026-07-26T17:14:03Z",
      "url": "https://artifact.example.com/aB3xK9qZ@2" }
  ]
}
```

---

## `GET /healthz`

Unauthenticated. Returns `200 {"status":"ok"}` when the database answers a ping
and the data directory is writable; `503 {"status":"unavailable"}` otherwise.

---

## Errors

Every non-2xx API response uses this shape:

```json
{ "error": { "code": "not_found", "message": "artifact not found" } }
```

| Status | `code` | Cause |
| --- | --- | --- |
| `400` | `bad_request` | Missing `filename`, malformed filename, malformed or reserved slug, empty body, bad `limit` or `cursor` |
| `401` | `unauthorized` | Missing, malformed, or incorrect token |
| `404` | `not_found` | No such artifact, version, or filename |
| `409` | `slug_taken` | Slug already in use, including by a deleted artifact |
| `413` | `payload_too_large` | Body exceeds `ARTIFACT_MAX_UPLOAD_SIZE` |
| `500` | `internal` | Unexpected server error; details are logged, not returned |

`code` is stable, so a client can branch on it rather than parsing prose.

Public artifact routes are not part of the JSON API and answer with plain text
on failure.

---

## Public routes

See the URL scheme table in the [README](../README.md). Every artifact
response carries `X-Content-Type-Options: nosniff` and a `Content-Disposition`
header (`inline` for everything previewable, `attachment` for the
`application/octet-stream` floor), encoded per RFC 6266.

## Suggestions and revision previews

Every artifact supports keyed editing. Upload responses return an `edit_url`
once; the key is stored only as a digest, never returned by info, list, raw,
review or instruction routes. `POST /api/artifacts/{id}/edit-link` (owner only)
issues another key and returns `view_url` and `edit_url`. Add `?rotate=true` to
revoke all old keys. Keys are supplied as `Authorization: Bearer <key>`, never
in URL queries. Edit links carry them in a browser fragment (`/edit#key=...`).
View-only requests cannot submit suggestions or mutate content.

`PUT /api/artifacts/{id}?filename=...&base_version=N` accepts an edit key or
owner token. An edit key requires the base version; stale saves return 409.
The check and version commit are atomic. Raw source/version URLs stay public.
The browser loads no authenticated data until the key is verified and keeps it
outside the sandboxed artifact document. `/review` remains a view-only shell
unless supplied a valid edit key in the fragment, just like `/edit`.

| Method | Path | Authorization | Result |
| --- | --- | --- | --- |
| POST | `/api/artifacts/{id}/suggestions` | Edit key or owner, JSON only | 201, suggestion |
| GET | `/api/artifacts/{id}/suggestions` | Edit key or owner | 200, inbox |
| POST | `/api/artifacts/{id}/suggestions/{suggestion}/dismiss` | Edit key or owner | 204 |
| POST | `/api/artifacts/{id}/suggestions/{suggestion}/proposals?filename=report.html&base_version=2` | Edit key or owner | 201, proposal with preview_url |
| POST | `/api/artifacts/{id}/proposals/{proposal}/accept` | Edit key or owner | 200, ordinary artifact response |
| GET | `/{ref}/review` | Public | Review page, pinned to loaded version |
| GET | `/{ref}@{n}/review` | Public | Review a particular version |
| GET | `/{ref}/proposals/{proposal}` | Public | Before/after candidate preview |

`id` accepts an ID or a slug. Proposal upload bodies use the same raw or
multipart forms and size limits as uploads. A candidate does not create a
published version. Acceptance verifies the current version and pending status
under the store write lock, then publishes and resolves feedback in one SQLite
transaction. Repeated or stale accepts return `409 revision_conflict`.

Authenticated submission requires an explicit positive version (never “latest”):

```json
{"version":2,"message":"Make the explanation clearer","author":"Alex","quote":"selected words","location":"Chart title"}
```

Only `version` and a nonblank `message` are required. The body is capped at
16 KiB; UTF-8 field byte limits are message 8000, author 120, quote 2000,
location 240. Unknown fields and trailing JSON are rejected. Cross-origin
browser submissions are rejected; no CORS credentials are granted. Names are
self-reported. Rate limits are per artifact: 30 submissions per minute and
1000 pending suggestions. A limit returns `429 suggestion_limit` with
`Retry-After: 60`.

The inbox accepts `all=true`, `limit` (default 50, max 200), and `after`
(a nonnegative suggestion ID). It returns `suggestions`, `latest_version`,
and `next_after` (zero on the final page). Each suggestion includes `id`,
`artifact_id`, `version`, `message`, `author`, `quote`, `location`, `status`,
`created_at`, `version_url` and immutable `raw_url`. Statuses are `pending`,
`addressed`, and `dismissed`. The default filters to pending feedback.

A proposal response includes `id`, `artifact_id`, `suggestion_id`,
`base_version`, `filename`, `content_type`, `size`, `sha256`, `status`,
`created_at`, and `preview_url`. Proposal statuses are `pending` and `accepted`.
An older suggestion can be addressed using a current base version after the
agent has read both versions. No endpoint executes an agent or applies natural
language feedback by itself.

The review shell never receives the owner token. Documents appear in an opaque
sandbox (`allow-scripts` without `allow-same-origin`); a small bridge forwards
selected text only. Quotes and feedback are escaped in the shell. Previews and
feedback belong to the artifact and are deleted with it. Candidate previews
are public to anyone holding their link. Existing databases gain the new tables
at startup using the same idempotent schema initialization as before.

Review and candidate pages embed non-executable `application/json` in
`script#artifact-agent-instructions`. The block is hidden from rendered UI.
Their `rel="describedby"` HTML links and HTTP `Link` headers identify a public
JSON copy at `/{ref}/review/instructions` (also `@n`) or
`/{ref}/proposals/{proposal}/instructions`. Collaborative original and raw artifact responses
also advertise the version-pinned instructions in a `Link` header, without
changing their bytes. The metadata describes recipient submission and the
owner inbox/propose/approval/accept process, identifies source/version and
proposal context, and includes no credentials or recipient-controlled feedback.
It is workflow documentation, never permission to execute tools or publish.
