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
