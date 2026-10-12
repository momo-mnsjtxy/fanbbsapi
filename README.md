# FanBBS Go API

This is a runnable local vertical slice of the approved FanBBS rewrite, independent of the original Java snapshot. HOP informed the shallow, subject-oriented layout and linear request flow; it is not linked, vendored, or copied as a Go framework.

## Included

- Local registration, Argon2id credentials, opaque 15-minute access tokens, rotating 30-day refresh sessions, one-time local recovery codes (displayed once and hashed at rest), password/profile changes, soft deactivation, and server-side revocation
- `recommend`/`recommended`, `latest`, `global`, and authenticated `following` feeds with opaque cursor pagination
- Post create/detail, thread-cursor comments/replies with complete depth-first pages and privacy-safe deleted-ancestor tombstones, and idempotent post/comment creation
- Post/comment edit and soft delete with required `If-Match` versions, immutable owner-readable post revision history, and idempotent comment likes
- Bounded local-filesystem media uploads behind a blob interface, post attachment, avatar upload/reference flow, owner-scoped abandonment, and retryable orphan expiry cleanup
- Transaction-safe, idempotent like/unlike and repost/undo-repost operations
- Private owner-only bookmarks, explicit private/public curated collections with viewer-aware contents, idempotent follows, separate categories/tags, and viewer-aware search
- Durable owner-scoped notifications, including private-message notifications, generated inside source transactions; per-kind unread counts, atomic future-event preferences, and owner/filter-scoped seek pagination
- Database-backed, membership-scoped private conversations and messages with sender idempotency, opaque cursors, monotonic read receipts, safe leave lifecycle, and HTTP polling checkpoints for reconnects
- Public profiles/follower lists, block-aware visibility, category follows, category/tag feed filters, device-session management, and profile covers
- Versioned local homepage/carousel operations configuration with `If-Match`, admin RBAC, media references, and immutable audit
- Local non-payment product/type catalog administration, bounded integer inventory, owner carts, idempotent order creation with atomic reservation, and explicit `created`/`cancelled`/`fulfilled` lifecycle
- Owner-scoped shipping-address CRUD, immutable order address snapshots, audited manual fulfillment and append-only internal tracking timelines, exact-once cancellation inventory restoration, and owner-scoped order reads; orders contain no money, payment, wallet, live carrier, or provider state
- UTC daily check-in, immutable award-only local points, admin-verified task rewards, threshold-derived levels/titles/ranks, and avatar-frame catalog/entitlement/selection with duplicate-award guards
- Member reports, explicit pending-to-published/rejected post review, audited moderator/admin pin/recommend controls, filtered content review, admin user status controls with session revocation, audited category/tag CRUD, role checks, and immutable audit events
- Privacy-safe owner activity history, bounded local rate limits for login and write-heavy endpoints, security response headers, structured request logs, and process-local `/metrics`
- SQLite foreign keys and local demo seed, plus a pgx/PostgreSQL 17 production adapter with embedded checksummed migrations, request IDs, validation and consistent error envelopes
- OpenAPI contract at `api/openapi.yaml`
- Explicit disabled boundaries for SMS, payment, cash wallet, withdrawal, lottery, paid-content purchase and external fulfillment integrations

SQLite remains the local-development adapter. PostgreSQL 17 is the production database adapter for the core identity/feed/post/comment path; it uses pgx stdlib, connection pooling, UTC sessions, serialized checksummed migrations, and the same domain transactions through dialect-safe binding. Real legacy-data profiling and production concurrency/operations acceptance remain separate gates.

## Run

Use Go 1.26 or newer with CGO enabled and a C compiler for the SQLite adapter. The October 11 continuation was verified with Go 1.26.9 and PostgreSQL 17.11. `make` accepts `GO=/path/to/go` when your toolchain is not on PATH.

```sh
cd /path/to/fanbbsapi
export CGO_ENABLED=1
go run ./cmd/api
```

Configuration:

