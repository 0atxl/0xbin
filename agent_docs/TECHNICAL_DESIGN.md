# 0xbin Technical Design

**Status:** Implemented MVP baseline; live-sharing extension design aligned;
hosted operational hardening remains pending
**Source of product truth:** `../spec.md`

## 1. System Context

0xbin is a single-instance web application serving a React frontend and versioned JSON API. It stores ephemeral records in SQLite on persistent local storage. Encryption occurs only in clients.

```text
Browser
  ├── plaintext create/view
  └── AES-GCM encrypt/decrypt
            |
            v
Reverse proxy / TLS
            |
            v
Go service
  ├── embedded frontend
  ├── API and validation
  ├── rate limiting
  ├── expiry worker
  └── SQLite storage
```

The approved post-MVP live mode adds a separate path through the same service:

```text
Browser live room
  ├── React/CodeMirror tabs
  ├── temporary participant presence
  └── WebSocket collaboration
            |
            v
Go live hub and authority
  ├── live HTTP bootstrap/create/unlock
  ├── room-scoped password sessions
  ├── in-memory presence and connection registry
  └── SQLite live-room snapshots and bounded change history
```

Live sharing does not modify paste routes, payloads, encryption, expiry, burn,
or rendering semantics. The initial deployment remains one Go process and one
SQLite database; the live hub is process-local and does not support multiple
application instances.

The reverse proxy may be part of the hosting platform. The Go service must also run directly for local development.

## 2. Repository Structure

Current structure:

```text
cmd/0xbin/main.go
internal/config/
internal/httpapi/
internal/paste/
internal/slug/
internal/storage/sqlite/
internal/ratelimit/
internal/cleanup/
internal/webassets/
db/migrations/
web/src/
wordlists/
docs/
agent_docs/
```

Avoid premature layers. Packages should correspond to an actual boundary or test seam.

## 3. Runtime Components

### 3.1 Configuration

Load environment variables once at startup into a validated typed configuration. Minimum fields:

```text
OXBIN_BASE_URL
OXBIN_LISTEN_ADDR
OXBIN_DATA_DIR
OXBIN_MAX_PASTE_BYTES
OXBIN_DEFAULT_EXPIRY
OXBIN_ALLOWED_EXPIRIES
OXBIN_CREATE_RATE
OXBIN_READ_RATE
OXBIN_MISS_RATE
OXBIN_CONSUME_RATE
OXBIN_TRUSTED_PROXIES
OXBIN_CREATION_ENABLED
OXBIN_READ_HEADER_TIMEOUT
OXBIN_READ_TIMEOUT
OXBIN_WRITE_TIMEOUT
OXBIN_IDLE_TIMEOUT
OXBIN_SHUTDOWN_TIMEOUT
OXBIN_LIVE_ROOM_LIFETIME
OXBIN_LIVE_MAX_TABS
OXBIN_LIVE_MAX_BYTES
OXBIN_LIVE_MAX_PARTICIPANTS
OXBIN_LIVE_MAX_MESSAGE_BYTES
OXBIN_LIVE_HEARTBEAT_INTERVAL
OXBIN_LIVE_CREATE_RATE
OXBIN_LIVE_UNLOCK_RATE
OXBIN_LIVE_CONNECTION_RATE
OXBIN_LIVE_MESSAGE_RATE
OXBIN_LIVE_MAX_CONNECTIONS
OXBIN_LIVE_SNAPSHOT_LIMITS
```

Fail startup on unsafe or incoherent values. Do not log secrets.
`OXBIN_LIVE_SNAPSHOT_LIMITS` uses `rows/bytes` syntax. The fixed validation
limits are 32 UTF-8 bytes for nicknames, 64 bytes for tab names, 64 bytes for
language identifiers, and 64 bytes for document identifiers.

### 3.2 HTTP server

- Go `net/http` with the standard-library `http.ServeMux`
- Explicit header, read, write, idle, and shutdown timeouts
- Request ID, recovery, and client-IP resolution wrap the root handler
- API handlers apply body limits and rate limits; additional security-header
  hardening remains part of pending Step 17 work
- Frontend fallback must not turn unknown API routes into HTML 200 responses

### 3.3 Paste service

Owns:

- Input validation
- Allowed expiry calculation
- Slug generation/retry orchestration
- Plaintext versus encrypted payload validation
- Retrieval semantics
- Consume semantics

It does not perform browser cryptography.

### 3.4 SQLite store

