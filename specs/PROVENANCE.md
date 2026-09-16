# `specs/openapi.json` — provenance

Built per `.claude/skills/connector/build-openapi-spec.md`. Scope is the connector's whole current
request surface (this is the repo's first spec, so it covers everything, not only what one PR
touched). Nothing here was derived from a struct tag alone without saying so.

Lucid publishes **no machine-readable spec** — their own community answer states the API
definitions are available only as individual reference pages, with a task open to explore
exporting a single OpenAPI file ([community.lucid.co][community]). So every operation below was
settled against a reference page, one at a time.

Accessed date for all external sources: **2026-09-15**.

## Operations

18 operations, across two hosts. 19 client methods map onto them: two pairs collapse because
OpenAPI keys operations by method + path (noted inline).

### REST API — `https://api.lucid.co` (`client.LucidchartApiUrl`, `--base-url`)

| Operation | Method / Path | Client location | Source | Accessed | Evidence |
|---|---|---|---|---|---|
| `refreshAccessToken` | `POST /oauth2/token` | `pkg/connector/connector.go:125` | [reference/createorrefreshaccesstoken][t] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:555`); **request encoding from `golang.org/x/oauth2`, not from docs** |
| `listUsers` | `GET /users` | `pkg/connector/client/query.go:42` | [reference/listusers][lu] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:607`) |
| `createUser` | `POST /users` | `pkg/connector/client/user_management.go:42` | [reference/createuser][cu] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:676`) |
| `getUser` | `GET /v1/users/{userId}` | `pkg/connector/client/query.go:29` | [reference/getuser][gu] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:652`) |
| `transferUserContent` | `POST /v1/transferUserContent` | `pkg/connector/client/user_management.go:135` | [reference/transferusercontent][tuc] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:724`) |
| `listRootFolderContents` | `GET /folders/root/contents` | `pkg/connector/client/query.go:60` | [reference/listrootfoldercontents][lrfc] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:788`, empty array only) |
| `listFolderContents` | `GET /folders/{folderId}/contents` | `pkg/connector/client/query.go:78` | [reference/listfoldercontents][lfc] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:789`, empty array only) |
| `listFolderUserCollaborators` | `GET /folders/{folderId}/shares/users` | `pkg/connector/client/query.go:98` | [reference/listfolderusercollaborators][lfuc] | 2026-09-15 | docs only |
| `getFolderUserCollaborator` | `GET /folders/{folderId}/shares/users/{userId}` | `pkg/connector/client/query.go:129` | [reference/getfolderusercollaborators][gfuc] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:816`, `pkg/connector/collaborator_test.go:137`) |
| `upsertFolderUserCollaborator` | `PUT /folders/{folderId}/shares/users/{userId}` | `pkg/connector/client/mutate.go:14` | [reference/putfolderusercollaborator][pfuc] | 2026-09-15 | docs + fixture (`pkg/connector/collaborator_test.go:156`); **409 is client/fixture evidence only** |
| `deleteFolderUserCollaborator` | `DELETE /folders/{folderId}/shares/users/{userId}` | `pkg/connector/client/mutate.go:41` | [reference/deletefolderusercollaborator][dfuc] | 2026-09-15 | docs + fixture (`pkg/connector/collaborator_test.go:189`) |
| `listDocumentUserCollaborators` | `GET /documents/{documentId}/shares/users` | `pkg/connector/client/query.go:152` | [reference/listdocumentusercollaborators][lduc] | 2026-09-15 | docs only |
| `getDocumentUserCollaborator` | `GET /documents/{documentId}/shares/users/{userId}` | `pkg/connector/client/query.go:185` | [reference/getdocumentusercollaborators][gduc] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:849`); **direct-only property is observed, not documented** |
| `upsertDocumentUserCollaborator` | `PUT /documents/{documentId}/shares/users/{userId}` | `pkg/connector/client/mutate.go:60` | [reference/putdocumentusercollaborators][pduc] | 2026-09-15 | docs + fixture (`pkg/connector/collaborator_test.go:156`); **409 is client/fixture evidence only** |
| `deleteDocumentUserCollaborator` | `DELETE /documents/{documentId}/shares/users/{userId}` | `pkg/connector/client/mutate.go:84` | [reference/deletedocumentusercollaborators][dduc] | 2026-09-15 | docs + fixture (`pkg/connector/collaborator_test.go:189`) |

### SCIM 2.0 — `https://users.lucid.app/scim/v2` (`config.LucidScimUrl`, `--scim-base-url`)