| Variable | Default | Meaning |
|---|---|---|
| `FANBBS_DATABASE_DRIVER` | `sqlite` | `sqlite` for local development or `postgres` for the production adapter |
| `FANBBS_DATABASE_URL` | value of `FANBBS_DB` | PostgreSQL URL when the production adapter is selected |
| `FANBBS_ADDR` | `:8080` | HTTP listen address |
| `FANBBS_DB` | `fanbbs.db` | SQLite database path |
| `FANBBS_SEED_DEMO` | SQLite: `true`; PostgreSQL: `false` | Seed local demo users/content when the user table is empty |
| `FANBBS_BLOB_DIR` | `data/blobs` | Local-development blob directory |
| `FANBBS_MEDIA_RETENTION` | `24h` | Minimum age for the one-shot worker to expire unattached media |
| `FANBBS_MEDIA_CLEANUP_LIMIT` | `100` | Maximum candidates and queued blob deletes per worker pass (1–1000) |

Secret-free environment and reverse-proxy examples live under `deploy/`. They are templates only; inject the PostgreSQL URL from the deployment platform's secret store and keep metrics private.

The optional seed contains local test identities used by the integration suite. They are development fixtures only. Set `FANBBS_SEED_DEMO=false` outside local development; production guidance never relies on seeded credentials.

PostgreSQL startup and the migration-only command use the same adapter:

```sh
FANBBS_DATABASE_DRIVER=postgres \
FANBBS_DATABASE_URL='postgres://fanbbs:password@db.example/fanbbs?sslmode=verify-full' \
go run ./cmd/migrate -driver postgres
```

Run a bounded orphan-media cleanup pass (suitable for deployment cron):

```sh
FANBBS_DB=fanbbs.db FANBBS_BLOB_DIR=data/blobs go run ./cmd/worker
```

PostgreSQL migrations live under `migrations/postgres`, mirror SQLite versions `001` through `012`, take a transaction-scoped advisory lock, and reject checksum drift. GitHub Actions starts PostgreSQL 17 and runs the real adapter rehearsal with `FANBBS_TEST_POSTGRES_DSN`.

The CI workflow also creates logical SQLite and PostgreSQL backups from synthetic databases, restores both into separate targets, and compares migration integrity. See `docs/operations.md` and `scripts/rehearse_backup_restore.sh`; the script has no production target or credentials built in.

## Verify

```sh
go test ./...
go vet ./...
go test -race ./internal/app ./internal/community ./internal/commerce
```

The integration suite uses a real temporary SQLite database and CI PostgreSQL 17 service. The PostgreSQL rehearsal covers all twelve migrations and idempotent reapplication, registration/login/authentication, follower visibility and feed reads, idempotent post/comment writes, reactions, optimistic edits, deterministic synthetic import reconciliation/quarantine/replay, target-conflict quarantine without an aborted PostgreSQL transaction, dry-run rollback, and trigger-forced import rollback. The broader SQLite suite covers registration/profile/password/deactivation and one-time recovery, all four feeds, cross-user follower visibility, create/detail/comments/replies, versioned revision history, taxonomy/search, bookmarks/follows, durable notifications, pending-post moderation and feed controls, moderator/admin RBAC, immutable audits, idempotency, security headers, local metrics/rate limits, and forced counter/audit failures that prove source transactions roll back. It also covers messaging membership masking, conversation reuse, message replay/conflicts, scoped cursors, poll-based reconnect events, admin user/content filters, suspension session revocation/reactivation, audited taxonomy conflicts/in-use deletion, and audit-failure rollback.

It also covers upload MIME/ownership/visibility and public/private cache policy, avatar/cover references, device-session ownership/revocation, public-profile and block visibility, category follows/feed filters, monotonic read receipts and safe conversation leave, homepage version/audit rollback, stale post/comment writes, comment reactions, moderated counter/repost cleanup, and deterministic synthetic legacy import with quarantine/count reconciliation.

