# FanBBS Go API

This is a runnable local vertical slice of the approved FanBBS rewrite. It keeps the original `../../backend` Java snapshot untouched. HOP informed the shallow, subject-oriented layout and linear request flow; it is not linked, vendored, or copied as a Go framework.

## Included

- Local registration, Argon2id credentials, opaque 15-minute access tokens, rotating 30-day refresh sessions, one-time local recovery codes (displayed once and hashed at rest), password/profile changes, soft deactivation, and server-side revocation
- `recommend`/`recommended`, `latest`, `global`, and authenticated `following` feeds with opaque cursor pagination
- Post create/detail, threaded comments/replies, idempotent post and comment creation
- Post/comment edit and soft delete with required `If-Match` versions, immutable owner-readable post revision history, and idempotent comment likes
- Bounded local-filesystem media uploads behind a blob interface, post attachment, and avatar upload/reference flow
- Transaction-safe, idempotent like/unlike and repost/undo-repost operations
- Idempotent bookmarks/follows, separate categories/tags, and viewer-aware search
- Durable owner-scoped notifications, including private-message notifications, generated inside source transactions
- Database-backed, membership-scoped private conversations and messages with sender idempotency, opaque cursors, monotonic read receipts, safe leave lifecycle, and HTTP polling checkpoints for reconnects
- Public profiles/follower lists, block-aware visibility, category follows, category/tag feed filters, device-session management, and profile covers
- Versioned local homepage/carousel operations configuration with `If-Match`, admin RBAC, media references, and immutable audit
- Local non-payment product/type catalog administration, bounded integer inventory, owner carts, idempotent order creation with atomic reservation, and explicit `created`/`cancelled`/`fulfilled` lifecycle
- Audited manual fulfillment metadata, exact-once cancellation inventory restoration, and owner-scoped order reads; orders contain no money, payment, wallet, or provider state
- UTC daily check-in, immutable award-only local points, admin-verified task rewards, threshold-derived levels/titles/ranks, and avatar-frame catalog/entitlement/selection with duplicate-award guards
- Member reports, explicit pending-to-published/rejected post review, audited moderator/admin pin/recommend controls, filtered content review, admin user status controls with session revocation, audited category/tag CRUD, role checks, and immutable audit events
- Privacy-safe owner activity history, bounded local rate limits for login and write-heavy endpoints, security response headers, structured request logs, and process-local `/metrics`
- SQLite foreign keys, embedded forward migrations, local demo seed, request IDs, validation and consistent error envelopes
- OpenAPI contract at `api/openapi.yaml`
- Explicit disabled boundaries for SMS, payment, cash wallet, withdrawal, lottery, paid-content purchase and external fulfillment integrations

SQLite is explicitly the local-development adapter. The approved production target remains PostgreSQL with profiled legacy-data migration and production concurrency testing. This slice is not a production migration or a claim that the remaining original features are complete.

## Run

The checked environment has Go at `/tmp/go1.26.5/bin/go`. Dependencies are already in the workspace module cache.

```sh
cd fanbbs-rewrite/backend
mkdir -p .cache/go-build
export GOPATH=/workspace/scratch/e5152f2a16c1/.gitea-gopath
export GOCACHE="$PWD/.cache/go-build"
export GOMAXPROCS=1 GOGC=25 GOMEMLIMIT=768MiB
export GOPROXY=off GOSUMDB=off CGO_ENABLED=1
/tmp/go1.26.5/bin/go run ./cmd/api
```

Configuration:

| Variable | Default | Meaning |
|---|---|---|
| `FANBBS_ADDR` | `:8080` | HTTP listen address |
| `FANBBS_DB` | `fanbbs.db` | SQLite database path |
| `FANBBS_SEED_DEMO` | `true` | Seed local demo users/content when the user table is empty |
| `FANBBS_BLOB_DIR` | `data/blobs` | Local-development blob directory |

The optional seed contains local test identities used by the integration suite. They are development fixtures only. Set `FANBBS_SEED_DEMO=false` outside local development; production guidance never relies on seeded credentials.

## Verify

```sh
find cmd internal migrations -name '*.go' -print0 | xargs -0 /tmp/go1.26.5/bin/gofmt -w
/tmp/go1.26.5/bin/go test ./...
/tmp/go1.26.5/bin/go vet ./...
```

The integration suite uses a real temporary SQLite database. It covers registration/profile/password/deactivation and one-time recovery, all four feeds, cross-user follower visibility, create/detail/comments/replies, versioned revision history, taxonomy/search, bookmarks/follows, durable notifications, pending-post moderation and feed controls, moderator/admin RBAC, immutable audits, idempotency, security headers, local metrics/rate limits, and forced counter/audit failures that prove source transactions roll back. It also covers messaging membership masking, conversation reuse, message replay/conflicts, scoped cursors, poll-based reconnect events, admin user/content filters, suspension session revocation/reactivation, audited taxonomy conflicts/in-use deletion, and audit-failure rollback.

It also covers upload MIME/ownership/visibility and public/private cache policy, avatar/cover references, device-session ownership/revocation, public-profile and block visibility, category follows/feed filters, monotonic read receipts and safe conversation leave, homepage version/audit rollback, stale post/comment writes, comment reactions, moderated counter/repost cleanup, and deterministic synthetic legacy import with quarantine/count reconciliation.

The commerce/gamification integration cases cover admin RBAC, public catalog reads, bounded stock, cart ownership, idempotent checkout, atomic reservation, order masking, exact-once cancellation restoration, terminal fulfillment with audited tracking metadata, forced-audit rollback, duplicate UTC check-ins, immutable point events, admin-verified task awards, duplicate-award prevention, avatar-frame entitlement/selection and disabled payment/wallet/VIP/raffle/paid-content/external-fulfillment capabilities.

## Synthetic migration rehearsal

`cmd/importlegacy` has no network or MySQL capability. It refuses to open the target database until both `--synthetic` is passed and the JSON document declares `"source":"synthetic"` with `mapping_version: 1`.

```sh
/tmp/go1.26.5/bin/go run ./cmd/importlegacy \
  --synthetic --input ./path/to/generated-fixture.json --db ./rehearsal.db
```

The synthetic mapping-v1 importer creates deterministic IDs/run hashes, maps users with imported login credentials explicitly disabled, category/tag taxonomy, posts and threaded comments, quarantines unsupported/orphan/paid rows, reconciles per-entity counts, and makes an identical input idempotent. It is a migration rehearsal tool, never a real-data connector.

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
cmd/worker              explicit no-enabled-jobs worker boundary
internal/identity       passwords and sessions
internal/community      posts, feeds, comments, social relations, taxonomy,
                        search, notifications, private messaging, moderation
                        and administrative operations
internal/commerce       local non-payment catalog, bounded inventory, carts,
                        orders, manual fulfillment and non-cash gamification
internal/capabilities   disabled external/regulated interfaces
internal/blob           blob interface and bounded local-filesystem adapter
internal/legacyimport   synthetic-only deterministic mapper and quarantine report
internal/platform       HTTP envelope, IDs and SQLite bootstrap
migrations              embedded schema source of truth
api/openapi.yaml        public API contract
docs/remaining-features.md
```
