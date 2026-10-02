# Legacy Java schema mapping

This mapping is grounded in the checked-in original Java source at
`../../backend/fanbbsapi-dev.zip_unzipped/fanbbsapi-dev`, especially
`InstallController`, the entities, and the MyBatis mapper XML. It describes the
schema created by that installer; it is not evidence that every deployed
database has the same version or clean values.

`cmd/importlegacy --format fanbbs-java` accepts only an offline JSON fixture
declared with:

```json
{"source":"synthetic","schema":"fanbbs-java-installcontroller-v1","mapping_version":1}
```

It has no legacy MySQL reader and never fetches a media URL. The authentic-row
adapter is a testable schema map, not approval to process a real snapshot.

| Original table/columns | Local target | Implemented handling |
|---|---|---|
| `${prefix}_users`: `uid`, `name`, `screenName`, `password`, `mail`, `introduce`, `avatar`, `status`, `group`, `created` | `users` | Deterministic IDs; supported groups map to member/moderator/admin; status 0/1 maps to suspended/active; invalid handles receive a deterministic `legacy_<uid>` handle while the visible name is preserved; bio and Unix creation time are mapped |
| `${prefix}_contents`: `cid`, `mid`, `title`, `text`, `authorId`, `type`, `status`, `price`, `images`, `videos`, `created` | `posts` | `post`→article, `photo`→image and `video`→video; only published, free, valid rows are imported; other status, kind, paid and orphan rows are quarantined |
| `${prefix}_metas`: `mid`, `name`, `slug`, `type` | `categories`, `tags` | Category/tag types are separated; a missing/unsafe slug receives deterministic `legacy-meta-<mid>` fallback; other types are quarantined |
| `${prefix}_relationships`: `cid`, `mid` plus `contents.mid` | `posts.category_id`, `post_tags` | The first deterministic category is selected and tag joins are deduplicated; dangling target relations prevent the affected post import |
| `${prefix}_comments`: `id`, `cid`, `uid`, `text`, `parent`, `all`, `created` | `comments` | Parent chains are resolved independently of row order; missing/cyclic parents and depth over two are quarantined |
| `${prefix}_fan`: `id`, `uid`, `touid`, `created` | `follows` | Valid user follows are imported; missing users, self-follows and target conflicts are quarantined |
| `users.avatar`, `metas.avatar`/`imgurl`, `contents.images`/`videos`, `comments.images` | `legacy_quarantine` only | JSON/text locations are inventoried as `media_recopy_required`; no URL is fetched and no `media_assets` row is created without verified bytes, MIME type, size and checksum |

## Credentials and account claim plan

The adapter decodes the original `password` column only to recognize the
authentic row shape, then discards it. Every imported user receives the invalid
sentinel `legacy-login-disabled`; no PHPass hash is copied and no session or
recovery code is created. Imported accounts therefore cannot log in.

Production migration still needs a separately approved identity-proofing and
account-claim flow that sets a new Argon2id password and issues new one-time
recovery codes. That flow, user communications, collision resolution, and an
audited activation procedure are not implemented by this importer. Legacy
password verification or silent hash upgrade is explicitly outside this pass.

## Still outside this mapping

- Reading MySQL, selecting a table prefix, or writing any remote system
- Profiling a real database for schema drift, duplicate identities, unknown
  enum values, volume, encoding, or corrupt relationships
- Copying/scanning media bytes or rewriting rich-text embedded media
- Legacy `userlog` bookmarks/likes/category follows, payment/VIP state, chat,
  commerce, notifications, moderation history, and other non-core tables
- Approval and count reconciliation against a real snapshot