The commerce/gamification integration cases cover admin RBAC, public catalog reads, bounded stock, cart ownership, address ownership/defaults, immutable order address snapshots, idempotent checkout, atomic reservation, order masking, exact-once cancellation restoration, terminal fulfillment with audited manual tracking metadata, immutable tracking events, forced-audit rollback, duplicate UTC check-ins, immutable point events, admin-verified task awards, duplicate-award prevention, avatar-frame entitlement/selection and disabled payment/wallet/VIP/raffle/paid-content/external-fulfillment capabilities.

## Synthetic migration rehearsal

`cmd/importlegacy` has no legacy-network or MySQL capability. It refuses to open the target database until both `--synthetic` is passed and the JSON document declares `"source":"synthetic"` with `mapping_version: 1`. It accepts either the normalized fixture layout or `--format fanbbs-java`, an authentic row-shaped fixture that must additionally declare `"schema":"fanbbs-java-installcontroller-v1"`. The target can be local SQLite or the configured PostgreSQL adapter.

```sh
go run ./cmd/importlegacy \
  --synthetic --input ./path/to/generated-fixture.json --db ./rehearsal.db

go run ./cmd/importlegacy \
  --synthetic --format fanbbs-java \
  --input ./path/to/generated-authentic-shape-fixture.json --db ./rehearsal.db

# Exercise the complete mapping, target-conflict checks and reconciliation,
# then roll back all application, import-run and quarantine rows.
go run ./cmd/importlegacy \
  --synthetic --dry-run --format fanbbs-java \
  --input ./path/to/generated-authentic-shape-fixture.json --db ./rehearsal.db
```

The authentic-shape adapter mirrors the installer-backed Java tables for users, contents, comments, metas/relationships and fan follows. The importer creates deterministic IDs/run hashes, maps category/tag joins, posts, threaded comments and follows, quarantines unsupported/orphan/paid/target-conflict rows, and makes an identical input idempotent. Conflict handling uses non-throwing inserts so PostgreSQL can record the quarantine inside the same transaction instead of entering an aborted transaction state. Ambiguous duplicate source IDs are rejected before a transaction begins, and canonicalization never mutates the decoded snapshot. Legacy media strings are inventoried and quarantined for a separate verified blob-copy pass; the command never fetches them. Strict decoding rejects unknown fields, trailing documents and undeclared source/schema versions before the target is opened.

Dry-run output sets `dry_run: true`. It commits no application, import-run or quarantine rows. PostgreSQL sequences are intentionally outside transaction rollback, so a rehearsal can leave harmless sequence gaps; run rehearsals against a disposable clone when gap-free sequence accounting matters.

Legacy password values are discarded and every imported password is the invalid `legacy-login-disabled` sentinel. The importer creates neither sessions nor recovery codes, so imported accounts remain unable to log in. A separately approved identity-proofing/account-claim flow must set a new Argon2id password and issue new recovery codes before production activation. See `docs/legacy-schema-mapping.md` for the evidence, exact mapping and remaining gates. This remains a synthetic migration rehearsal tool, never a real-data connector.

## Response contract

Success:

```json
{"data":{"id":"post_..."},"meta":{"request_id":"req_..."}}
```

List:

```json
{"data":[],"page":{"next_cursor":"..."},"meta":{"request_id":"req_..."}}
```

Error:

```json
{"error":{"code":"validation_failed","message":"请检查标记字段","field_errors":{"content":["不能为空"]},"request_id":"req_..."}}
```

The API accepts the Vue slice's compact names (`identity`, `content`, `recommended`, singular `/repost`) and also exposes the canonical design fields/routes (`account`, `body`, `recommend`, `/reposts`).

## Layout

```text
cmd/api                 HTTP process
cmd/migrate             migration-only process
cmd/worker              one-shot local orphan-media cleanup worker
internal/identity       passwords and sessions
internal/community      posts, feeds, comments, social relations, taxonomy,
                        search, notifications, private messaging, moderation
                        and administrative operations
internal/commerce       local non-payment catalog, bounded inventory, carts,
                        orders, manual fulfillment and non-cash gamification
internal/capabilities   disabled external/regulated interfaces
internal/blob           blob interface and bounded local-filesystem adapter
internal/legacyimport   synthetic-only authentic-schema adapter, mapper and quarantine report
internal/platform       HTTP envelope, IDs, SQLite and pgx/PostgreSQL bootstrap
migrations              embedded SQLite and PostgreSQL schema sources of truth
api/openapi.yaml        public API contract
docs/remaining-features.md
```