Owns SQL, migrations, transactions, and database-specific error mapping. Keep its public surface small:

```go
type Store interface {
    Create(ctx context.Context, paste NewPaste) (Paste, error)
    CreateEncrypted(ctx context.Context, paste NewEncryptedPaste) (Paste, error)
    GetActive(ctx context.Context, slug string, now time.Time) (Paste, error)
    ConsumeActive(ctx context.Context, slug string, now time.Time) (Paste, error)
}
```

The paste-service interface exists to isolate storage logic and enable tests,
not as a promise to implement PostgreSQL. The SQLite store additionally exposes
`Ping` for readiness, while the cleanup worker uses its own narrow
`DeleteExpiredBatch` interface.

### 3.5 Cleanup worker

- Runs once after successful startup/migration
- Uses a ticker and shutdown context
- Applies a timeout to each cleanup pass
- Repeats bounded deletes until fewer than the batch size are removed, with a safety cap per pass
- Stops ticker on shutdown
- Emits structured count, duration, and error logs; metrics remain part of the
  pending hosted-operational work

### 3.6 Live-sharing service

The live extension is kept behind focused packages and uses the same service
process:

- Live HTTP handlers own room creation, bootstrap, unlock, and WebSocket
  upgrade boundaries; handlers remain thin.
- The live room hub serializes room operations and orders document changes,
  tab metadata changes, presence, cursor/selection updates, reconnects, and
  expiry transitions.
- The collaboration authority uses independent metadata/document revisions,
  persists accepted changes before acknowledging them, and keeps bounded
  history for reconnect/rebase.
- Presence and short-lived room password sessions remain process memory only.
- Passwords use an adaptive Argon2id hash; the password itself never enters
  URLs, logs, SQLite, or telemetry.
- The frontend uses a small native WebSocket client and React hooks with
  Phoenix-style join/rejoin/heartbeat behavior, but does not add Phoenix or
  LiveView runtime dependencies or protocols.
- The existing shared top progress bar handles static paste loading and live
  bootstrap/connect/reconnect/resync states.

## 4. Data Design

### 4.1 Schema

Use the schema direction from `spec.md`, implemented through numbered migrations. Store time as Unix seconds UTC.

`payload` formats:

- Plaintext: versioned JSON containing title, language, and content
- Encrypted: versioned encryption envelope containing IV and ciphertext

Example plaintext payload:

```json
{
  "version": 1,
  "title": "Example",
  "language": "go",
  "content": "package main"
}
```

Plaintext payload version 1 requires valid UTF-8 and non-empty content. Limits
are measured in UTF-8 bytes: content is at most 1 MiB, title is at most 200
bytes, and language is at most 64 bytes. Empty title and language values are
valid. Size checks precede full UTF-8 validation.

The server parses plaintext payloads for validation and raw responses. It treats encrypted ciphertext as opaque after structural envelope validation.

The live extension adds separate `live_rooms`, `live_documents`, and bounded
`live_changes` tables. Live room documents are server-readable text. Room
metadata and each document have independent revision streams. Presence,
participant names/colours, cursors, selections, joined times, and room-session
cookies are not durable database data. The complete migration direction and
limits are defined in
[`LIVE_SHARING_IMPLEMENTATION_PLAN.md`](LIVE_SHARING_IMPLEMENTATION_PLAN.md).

Creation accepts an expiry identifier, not a timestamp. The default policy maps
`1h`, `24h`, and `72h` to their durations and calculates `created_at` and `expires_at`
from the server clock, normalized to UTC Unix seconds.

### 4.2 SQLite settings

- Enable foreign keys even if the MVP has one primary table.
- Attempt WAL mode and verify the hosting filesystem supports the required locking semantics.
- Configure busy timeout.
- Limit open connections appropriately for SQLite; start with one writer-oriented connection policy and benchmark.
- Use short transactions.
- Do not run `VACUUM` during ordinary cleanup.
- Consider incremental vacuum only after observing file-growth behaviour.

### 4.3 Migrations

- Use ordered SQL files embedded into the binary.
- Maintain a schema migrations table.
- Apply migrations under an exclusive migration lock at startup.
- Refuse to run if the on-disk schema is newer than the binary understands.
- Test upgrading from every supported prior release.

## 5. Slug Design

### 5.1 Word lists

Store curated adjective and noun lists as versioned repository assets. Version 1
uses 128 adjectives and 128 nouns, producing exactly 2,097,152
adjective-adjective-noun combinations. At build/test time:

