# Remaining feature matrix

This matrix is grounded in the original Java source under `backend/fanbbsapi-dev.zip_unzipped/fanbbsapi-dev`: its controllers, entities, services and MyBatis mapper XML. “Implemented” below means implemented in this local Go slice, not production-ready.

| Original capability | Original source evidence | Local Go slice | Remaining work / gate |
|---|---|---|---|
| Login and refresh | `UsersController`, `JWT`, `JWTInterceptors` | Local registration, Argon2id login, opaque access + rotating refresh, password change, logout and revocation | Recovery design, OAuth/provider binding, legacy PHPass/bcrypt migration and device-session management UI |
| User profile | user info/update/edit/destroy routes | Authenticated edit/deactivation, avatar/cover media, public profile and follower/following lists, device-session list/revoke, and admin status/session controls | Expanded privacy controls, timed suspension and device-session naming/activity metadata |
| Article feeds | `ArticleController.articleList`, `follow`; `ArticleMapper.xml` | Recommend/latest/global/following with cursor paging plus category/tag filters and block-aware visibility | Pin/recommend controls, stable production ranking and PostgreSQL query plan validation |
| Post create/detail/edit/delete | `articleAdd`, `info`, update/delete routes, Article entity | Create/detail plus owner edit/soft-delete with versioned `If-Match`, tag/media references and idempotency | Rich-text sanitization, revision history, moderation publish workflow and paywall visibility |
| Comments/replies | `CommentsController.list/add/edit/delete/like`, Comments entity | List/reply plus owner versioned edit/soft-delete, idempotent reactions, transactional counters and notifications | Tombstone thread presentation, per-thread cursors, rate limits and expanded moderation |
| Article likes | `ArticleController.like`, `Userlog(articleLike)` | Idempotent PUT/DELETE with uniqueness and transactional counter | Abuse controls, event/notification fan-out and production concurrency tests |
| Reposts | No original persistence/action endpoint; only share-task counters | New explicit idempotent repost relation + generated repost post | Product policy for quoting/commentary, follower notifications and migration semantics (there is no original source equivalent) |
| Bookmarks and follows | article mark routes; user/category follow routes; Fan/Relationships | Idempotent bookmarks, user/category follows, follower/following lists, block mutations/list and block-aware reads implemented | Richer block policy, category discovery UI and import mapping |
| Categories/tags/search | `CategoryController`, article search/list parameters | Separate taxonomy, tag joins, public lists, viewer-aware search, and audited admin category/tag CRUD with case-insensitive uniqueness and in-use delete guards | Category follows, feed filter URLs and production full-text indexing |
| Reports/moderation | Report/Violation controllers/entities and article audit routes | Idempotent report intake, filtered post/comment review, moderator/admin queue, transactional decisions, user status control and immutable audit | Pending-post workflow, appeals, timed suspensions, expanded cases and retention policy |
| Notifications/inbox | Inbox controller/entity, unread counters, PushService | Durable comment/like/repost/follow/moderation/message notifications with owner-scoped list/read and user-scoped reconnect event polling | Per-kind counts/preferences and optional external push provider retry policy |
| Chat/realtime | Chat/ChatMsg routes; empty `webSocketController`; scattered socket endpoint/config | Database-backed conversations with membership checks, idempotent sends, seek paging, reconnect cursors, monotonic read receipts and non-destructive leave lifecycle | Message edit/delete policy, explicit reinvitation, abuse/rate limits and optional WebSocket/external delivery |
| Uploads | upload controller with local/COS/OSS/FTP/Qiniu branches | Blob interface plus bounded local-filesystem adapter, MIME/size/checksum metadata, post visibility and avatar references | Production S3 adapter/presigned workflow, malware scanning, dimensions and media processing states |
| Shop/orders/shipping | ShopController, Order, shop/order mapper set | Not implemented; external/commerce work disabled | Product and inventory model, integer-money order state machine, fulfillment, policy and operational acceptance |
| Wallet/payments/tips/VIP/withdrawal | PayController, Paylog, Reward_log, article pay/tip and VIP routes | Interfaces explicitly disabled | Double-entry ledger, provider signature/callback idempotency, reconciliation, refund/reversal rules, security and legal review |
| Raffle/exchange/tasks/rank/sign-in | Raffle, Exchange, Task, Rank and related routes/entities | Interfaces disabled or absent | Auditable randomness, inventory transactions, anti-abuse rules and product approval |
| Avatar frames/medals | Headpicture and rank/medal fields/routes | Not implemented | Entitlement model and profile display |
| Carousel/home/app config | swiper and configuration controllers | Versioned local homepage/carousel config with admin RBAC, `If-Match`, media references and immutable audit | Scheduling/experiments, localization and production CDN workflow |
| Web installer/updater | install/update routines | Intentionally not planned as public web API | Keep deployment and migrations in operational tooling |

## Production gates for this slice

- Replace SQLite with the approved PostgreSQL/sqlc repository implementation after real schema and volume discovery
- Add rate limits, structured logging/metrics/traces, security headers and secrets management
- Profile and import the real legacy schema; verify row counts, relations, unknown enums and legacy password hashes
- Replace the synthetic-only importer with an separately approved, read-only real snapshot adapter after profiling; reuse its deterministic mapping/quarantine/count invariants
- Define moderation, privacy/retention, minors and content policies
- Add load/concurrency, migration rehearsal, backup/restore and rollback tests
- Configure CORS/CSRF according to the final same-origin or cookie deployment model; this slice returns bearer tokens in JSON and never stores refresh tokens in the frontend