## Inbox preferences and pagination

`GET /api/v1/notifications/counts` returns total and all six per-kind unread counts in one query. `GET/PATCH /api/v1/me/notification-preferences` reads or atomically updates optional booleans under `preferences` (`comment`, `like`, `repost`, `follow`, `message`). Defaults are enabled; moderation notices remain mandatory. Changes affect future events only. Muted events do not appear in the inbox or badges, but remain in the reconnect log with `muted: true` so chat synchronization is not lost. Clients must suppress alerts for muted events.

Notification list cursors are sequence-based and scoped to the current owner, unread filter and optional type filter. Keep the same filters on subsequent pages. New events and marking earlier pages read do not shift the remaining page. Old offset cursors are intentionally rejected; restart the list without a cursor after upgrading.

`PG_BIN=/path/to/postgres/bin GO=/path/to/go scripts/test-postgres-local.sh` creates a fresh loopback-only disposable PostgreSQL cluster, executes adapter tests and stops it through an EXIT trap. It never accepts a production DSN. The stopped cluster data is removed; the retained directory contains logs for inspection.

## Chat unread consistency

Conversation list/detail responses include viewer-specific `unread_count` and the list now includes `last_read_message_id`. `GET /api/v1/conversations/counts` returns unread conversation/message totals for active memberships, excluding the viewer's own messages. Muting inbox notices does not erase unread chat state. Advancing a read receipt clears the corresponding message inbox badges in the same transaction; leaving dismisses only that member's badges. PostgreSQL conditional updates prevent older concurrent receipts from moving the checkpoint backwards. Forced badge-update failures prove receipt/leave rollback.

## Device-session labels and activity

Owners can `PATCH /api/v1/me/sessions/{session_id}` with `{"name":"My laptop"}` (up to 80 Unicode characters; empty clears the label). Unknown, expired, revoked and foreign sessions are masked as not found. Labels survive refresh rotation within the rotation transaction. Session lists include `name` and `last_active_at`, sorted by recent activity. Authentication updates coarse activity at most once per minute and never moves it backwards. No user-agent fingerprint, IP address or location is collected.

## PostgreSQL transaction contention

Local non-payment cart, checkout, shipping-address defaults and non-cash award transactions now acquire an owner-row `FOR NO KEY UPDATE` lock on PostgreSQL before read/check/write decisions. SQLite remains single-connection. This serializes same-owner requests without blocking unrelated foreign-key checks. Order transitions lock their order row before restoring inventory, preventing concurrent cancellations from restoring it twice. The point-balance upsert qualifies existing columns for both adapters.

Real PostgreSQL tests cover twelve competing same-key checkouts, different-key checkout races on one cart, concurrent cancellation, daily check-in and task awards, concurrent default-address changes, and forced-audit rollback. These use synthetic data and no payment or live fulfillment integration. Production volume, latency and deployment acceptance remain separate.

## Private author workspace

`GET /api/v1/me/posts` lists the author's own undeleted submissions with optional `published`, `pending`, `rejected` or `draft` filtering and owner/filter-scoped seek cursors. `GET /api/v1/me/posts/{post_id}` returns a consistent `post` plus optional `moderation` snapshot, including a rejection reason and ETag. This closes the gap where a submitted pending post could be read only in its creation response. Foreign owners and public post endpoints still cannot read unpublished content. Resubmission/appeals and edits during review remain separate policy decisions.

### Receipt replay and concurrent leave

Receipt replay reconciles message badges against the effective stored checkpoint even when the requested checkpoint is equal or older (`changed` remains false). Sending, reading and leaving acquire the same per-conversation transaction lock on PostgreSQL. A recipient cannot leave between recipient selection and notification insertion, and a send cannot pass a stale active-member check then commit without recipients. Deterministic PostgreSQL tests pause both message insertion and notification insertion with an advisory-lock gate; leave waits, then clears the committed badge. No data backfill or old-version compatibility path is involved.