- Normalize lowercase ASCII.
- Reject blank entries, duplicates, separators, digits, and unacceptable words.
- Verify no concatenated combination becomes ambiguous because of duplicate source entries.
- Record licenses/provenance for word sources.
- Confirm the product of list dimensions is approximately two million.

### 5.2 Generation

Use `crypto/rand` with unbiased bounded selection. Avoid modulo bias when mapping random numbers to list indices.

Creation algorithm:

```text
for attempt in 1..maxAttempts:
    slug = randomAdjective() + randomAdjective() + randomNoun()
    err = INSERT paste with slug
    if success: return paste
    if unique violation: continue
    return mapped storage error
return slug-space-temporarily-unavailable error
```

Choose a bounded attempt count such as 8 and test collision injection deterministically.

### 5.3 Security interpretation

The slug is an unlisted locator. Rate limiting and expiry are compensating controls, not cryptographic guarantees. Encrypted content is protected by its independent key even when a slug is guessed.

Do not confuse birthday-collision probability with lookup occupancy. Duplicate generation attempts begin to become statistically unsurprising long before the space is full, but the probability that one new attempt collides is simply approximately `active rows / total combinations`. The unique constraint and bounded retry are the correctness mechanism.

## 6. Encryption Protocol

### 6.1 Create

1. Serialize `{title, language, content}` as UTF-8 JSON with payload version.
2. Generate non-extractable-or-exportable-as-needed AES-GCM key through Web Crypto. Because the key must be shared in the fragment, export raw key bytes after generation.
3. Generate 12-byte IV with `crypto.getRandomValues`.
4. Encrypt with AES-GCM.
5. Base64url-encode IV and ciphertext.
6. POST envelope without key.
7. Append Base64url raw key to returned URL as fragment.

Do not reuse a key/IV pair. A new random key per paste makes random IV collision risk negligible at expected scale, but unique IV generation remains mandatory.

### 6.2 Retrieve

1. Fetch paste metadata/envelope by slug.
2. Extract and validate fragment key locally, or request it through the dialog.
3. Base64url-decode and require exactly 32 key bytes.
4. Import AES-GCM key.
5. Decode IV/ciphertext and decrypt.
6. Parse and validate decrypted payload version/shape.
7. Render fields as untrusted text.

The fragment must not be included in error reporting, analytics, router telemetry, or copied server requests.

### 6.3 Compatibility tests

Maintain fixed test vectors for:

- Known key, IV, plaintext, and ciphertext
- Unicode content
- Empty optional title
- Wrong key
- Modified ciphertext
- Malformed Base64url
- Unsupported envelope and plaintext versions

## 7. API Design

Use JSON with `Content-Type: application/json`. The current endpoint schemas are
defined in [`openapi.yaml`](../docs/openapi.yaml); the document expands as later API
modes are implemented.

### 7.1 Create paste

```text
POST /api/v1/pastes
```

Illustrative request:

```json
{
  "mode": "plaintext",
  "payload": {"version": 1, "title": "", "language": "plaintext", "content": "hello"},
  "expiry": "24h",
  "burn_after_read": false
}
```

Encrypted requests replace `payload` with the encryption envelope and set `mode` to `encrypted`.

Illustrative response:

```json
{
  "slug": "radiantcolorfulpomeranian",
  "url": "https://0xbin.app/radiantcolorfulpomeranian",
  "expires_at": "2026-07-14T12:00:00Z"
}
```

The server never constructs a URL containing a key.

### 7.2 Retrieve

```text
GET /api/v1/pastes/{slug}
```

- Returns active normal paste payload/envelope.
- For burn paste, returns only `{burn_after_read: true, is_encrypted: ...}` or a dedicated confirmation response, never content.
- Uses `Cache-Control: no-store` initially.

### 7.3 Consume

```text
POST /api/v1/pastes/{slug}/consume
```

- Performs atomic delete-and-return with active expiry condition.
- Returns payload/envelope once.
- Is protected by consume-specific limits.
- Uses `Cache-Control: no-store`.

SQLite 3.35+ supports `RETURNING`; pin and verify the runtime version. Otherwise implement an equivalent short write transaction without exposing a read/delete race.

### 7.4 Raw

```text
GET /api/v1/pastes/{slug}/raw
```