| Operation | Method / Path | Client location | Source | Accessed | Evidence |
|---|---|---|---|---|---|
| `scimGetUser` | `GET /Users/{scimUserId}` | `pkg/connector/client/scim.go:275` | [reference/getuser-1][sgu] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:904`) |
| `scimPatchUser` | `PATCH /Users/{scimUserId}` | `scim.go:247` (`SetUserActive`) + `user_management.go:74` (`UpdateUser`) | [reference/modifyuserpatch][smu] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:929`, `pkg/connector/client/scim_test.go:49`) |
| `scimDeleteUser` | `DELETE /Users/{scimUserId}` | `scim.go:298` (`ScimDeleteUser`) + `scim.go:310` (`ScimDeleteUserContentAccess`) | [reference/deleteuser][sdu] | 2026-09-15 | docs + fixture (`cmd/test-server/main.go:1090`) |

Two client methods share `scimPatchUser` (they differ only in which `Operations` they send) and two
share `scimDeleteUser` (they differ only in the bearer token, which is what routes the call to the
"admin management" vs "content access" integration). Both hit the same host and the same path, so
OpenAPI cannot separate them; the split is documented in each operation's `description`.

**Source tally:** 18 of 18 operations are backed by a vendor reference page. 15 of those pages were
found via a URL already cited inline in this repo (`pkg/connector/client/*.go`, `cmd/test-server/main.go`,
`pkg/connector/actions.go`, `pkg/config/config.go`); 3 (`listfolderusercollaborators`,
`putfolderusercollaborator`, `deletefolderusercollaborator`) had no inline citation and were found by
external search. 0 operations are unsourced. 3 individual *facts* could not be proven from docs and
are marked unproven below.

## Unproven / blockers

Recorded rather than guessed, per the skill's "Do Not" section.

1. **`User.usernames` (plural) — unproven field.** `client.User` declares
   `Usernames string \`json:"usernames"\`` (`pkg/connector/client/models.go:16`) and
   `pkg/connector/users.go:243` copies it into the resource profile. It appears on **no** Lucid
   reference page consulted and in no fixture; the code carries its own `// TODO: probably should
   be removed, "usernames" is not a real Lucid field.` It is modelled as an optional string with
   `x-unproven: true` because the connector reads it — dropping a declared field is not allowed —
   but it is almost certainly dead. Removing it is a Go change and out of scope here.
2. **`PUT .../shares/users/{userId}` 409 — undocumented status.** Lucid documents 400/403 on the
   folder upsert and 403 only on the document upsert (see 4). The connector's grant-idempotency path (CXH-2285) depends on a 409
   whose body may carry the conflicting collaborator record, and the test suite injects exactly
   that. Modelled with the collaborator schema as its body and flagged in the description as
   undocumented. **Unconfirmed against Lucid's docs; confirmed only by this connector's own code
   and fixtures.**