## Author draft lifecycle (October 12)

Create with `status: draft` to persist an owner-only draft. Existing versioned post PATCH/DELETE accepts drafts and rejected submissions; pending submissions require withdrawal first. `POST /me/posts/{id}/review` with `If-Match`, `action: submit|withdraw` and a 3–500 character reason moves draft/rejected → pending or pending → draft. Moderation, editing and author transitions serialize on the same post row, advance versions and preserve audit/revision history. The web author workspace includes edit/save, withdraw, resubmit and delete controls, and the composer supports server drafts. Existing direct-publish creation remains unchanged.

Validation for this feature-first pass is compilation, a single SQLite lifecycle smoke case and frontend production build only; complete regression, PostgreSQL concurrency and browser acceptance remain pending.

## Moderator workbench (October 12)

Role-gated `/admin/posts/{id}` returns a private submission snapshot; `/admin/posts/{id}/media/{mediaId}` previews only ready post-purpose media actually attached to that post, always no-store; `/admin/posts/{id}/review-history` returns only submission/decision audit entries for that post. Ordinary public/author media routes gain no moderator bypass. Decisions honor If-Match when supplied and audit the reviewed/resulting versions. The web workbench supplies the snapshot version, displays full content and authenticated attachments, pages queues/history, takes reasoned decisions and manages published pin/recommend controls. A focused role/media/version smoke and Vite build passed; full regression/PG/browser acceptance remains pending.

## Report resolution workspace (October 12)

`GET /me/reports` provides a private, paginated status-filtered list of the signed-in reporter's submissions and decision reasons. It never includes target content or moderator identity. `GET /admin/reports/{id}` lets moderators inspect current post/comment/user context, including a reply's parent comment, parent post and existing role-gated attachment preview. The web report workbench adds queue pagination, open/decided filters, context inspection and reasoned decisions; the profile page includes personal report history.

Report decisions serialize per report; content removal locks the target and honors optional If-Match, preventing decisions against an edited version and duplicate counter updates. The UI supplies the reviewed version. Existing role checks, immutable audits and notifications remain in the transaction. Compilation, one SQLite comment/context/owner-isolation/version/outcome smoke and frontend build passed; full regression, PostgreSQL concurrency and browser acceptance are deferred.

## Author attachment editing (October 12)

Post PATCH accepts an ordered `media_ids` replacement (up to eight unique ready post-purpose uploads owned by the author). Omission preserves attachments; an empty array detaches all. Other posts' attachments and foreign uploads are rejected. Changes share the text-edit/version transaction and immutable media-change audit; pending submissions must first be withdrawn. Detached blobs are not immediately destroyed and remain subject to existing unreferenced-upload cleanup. The author workspace can select new uploads, reorder and remove existing attachments, and saves everything together; a failed edit abandons only its newly uploaded unreferenced assets.

Minimal validation: Go compilation, one SQLite attachment ordering/ownership/rollback/version/pending-review smoke, OpenAPI validation and Vue SFC compilation. No complete regression, PostgreSQL concurrency or browser acceptance in this feature-first pass.

## Revision recovery (October 12)

`POST /posts/{id}/revisions/restore` accepts a historical `version`, a 3–500 character `reason` and the current If-Match. Owner authorization, pending-review restrictions and version checks share the existing edit transaction. It snapshots the current content before restoring historical title/summary/body/category/tags/visibility, increments the version and writes an immutable restore audit. Current attachments and publication status are retained. Deleted taxonomy references fail validation instead of silently substituting values. The private author workspace pages old versions, previews full text and metadata, confirms visibility changes, and restores without discarding history.

Validation was one SQLite ownership/version/history/pending-review smoke, Vue SFC compilation and OpenAPI validation only. Full regression, PostgreSQL concurrency and browser acceptance remain pending.

## Discussion lock (October 12)