- Available for active, non-burn plaintext pastes.
- Returns `text/plain; charset=utf-8` with `nosniff` and `no-store`.
- Never attempts server-side decryption.
- Burn and encrypted modes do not expose raw plaintext from the server.

### 7.5 Errors

Stable structure:

```json
{
  "error": {
    "code": "paste_not_found",
    "message": "Paste not found",
    "request_id": "..."
  }
}
```

Publicly collapse missing/expired/consumed/deleted into the same code and status. Define separate validation, too-large, rate-limited, unsupported-version, creation-disabled, and internal errors.

### 7.6 Live sharing

Live endpoints are separate from paste endpoints:

```text
POST /api/v1/live
GET  /api/v1/live/{slug}
POST /api/v1/live/{slug}/unlock
GET  /api/v1/live/{slug}/ws
```

The HTTP bootstrap returns the bounded full room snapshot. The WebSocket then
carries join, document changes, tab metadata changes, presence, cursors,
selections, acknowledgements, reconnect/resync status, and expiry events. A
protected room requires a short-lived `HttpOnly`, `SameSite=Strict`, `Secure`
room session under HTTPS. Live responses use `no-store` and no-index headers.
The detailed message contract is maintained in the live-sharing implementation
plan rather than mixed into the paste API contract.

## 8. Expiry and Atomic Consume

Normal retrieval SQL must include expiry in the query. Never retrieve content and decide later that it expired.

Atomic consume concept:

```sql
DELETE FROM pastes
WHERE slug = ?
  AND burn_after_read = 1
  AND expires_at > ?
RETURNING slug, payload, is_encrypted, crypto_version,
          burn_after_read, content_size, expires_at, created_at;
```

Write concurrency tests with many goroutines/clients against the actual SQLite implementation and assert exactly one returned paste.

## 9. Rate Limiting

Use separate token-bucket categories. Initial values are configuration, not API promises.

- Create: starting point 15/hour/IP with an explicitly chosen burst
- Reads: higher allowance
- Misses: lower sustained allowance and consecutive-miss escalation
- Consume: per-IP plus short per-slug guard

Client IP resolution algorithm:

1. Parse TCP remote address.
2. If it is not a configured trusted proxy, ignore forwarding headers.
3. If trusted, parse the header format emitted by that exact proxy and select the first untrusted hop according to documented rules.
4. On malformed data, fall back safely to proxy/remote identity rather than accepting arbitrary input.

In-memory limiting resets on restart and can be bypassed using distributed IPs. This is accepted for the initial architecture; edge controls supplement it on the hosted service.

## 10. Frontend Architecture

### 10.1 Routes/states

- `/` creation experience
- `/{slug}` paste viewer or burn confirmation
- `/live` live-room creation
- `/live/{slug}` live-room password gate or collaborative room
- Not-found/error state without revealing lifecycle reason

API routes remain under `/api/v1`; health routes are reserved.

Search discovery is route-aware at the Go HTTP boundary. The configured public
base URL supplies canonical URLs. Self-hosted instances advertise only their
homepage; when the configured origin is `0xbin.app`, the sitemap also advertises
the permanent hosted information and policy routes. The server marks the HTML
shell so React enables the hosted-only corner menu without baking the operator's
policies into self-hosted navigation. Paste and unknown browser routes return
`X-Robots-Tag: noindex, nofollow, noarchive` and equivalent HTML metadata;
paste URLs never appear in the sitemap. `robots.txt` permits crawling so
compliant bots can observe those response directives.

### 10.2 State ownership

- Form state in frontend memory
- Encryption key in memory/fragment only
- No paste body or key in persistent browser storage
- Server state fetched through a small typed API client
- Live room state is owned by the live-room hook/socket client and CodeMirror;
  presence, cursors, selections, reconnect queues, and room revisions remain
  out of persistent browser storage.
- Avoid a global state library until demonstrated necessary

### 10.3 CodeMirror

Use CodeMirror 6 for the editor. Evaluate read-only CodeMirror for the viewer against a simpler preformatted/text implementation. Benchmark before committing to custom virtualization.

Language selection should be explicit initially. `plaintext` is a safe fallback. Automatic detection is deferred.

The live editor uses one collaborative CodeMirror state per document tab. The
server is the ordering authority; clients send change sets with document
revisions and receive rebased accepted updates. Remote cursors and selections
are ephemeral decorations mapped through document changes and are rendered only
for active connected participants in the same tab.

### 10.4 Security