3. **`getDocumentUserCollaborator` direct-only behaviour — observed, not published.** Lucid
   publishes the ancestor-access caveat for *folders* ("A user having access to a folder through
   one of the folder's ancestors will not be shown through this API") and says nothing equivalent
   for documents. The document endpoint's direct-only behaviour was verified empirically against a
   live tenant under CXH-2285 and is recorded in the operation description as observed. If Lucid
   starts reporting inherited access there, the Grant short-circuit's safety argument breaks.
4. **`upsertDocumentUserCollaborator` 400 for the `owner` role — asymmetric, unconfirmed.**
   [reference/putfolderusercollaborator][pfuc] documents a 400 ("Bad Request when trying to add or
   update a Folder User Collaborator to have the \"owner\" role") and states "Collaborators cannot
   be given the role \"owner\"". [reference/putdocumentusercollaborators][pduc] documents **only**
   200/201/403 — no 400, and no mention of the `owner` restriction anywhere on the page
   (re-checked 2026-09-15).

   The asymmetry matters because the document upsert can in fact be asked for `owner`:
   `documentBuilder.Entitlements` (`pkg/connector/document.go:93`) builds document entitlements
   from `client.UserFolderRoles`, which *includes* `owner`, so a `document:<id>:user/owner` grant
   is emittable and would send `{"role":"owner"}`. Both upserts bind the same
   `CollaboratorRoleRequest` → `CollaboratorWriteRole`, and the document page's own published role
   enum omits `owner` too, so the same validation very likely applies — but "likely" is not
   documented. **No 400 was added to `upsertDocumentUserCollaborator` in the spec**; inventing a
   response the vendor page does not publish is exactly what the skill's "Do Not" section forbids.
   Resolve by observing a real `owner` upsert against a live tenant, or by Lucid publishing it.

## Divergences worth knowing (spec records the connector's behaviour, not the doc's)

- **Path versioning is inconsistent in the connector.** Lucid versions the REST API with a
  `Lucid-Api-Version: 1` header ([reference-rest][rr]) and most reference pages *also* render a
  `/v1` path prefix. The client sends the header on every REST request and uses **unversioned**
  paths everywhere except `GET /v1/users/{userId}` and `POST /v1/transferUserContent`, which carry
  the prefix. Paths in the spec are written exactly as the client builds them (skill Step 3), so
  this inconsistency is visible rather than smoothed over. Not a bug on current evidence — the
  unversioned form is what `reference-rest` and the list-collaborator pages show as the live URL —
  but worth a look if a `/v1` 404 ever appears.
- **OAuth2 token request encoding.** Lucid documents `POST /v1/oauth2/token` with an
  `application/json` body. `golang.org/x/oauth2` posts `application/x-www-form-urlencoded` to the
  unversioned `/oauth2/token`, and may send credentials via HTTP Basic instead of body fields. The
  spec records what goes on the wire. `cmd/test-server` accepts both credential forms, which is why
  CI has never caught this.
- **SCIM content negotiation.** Lucid's reference documents `application/json` and a `schemas` value
  of `urn:ietf:params:scim:schemas:core:2.0:User`, where RFC 7644 specifies `application/scim+json`
  and the PatchOp URN. The connector follows Lucid's published form deliberately
  (`pkg/connector/client/scim.go:17-30`); the test-server accepts both and logs a DIVERGENCE.
- **SCIM PATCH 204.** Lucid documents a 200 with the updated user. The client additionally tolerates
  a bodyless 204 (and a non-JSON or undecodable body), reporting the write as applied-but-unconfirmed
  via `ScimUser.IsZero`. The 204 is in the spec, described as client-handled rather than documented.

## Called out separately (the spec cannot hold these)

**Pagination.** Link-header based, so only half of it is expressible. The connector sends an opaque
`pageToken` query parameter (modelled) and reads the next cursor out of the `Link` response header's
`rel="next"` URL (modelled as a response header; `extractPageToken`,
`pkg/connector/client/lucidchart.go:344`). The loop terminates when the response carries no `Link`
header. The cursor relationship between the two is not representable in OpenAPI. The connector never
sends `pageSize`, so Lucid's 200-record default and 200-record cap apply to all five paginated
operations ([reference-rest][rr]).

**Retry, rate limits, errors.** Retry is the SDK's: `uhttp` maps HTTP status onto gRPC codes, and
`pkg/connector/client/helpers.go` is the only place that classifies them (404 → `NotFound`,
403 → `PermissionDenied`, 401 → `Unauthenticated`, 409 → `AlreadyExists`, and
`Unavailable`/`DeadlineExceeded` → retryable). Lucid rate-limits account tokens at 50 req/s and user
tokens at 15 req/s, answering 429 with `Retry-After`; `transferUserContent` has its own 30-per-5-seconds
per-account limit. Only the transfer limit is modelled, because it is the only one Lucid documents
per-endpoint.

**Post-response filtering.** `folderBuilder.List` keeps only `type == "folder"` and
`documentBuilder.List` only `type == "document"` from the same `FolderContent` payload; both drop
items with `isShortcut == true` when `--exclude-shortcuts` is set. The API returns both kinds
together — the split is the connector's.

**Caching.** The two single-collaborator GETs are sent with `uhttp.WithNoCache()` because uhttp's
GET cache (on by default, 1h TTL) is not invalidated by the PUT/DELETE on the same path, and the
read-before-write pre-check must see current state.

**Parity-critical fields.** `User.userId` → user resource ID and ExternalId. `FolderContent.id` →
folder/document resource ID (number for folders, UUID string for documents — never render it with
`%v`). `FolderUserCollaboration.userId` / `DocumentUserCollaboration.userId` → grant principal.
`.role` → entitlement slug. `.created` → grant metadata (omitted when zero). `ScimUser.id` is the
`lucid-`-prefixed SCIM resource ID and is **not** interchangeable with `User.userId`.

**Adjacent observation (not a spec matter).** `client.UserFolderRoles`
(`pkg/connector/client/lucidchart.go:21`) includes `owner`, and folder entitlements are built from
that list — but Lucid documents 400 for a PUT that requests `owner` ("Collaborators cannot be given
the role 'owner'"). A grant against the `owner` entitlement would therefore always fail upstream.
The spec records the enum split (`CollaboratorRole` for reads, `CollaboratorWriteRole` for writes);
whether to stop offering the entitlement is a connector decision outside this spec's scope.

**Declared but unused models.** `client.Folder`, `client.AccountDocument`,
`client.FolderGroupCollaborator` and `client.DocumentShareLink` (`models.go`) are declared but no
client method requests the endpoints that return them. Per the skill ("do not add endpoints the
client never calls"), they are not in the spec.

## Validation

```
npx @redocly/cli lint specs/openapi.json
```

[community]: https://community.lucid.co/lucid-for-developers-6/yaml-swagger-or-oas-for-the-lucidchart-api-9340
[rr]: https://lucid.readme.io/reference/reference-rest
[t]: https://lucid.readme.io/reference/createorrefreshaccesstoken
[lu]: https://lucid.readme.io/reference/listusers
[cu]: https://lucid.readme.io/reference/createuser
[gu]: https://lucid.readme.io/reference/getuser
[tuc]: https://lucid.readme.io/reference/transferusercontent
[lrfc]: https://lucid.readme.io/reference/listrootfoldercontents
[lfc]: https://lucid.readme.io/reference/listfoldercontents
[lfuc]: https://lucid.readme.io/reference/listfolderusercollaborators
[gfuc]: https://lucid.readme.io/reference/getfolderusercollaborators
[pfuc]: https://lucid.readme.io/reference/putfolderusercollaborator
[dfuc]: https://lucid.readme.io/reference/deletefolderusercollaborator
[lduc]: https://lucid.readme.io/reference/listdocumentusercollaborators
[gduc]: https://lucid.readme.io/reference/getdocumentusercollaborators
[pduc]: https://lucid.readme.io/reference/putdocumentusercollaborators
[dduc]: https://lucid.readme.io/reference/deletedocumentusercollaborators
[sgu]: https://lucid.readme.io/reference/getuser-1
[smu]: https://lucid.readme.io/reference/modifyuserpatch
[sdu]: https://lucid.readme.io/reference/deleteuser
