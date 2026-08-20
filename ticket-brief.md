# Ticket Brief — CXH-2281

- **Title:** baton-lucidchart: four SCIM account-lifecycle defects surfaced during CXH-1488 validation
- **URL:** https://linear.app/ductone/issue/CXH-2281
- **Team / Priority / State:** Connector Horizon · High · In Progress
- **Parent:** CXH-1488 (blocks CXH-1488) · Labels: `Connector: Lucidchart`, `Bug`
- **Filed by:** QA (Manuel Traversaro Sasia) from CXH-1488 validation
- **Reference PR (prior art, not assumed mergeable):** #59 — `[CXH-2281] fix: SCIM account-lifecycle defects and test-server fidelity`

## INTENT (acceptance criteria, restated verbatim from the ticket)

> Validation of `baton-lucidchart` v0.1.3 against a test-server rebuilt from Lucid's published OpenAPI surfaced four defects on the SCIM account-lifecycle paths added by CXH-1488. All four are reproducible; a fix branch and PR accompany this issue.
>
> The sandbox Lucid account is Team/Free tier and the SCIM surface is Enterprise-only, so these were exercised against a local mock. The mock replicates Lucid's documented contract rather than the connector's assumptions — where the two disagree it follows the documentation, which is how findings 1 and 2 surfaced at all.
>
> ### Finding 1 — delete retry aborts on Lucid's documented 403
> `pkg/connector/users.go:260`
> `Delete` resolves the leaving user's email before a content transfer and continues only when the lookup fails with not-found:
> ```go
> if err != nil && !client.IsNotFoundError(err) {
> ```
> Lucid's `GET /v1/users/{id}` documents 403 — never 404 — for a user that does not exist: "if the user does not belong to the authenticated account or if the user does not exist" (https://lucid.readme.io/reference/getuser). The guard therefore fails and the operation aborts before the SCIM delete is attempted.
>
> Observed on 2026-08-18, deleting an already-removed user with `--lucid-content-transfer-user-email` set:
> ```
> rpc error: code = PermissionDenied desc = baton-lucidchart: resolve email for content transfer (user 105): baton-lucidchart: get user 105: rpc error: code = PermissionDenied desc = 403 Forbidden
> ```
> The mock log shows no `DELETE /scim/v2/Users/lucid-105` after the 403.
>
> ### Finding 2 — SCIM delete 409 surfaces as AlreadyExists
> `pkg/connector/users.go:272`
> `Delete` maps only 404 to success; every other status is wrapped verbatim. Lucid returns 409 for a user that cannot be deleted — "e.g., account owner or default document owner" (https://lucid.readme.io/reference/deleteuser) — which `uhttp` maps to `codes.AlreadyExists`, the same code the SDK uses for idempotent no-ops.
>
> Observed 2026-08-18 against a user the mock marks protected:
> ```
> rpc error: code = AlreadyExists desc = baton-lucidchart: delete user 101: rpc error: code = AlreadyExists desc = 409 Conflict
> ```
> The user remains present in the next sync. The reason Lucid gave is not carried in the message.
>
> ### Finding 3 — user status is pinned to ENABLED
> `pkg/connector/users.go:209`
> ```go
> status := v2.UserTrait_Status_STATUS_ENABLED
> ```
> The status is a constant, and `client.User` (`pkg/connector/client/models.go:9-16`) has no field for Lucid's `enabled`, documented as "Whether the user can authenticate to Lucid. Corresponds to the SCIM active attribute" (https://lucid.readme.io/reference/getuser).
>
> Observed 2026-08-18: after `disable_user` returned `success: true` and the mock recorded `enabled=false`, a re-sync emitted `RESOURCE_STATUS_ENABLED` for that user. The same function drops `User.Roles` from the emitted profile, so a role change made through `update_user` is also absent from the bundle.
>
> ### Finding 4 — the username field never decodes
> `pkg/connector/client/models.go:14`
> ```go
> Usernames string   `json:"usernames"`
> ```
> Lucid's REST `User` model defines `username` (singular). The plural key is not emitted by Lucid, so the field unmarshals empty on every user and the profile key written at `pkg/connector/users.go:216` is always `""`.
>
> ### Reproduction
> 1. 2026-08-18 — build the connector from `origin/main` (79714b3) and from the fix branch.
> 2. Start the bundled test-server with `-protected-users 101`.
> 3. Run a sync against the mock and read back user 105 (`enabled=false` in the mock).
> 4. Delete user 105, then delete it again with `--lucid-content-transfer-user-email` set.
> 5. Delete user 101.
>
> | Check | origin/main | fix branch |
> | -- | -- | -- |
> | Status of a disabled user | `RESOURCE_STATUS_ENABLED` | `RESOURCE_STATUS_DISABLED` |
> | `username` in profile | absent | populated |
> | Delete retry after 403 | exit 1, `PermissionDenied` | exit 0 |
> | Delete a protected user (409) | `AlreadyExists` | `FailedPrecondition` |
>
> The harness re-runs deterministically; a second run of the fix-branch case produced identical output.
>
> ### Impact
> With a content-transfer recipient configured — the configuration that preserves a leaving user's documents — a retried delete of an already-removed user returns `PermissionDenied` and never reaches the SCIM delete, so retries cannot converge. A 409 on a protected user returns the gRPC code used for idempotent success while the user remains present. Deactivation and role changes made through the SCIM actions are absent from the synced bundle. The `usernames` profile key is empty on every user.

## Acceptance checklist (derived from the A/B table — the ticket's pass/fail gate)

1. **Status of a disabled user** → `RESOURCE_STATUS_DISABLED` (was `ENABLED`).
2. **`username` in profile** → populated (was absent, because the model read `usernames`).
3. **Delete retry after 403** → exit 0 / idempotent success (was exit 1, `PermissionDenied`).
4. **Delete a protected user (409)** → `FailedPrecondition` carrying Lucid's reason (was `AlreadyExists`).
5. Test-server is rewritten from Lucid's **published OpenAPI** (not the connector's assumptions); it is the repro harness the sibling tickets CXH-2282–2285 depend on, so it must be correct.