Moderators can `PUT /admin/posts/{id}/comments-lock` with current If-Match, `locked` and a 3–500 character reason. SQLite/PostgreSQL migration 013 persists `posts.comments_locked`. Changed state advances the post version, records immutable audit and notifies the author; repeated identical state at the current version is a no-op. New comments/replies serialize on the same post row and return `comments_locked` when paused, including moderator comments. Existing discussions remain readable and old successful idempotency replays remain successful. The moderation workbench can close/reopen discussion, refreshes the audit timeline, and public/author views show the pause and suppress new-reply controls. Existing comment edit/delete/reaction permissions are unchanged.

Validation: one SQLite migration/role/version/lock/reply/replay/reopen smoke, OpenAPI validation and Vue SFC compilation. PostgreSQL migration execution/concurrency and complete regression/browser acceptance remain pending.

Report outcomes also have owner-only `/me/reports/{id}` detail, masking foreign and absent IDs identically. Web `#reports` and `#report-{id}` routes support direct result reading from moderation notices without searching old pages, while account changes clear the focused report. Comment-lock notices open the author's workspace. Ordinary feed/detail attachments now use authenticated media reads; feed loads are viewport-delayed and object URLs are released when views or identities change. These additions received targeted SQLite owner checks and Vue SFC compilation, not browser acceptance.

## Administrator account workspace (October 12)

The web account workbench supports filtered pagination, account detail and safe aggregate counts, reasoned activation/suspension, independent revocation of another user's existing sessions, and paginated account-specific audit history. Three admin-only endpoints provide `/admin/users/{id}`, `/moderation-history` and `/sessions/revoke`; detail never returns session identifiers, tokens, hashes or recovery material. Revocation and its audit are atomic and do not suspend future login. Self-session management stays in the existing personal security page. Status writes accept `expected_status` and lock the target, preventing stale workbench decisions. Direct and report-driven suspension share deterministic administrator-row locking and self/last-active-admin safeguards.

Validation: one SQLite administrator workflow smoke including session invalidation and re-login, status conflict, suspend/reactivate and audit history; Vue SFC compilation and OpenAPI validation. Full regression, PostgreSQL contention and browser acceptance remain pending.

## Direct comment moderation (October 12, runtime validation deferred)

The content workbench now reviews comments even without a report: moderator/admin detail includes current version, parent-comment context and parent-post content/attachments. A reasoned If-Match removal locks the comment, soft-deletes it, decrements its parent post counter once, writes immutable audit and notifies its author in one transaction. Existing replies remain governed by the existing deleted-ancestor tombstone rendering. Scoped history includes direct removals and report decisions. A moderator cannot rewrite a comment, and ordinary public read/privacy paths are unchanged. Existing open reports remain explicit cases in the report queue.

Routes: `GET /admin/comments/{id}`, `POST /admin/comments/{id}/remove`, `GET /admin/comments/{id}/moderation-history`. This increment received gofmt parsing, Vue SFC/JavaScript source checks and diff checks only. No Go compilation, SQLite execution, PostgreSQL execution or browser test was run after the disk-space stop instruction. Role denial, stale versions, counter/audit rollback and descendant tombstones need focused execution during consolidated acceptance.


Published posts now have the corresponding direct moderation path: `POST /admin/posts/{id}/remove` requires moderator/admin, If-Match and reason, reuses existing soft-removal semantics and commits version, repost-relation/counter changes where relevant, audit and author notification together. It does not act on pending/private drafts or implicitly resolve open reports. The content workbench confirms removal, hides stale attachment previews and shows its audit entry. This increment received source/document checks only; runtime validation remains deferred under the disk-space stop instruction.


The private author list now supports literal `q` substring search across title/summary/body and `kind=article|image|video|repost` filtering. Owner, status, query and kind scope its seek cursor so a page cannot be reused across filters or accounts. The author workspace combines search, type and review-status controls with reset and continued paging. Owner list/detail responses are explicitly private/no-store. This increment has source/document checks only; no Go compilation or runtime tests were executed.