- Render via text nodes or trusted syntax-token output, never arbitrary HTML.
- Strict CSP compatible with the actual Vite production bundle.
- No third-party analytics/scripts.
- Redact URL fragments from client error reporting.
- Test malicious content including script tags, event handlers, SVG payloads, Markdown HTML, and bidi/control characters.

### 10.5 Frontend design boundary

The implementation follows [`FRONTEND.md`](FRONTEND.md) for its
visual and interaction baseline. The route shell keeps a generic unavailable
state at the original `/{slug}` path rather than redirecting to a lifecycle
specific error route. A successful create copies the browser-built sharing URL
and navigates to that path; the URL fragment is never placed in API input,
telemetry, or application persistence.

The version-1 payload contains title, language, and content only. A creator
field or attribution requires an explicit payload, validation, crypto-vector,
API, and UI change; it is not introduced by the frontend design work.

The live frontend follows the same compact editor-first design. The `Live
share` control sits immediately left of the theme toggle. It reuses current
notifications, warnings, validation, unavailable states, and the minimal top
progress bar. It must not add technical badges, marketing panels, fake
participants, decorative placeholders, or persistent encryption/storage
explanations.

The create-page Live Share handoff carries only the unsaved draft in memory.
The paste viewer action always starts a blank live draft and never copies
plaintext or decrypted encrypted-paste content automatically.

## 11. Observability

The following metrics are planned for hosted operational hardening; the current
service emits structured logs but does not yet expose a metrics system.

Structured server logs:

- Timestamp, level, request ID, route template, status, duration, response bytes
- No raw URL query/fragment, paste slug where avoidable, payload, title, language, or key

Metrics:

- Requests and latency by route template/status
- Create/read/miss/consume/rate-limit counts
- Live room creation, connection, expiry, resync, and error categories without
  room bodies, participant names, cursors, selections, passwords, or raw frames
- Payload-size buckets
- SQLite errors and busy duration
- Cleanup rows/duration/errors
- Process health and database size

## 12. Deployment

### 12.1 Container

- Multi-stage build compiles frontend then Go binary with embedded assets.
- Runtime image contains only required CA/timezone data and the non-root application.
- `/data` is the persistent mount.
- Graceful shutdown receives hosting-platform termination signals.

### 12.2 Hosted instance

- One instance initially.
- Persistent disk in one region.
- TLS/reverse proxy in front.
- Automated database backup with restore test.
- Do not horizontally scale multiple independent SQLite copies.

### 12.3 Self-hosted

- Docker/Podman command and Compose example.
- Config reference.
- Health check.
- Backup/restore and upgrade documentation.

## 13. Security Boundaries

| Threat | MVP response |
|---|---|
| Server/database reads encrypted content | Client-side AES-GCM; key in fragment only |
| Guess plaintext slug | Accepted risk; unlisted label, expiry, miss limits, no index |
| Stored XSS | Text-safe rendering, CSP, browser tests |
| Preview consumes burn paste | GET confirmation; deliberate POST consume |
| Concurrent consume | Atomic conditional delete returning one row |
| Oversized request | Early body limit and decoded-size validation |
| Spoofed forwarded IP | Trusted-proxy-only parsing |
| Compromised frontend | No third-party scripts, CSP, pinned/audited build; documented residual risk |
| Distributed enumeration | Edge controls and expiry; no claim of complete prevention |

## 14. Verification Strategy

- Go unit tests for validation, slug selection, rate-limit state, and expiry calculation
- SQLite integration tests for schema, retrieval, cleanup, and consume concurrency
- Browser unit tests for Base64url, envelope validation, and crypto vectors
- End-to-end tests for plaintext/encrypted/burn flows and accessibility states
- Go race detector
- Static analysis and dependency audit
- Fuzz tests for slug parsing, payload/envelope decoding, and client-IP header parsing
- Performance tests for 1 MiB and 10,000-line payloads
- Live room authority, reconnect, cursor mapping, presence, password, expiry,
  and WebSocket abuse tests, including race and malformed-message cases

## 15. Deferred Architecture

- PostgreSQL adapter and multi-instance coordination
- Redis/distributed limits
- Object storage and attachments
- User identity and authorization
- MCP product interface; it must reuse the companion CLI library rather than
  introducing another protocol implementation
- Permanent records
- Advanced moderation automation
- Custom rendering engine or virtual scroller
- Video/audio/screen sharing, execution, accounts, saved live rooms, and
  user-visible live history remain outside this extension phase