## Deltas vs. reference PR #59 (verified, not blindly ported)

PR #59 implements the four fixes + the test-server rewrite, but its CI review left **1 blocking issue + 3 suggestions** open. This branch ports PR #59's sound approach and closes those:

- **[BLOCKING] `Enabled bool` cannot distinguish "field absent" from "explicitly false".** PR #59 defaults `userResource` status to `DISABLED`, so any `GET /users` payload missing `enabled` would sync **every** user as disabled — a mass-deactivation footgun. Fix here: `Enabled *bool`, default `STATUS_ENABLED`, flip to `DISABLED` only when `Enabled != nil && !*Enabled`. Confirmed against Lucid docs that `GET /users` (listusers) *does* return `enabled` + `username` (same User schema as getuser), so the field is present on the sync path — the pointer is defence-in-depth, at no cost.
- **[suggestion] `IsConflictError` duplicated `IsAlreadyExistsError` verbatim.** Collapsed to a single `IsConflictError` predicate (the 409/delete-refusal name), dropped the callerless `IsAlreadyExistsError`.
- **[suggestion] test-server `-page-size 0`/negative was unclamped** → empty page + repeating `Link` next token = infinite pagination for the connector under test. Clamp `if size <= 0 { size = lucidPageSize }`.
- **[suggestion] `ci.yaml` comment "Any non-empty bearer is accepted" is now false** and `start-test-server` never passed `-scim-token`. Updated the comment and wired `-scim-token` (from `BATON_LUCID_SCIM_TOKEN`) into the composite action so REST/SCIM tokens cannot silently diverge.
- **Excluded PR #59's unrelated churn:** its `go.mod`/`go.sum`/`vendor/**`/`.versions.yaml` bumps (PR #59 was cut from `79714b3`; this branch is based on the newer `origin/main` `3420066` which already carries the dependency/Go bumps) and its deletion of `tickets/CXH-1488/*.md`.

## Not in scope (tracked separately, per PR #59)
- CXH-2282 — SCIM wire contract (Content-Type / PatchOp schemas URN divergence; needs a live Enterprise tenant). Test-server models it behind `-strict-scim-doc`, off by default.
- CXH-2283 — ungated capabilities · CXH-2284 — remaining SCIM surface gaps · CXH-2285 — folder/document grant idempotency.
