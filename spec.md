# 0xbin Product Specification

**Status:** Living specification  
**Domain:** `0xbin.app`  
**Product model:** Public hosted service and open-source self-hosted software  
**Last updated:** 2026-07-29

This file defines the settled product and architecture boundaries. Product requirements live in `docs/PRD.md`; agent-facing implementation design and sequencing live in `agent_docs/`; repository instructions live in `AGENTS.md`.

## 1. Product

0xbin is an ephemeral paste service for sharing text, code, logs, and configuration. Anyone may use the hosted instance at `0xbin.app`; the same application is open source and self-hostable.

Accurate public description:

> 0xbin is an ephemeral paste service with memorable links, automatic expiry, and optional client-side encryption.

Do not describe the entire service as zero-knowledge or private. Those properties apply only to encrypted paste contents.

## 2. Settled Decisions

| Topic | Decision |
|---|---|
| Name and domain | 0xbin at `0xbin.app` |
| Distribution | Hosted service plus self-hosting from the same codebase |
| Default paste mode | Unencrypted |
| Encrypted mode | Optional toggle near the Generate action |
| Encryption key sharing | Key in URL fragment; prompt for key if fragment is absent |
| Paste path | Three concatenated lowercase words: adjective + adjective + noun |
| Example | `radiantcolorfulpomeranian` |
| Path suffix, digits, separators | None |
| Database | SQLite for hosted and self-hosted deployments initially |
| Backend | Go |
| Frontend | React + TypeScript + Vite; MVP design baseline in `agent_docs/FRONTEND.md` |
| Packaging | One Go service with embedded frontend and one SQLite volume |
| Expiry | 1 hour, 1 day, 3 days, or burn after one deliberate read; unopened burn pastes expire after 3 days |
| Redis/PostgreSQL | Not part of the initial design |
| Maximum paste size | 1 MiB initially; raise only after benchmarks |
| Public paste index | None |
| Accounts | Not in MVP; live rooms use temporary session names only |
| Live sharing | Approved post-MVP extension under `/live`, separate from paste semantics |

## 3. URL and Slug Model

Canonical plaintext URL:

```text
https://0xbin.app/radiantcolorfulpomeranian
```

Canonical encrypted sharing URL:

```text
https://0xbin.app/radiantcolorfulpomeranian#<base64url-key>
```

The fragment is part of the browser-visible sharing URL but is not included in the HTTP request. The server receives only `/radiantcolorfulpomeranian`.

### 3.1 Slug generation

- Select two adjectives and one noun with a cryptographically secure random generator.
- Concatenate the selected words without separators.
- Use lowercase ASCII only.
- Maintain curated word lists that avoid offensive, ambiguous, and visually confusing entries.
- The complete generated slug list must contain approximately two million unique combinations.
- Enforce uniqueness with the database primary key.
- Generate, attempt `INSERT`, and retry on a uniqueness violation. Do not use a separate check-then-insert flow.

### 3.2 What two million combinations mean

Two million possibilities provide approximately:

```text
log2(2,000,000) ≈ 20.9 bits
```

This is enough for memorable links and a student-scale ephemeral service, but it is not cryptographic access control.

Collision probability is not based on receiving two million concurrent pastes. If `n` unexpired slugs currently exist, a uniformly generated new slug has approximately `n / 2,000,000` probability of colliding on its first insertion attempt. For example, at 10,000 active pastes the per-attempt collision chance is about 0.5%; retrying resolves it.

The birthday paradox means there is roughly a 50% chance that at least one duplicate attempt has occurred by around 1,665 randomly generated slugs in a two-million-value space. That does **not** mean the next insertion has a 50% collision chance, and it does not cause lost data: the primary-key constraint rejects that one attempt and the service generates another slug. Occupancy, not the historical probability of ever seeing a duplicate, determines the retry cost.

The larger security consideration is enumeration. Rate limiting, missed-slug detection, lack of indexing, and short expiry make scanning costly, but distributed clients can bypass a purely per-IP limit. This is an accepted design tradeoff:

- Unencrypted pastes are **unlisted**, not private or secret.
- Encrypted paste confidentiality comes from the independent 256-bit encryption key, not from the slug.
- Sensitive content should use encrypted mode.

### 3.3 Enumeration mitigations

- No listing, search, or recent-pastes page.
- The sitemap may advertise only intentionally indexable permanent product or
  policy pages. It must never include paste URLs.
- Add `X-Robots-Tag: noindex, nofollow, noarchive` to paste responses.
- Add equivalent robots metadata to rendered pages.
- Apply stricter limits to repeated missing-slug requests than to successful reads.
- Return the same generic not-found response for missing, expired, deleted, and consumed pastes.
- Keep expiration short by default.
- Allow edge-level temporary blocking on the hosted instance.

Do not claim that response timing is perfectly indistinguishable or that rate limiting makes brute-force attacks impossible.

## 4. Encryption

### 4.1 Client-side flow

1. User enables encryption.
2. Browser creates a random 256-bit AES-GCM key with Web Crypto.
3. Browser creates a unique random 96-bit IV.
4. Browser encrypts a structured payload containing title, language, and content.
5. Browser sends only the versioned ciphertext envelope to the server.
6. Server returns the clean three-word URL.
7. Browser appends the encoded key after `#` locally.

The server never receives the key or plaintext.

### 4.2 Viewing flow

- If a valid key is present in `location.hash`, use it locally and remove it from visible application state where practical without breaking reload behaviour.
- If the fragment is absent, show a key-entry dialog.
- A user may paste either the key or a complete encrypted URL into the dialog.
- Never send the key to an API, analytics system, error report, or log.
- Keep the key in memory for the active page only; do not place it in local storage.
- Show one generic decryption error for missing, malformed, wrong, or corrupted key/ciphertext combinations.

### 4.3 Cryptographic format

- Web API: `window.crypto.subtle`
- Algorithm: AES-GCM
- Key: randomly generated 256-bit key
- IV: unique 96-bit random value per encryption
- Encoding: Base64url without padding for the fragment key and binary envelope fields
- Envelope: explicitly versioned

Example envelope:

```json
{
  "version": 1,
  "algorithm": "A256GCM",
  "iv": "base64url",
  "ciphertext": "base64url"
}
```

Encrypted plaintext before encryption:

```json
{
  "title": "optional title",
  "language": "plaintext",
  "content": "paste content"
}
```

The server may validate envelope shape, version, encoding, IV length, and size. It cannot authenticate or decrypt AES-GCM ciphertext without the key; cryptographic validation occurs in the client.

### 4.4 Frontend trust

Client-side encryption depends on trustworthy frontend JavaScript. Therefore the hosted application must use a strict Content Security Policy, no third-party executable scripts, escaped rendering, pinned dependencies, and security-focused tests. A compromised frontend can capture plaintext or keys even when the backend is zero-knowledge.

## 5. Paste Lifecycle

### 5.1 Timed expiry

- Store server-generated UTC timestamps as Unix seconds in SQLite `INTEGER` fields.
- Every read query must filter `expires_at > now`; cleanup is never the access-control boundary.
- Run a cancellation-aware cleanup worker at startup and periodically while the application is running.
- Delete expired rows in short batches.
- If the service is offline, cleanup safely resumes at startup.

SQLite does not require cron. Expiry is enforced by queries, while the Go cleanup worker reclaims storage.

### 5.2 Burn after read

A normal `GET` must not consume a burn-after-read paste because preview bots may open links automatically.

1. `GET /{slug}` returns a non-consuming reveal confirmation.
2. User deliberately selects **Reveal and destroy**.
3. Client sends `POST /api/v1/pastes/{slug}/consume`.
4. One atomic SQLite operation deletes and returns an active paste.
5. The first successful request wins; all others receive generic not found.

For encrypted pastes, selecting Reveal consumes the ciphertext before the browser can prove that the recipient has the correct key. The confirmation UI must explain this.

## 6. Hosted and Self-Hosted Architecture

The initial deployment is one application instance:

```text
Browser or CLI
      |
Reverse proxy / TLS
      |
Go application
  ├── embedded React frontend
  ├── versioned HTTP API
  ├── rate limiter
  ├── expiry worker
  └── SQLite database on persistent storage
```

The hosted deployment and self-hosted distribution use the same binary and migrations. Differences are configuration only: base URL, trusted proxies, rate limits, storage path, allowed expiry values, and administrative controls.

The approved live-sharing extension uses the same binary and SQLite volume with
a process-local room hub and WebSocket transport. It does not require Redis,
PostgreSQL, a broker, or a second service. Multiple application instances are
not supported for live rooms in this phase.

SQLite requirements:

- Persistent volume
- WAL mode where supported by the deployment filesystem
- Busy timeout
- Short write transactions
- Database migrations
- Documented safe backup and restore
- One writer application instance initially

Do not introduce a storage abstraction or PostgreSQL implementation until a second database is actually required. Keep SQL isolated in the storage package so later extraction remains possible.

## 7. Rate Limiting and Abuse

- Creation: configurable strict per-IP limit; hosted starting point 15 per hour with a small burst policy.
- Successful reads: substantially more generous.
- Missing-slug requests: stricter rolling limit and consecutive-miss detection.
- Consume and deletion operations: per-IP and per-slug protection.
- Health checks: exempt.
- Periodically evict inactive limiter entries to bound memory.
- Trust forwarded IP headers only from explicitly configured proxies that overwrite client-supplied values.

Before public launch, provide an abuse contact, administrative paste deletion, emergency creation shutdown, storage/bandwidth alerts, and a basic acceptable-use and privacy policy.

The hosted `0xbin.app` shell exposes permanent Who & why, Terms & conditions,
and Privacy & reports pages through an accessible bottom-left menu. Together
they cover maintainer information, privacy, acceptable use, service terms,
security, and abuse reporting. These operator-specific destinations are enabled
from the configured hosted origin and remain absent from self-hosted navigation.

## 8. Frontend Behaviour

The MVP interaction and visual baseline is defined in
[`agent_docs/FRONTEND.md`](agent_docs/FRONTEND.md). That document may refine
implementation detail without changing the security and lifecycle behaviour
settled here.

Required behaviours:

- Create page with editor, optional title, language selection, expiry, burn option, Generate action, and encryption toggle near Generate.
- Successful creation copies the sharing URL and opens the viewer with a
  minimal confirmation. The viewer shows lifetime information for view-once
  and one-hour pastes; ordinary one-day views do not need persistent expiry
  copy.
- Plaintext viewer with copy, raw/download, search, and permanent line
  wrapping. Horizontal scrolling and a wrap toggle are not part of the MVP.
- Encrypted viewer that reads the fragment key or prompts when missing.
- Burn confirmation page that does not fetch content until deliberate reveal.
- Clear loading, success, validation, rate-limit, not-found, decryption-error, and service-error states.
- Full keyboard operation, visible focus, form labels, screen-reader status announcements, sufficient contrast, and reduced-motion support.
- User content must never execute as HTML or JavaScript.

Use CodeMirror 6 for editing and, after benchmarking, potentially for read-only viewing. Automatic language detection and hand-written virtualization are not MVP requirements.

### 8.1 Live-sharing extension

Live sharing is an optional post-MVP mode. It is a separate room namespace and
does not change paste creation, retrieval, encryption, expiry, burn, or URL
semantics.

- LiveBin is part of the same one-service deployment but can be disabled by a
  self-hosted operator, leaving the bare paste service available without live
  routes or room workload.

- `/live` creates a temporary live room and `/live/{slug}` opens one.
- A room defaults to a 24-hour lifetime from server-side creation; a
  self-hosted operator may configure a shorter lifetime, but never a longer
  one. Editing or joining never extends expiry.
- A room starts with one CodeMirror document tab and supports up to the
  configured tab limit with existing language modes.
- Multiple participants edit the same documents in real time, including
  visible temporary cursors and selections in the active tab.
- Each participant receives an adjective+noun temporary display name and can
  rename it during the session. Presence, colours, cursors, selections, joined
  time, and connection state are session-only.
- Live room content is server-readable plaintext. Client-side AES-GCM paste
  encryption is not used for live rooms.
- An optional shared password gates entry. It is access control, not
  end-to-end encryption. No account or recovery flow is required.
- The room creator uses a room-scoped temporary session, not an account, to
  switch the room to watch-only mode and remove active collaborator sessions.
- A room allows up to 10 writing participants, up to 100 additional
  watch-only viewers, and 110 total connected participants.
- Participants can save either the current tab or every tab as one normal
  paste; the normal paste flow then controls expiry, encryption, and burn.
- The server is the collaboration authority over WebSockets. P2P/WebRTC is
  not part of the live extension.
- Live room documents and bounded synchronization history use separate tables
  and routes from `pastes`; active presence is not stored in SQLite.
- The live UI keeps the existing editor-first visual baseline and notification
  treatment. Technical descriptions do not become unnecessary frontend copy.
- The existing static paste loading state may use the same minimal top progress
  bar, without changing paste API or security semantics.

The LiveBin header action carries an unsaved create-page draft into a live
draft in browser memory. From an existing paste viewer it opens a blank live
draft and never automatically transfers viewed or decrypted content.

## 9. Initial Data Model

SQLite schema direction:

```sql
CREATE TABLE pastes (
    slug             TEXT PRIMARY KEY,
    payload          TEXT NOT NULL,
    is_encrypted     INTEGER NOT NULL CHECK (is_encrypted IN (0, 1)),
    crypto_version   INTEGER,
    burn_after_read  INTEGER NOT NULL CHECK (burn_after_read IN (0, 1)),
    content_size     INTEGER NOT NULL CHECK (content_size >= 0),
    expires_at       INTEGER NOT NULL,
    created_at       INTEGER NOT NULL,
    CHECK (
      (is_encrypted = 0 AND crypto_version IS NULL) OR
      (is_encrypted = 1 AND crypto_version IS NOT NULL)
    )
) STRICT;

CREATE INDEX idx_pastes_expires_at ON pastes(expires_at);
```

`payload` is a versionable plaintext JSON payload or encrypted-envelope JSON. Do not store encrypted titles or language values in separate plaintext columns. View counters and accounts are excluded from the MVP.

## 10. API Direction

```text
POST   /api/v1/pastes
GET    /api/v1/pastes/{slug}
POST   /api/v1/pastes/{slug}/consume
GET    /api/v1/pastes/{slug}/raw
POST   /api/v1/live
GET    /api/v1/live/config
GET    /api/v1/live/{slug}
POST   /api/v1/live/{slug}/unlock
GET    /api/v1/live/{slug}/ws   (WebSocket upgrade)
GET    /health/live
GET    /health/ready
```

All API errors use a stable JSON shape and request ID. Raw access applies to
plaintext pastes; encrypted clients fetch the envelope and decrypt locally.
Live bootstrap/unlock responses use `no-store` and no-index headers. The live
WebSocket message contract is maintained in the live-sharing implementation
plan and does not alter the paste API contract.

## 11. Security Baseline

- Strict CSP and no third-party executable scripts
- HSTS after HTTPS is confirmed stable
- `X-Content-Type-Options: nosniff`
- Restrictive referrer and permissions policies
- Request/header/read/write/idle timeouts
- Request-body limit enforced before unbounded allocation
- Parameterized SQL
- No paste content, fragment keys, or decrypted metadata in logs
- Non-root minimal container
- Dependency, static-analysis, race, unit, integration, and browser tests
- Generic public error messages; structured internal diagnostics without content

## 12. MVP Boundaries

Included:

- Plaintext and encrypted text pastes
- Three-word slugs
- Timed expiry and safe burn-after-read
- SQLite cleanup worker
- Hosted and self-hosted packaging
- Basic syntax-aware editing/viewing
- Rate limits and baseline abuse operations

Deferred:

- Accounts, comments, revisions, public discovery, custom slugs
- Permanent hosted pastes
- File/image upload
- PostgreSQL, Redis, multiple application instances
- CLI or MCP protocol changes; the companion CLI is maintained in the separate
  [`0xbin-cli`](https://github.com/0atxl/0xbin-cli) repository
- Automatic language detection if it delays the core flow
- Custom virtual-scroll implementation

The initial paste MVP remains the scope described above. The approved live
sharing extension is delivered as a separate post-MVP phase and must pass its
own collaboration, security, expiry, accessibility, and self-hosting gates.

## 13. Acceptance Summary

The MVP is ready for public beta when:

- A user can create and share a plaintext paste without instructions.
- An encrypted paste round-trips without key or plaintext appearing in server traffic or logs.
- A URL lacking its fragment prompts for and accepts the correct key.
- Expired content is never returned, regardless of cleanup timing.
- Exactly one concurrent burn-after-read consume succeeds.
- Slug collisions retry safely through the primary-key constraint.
- Arbitrary paste content cannot execute in the application.
- A self-hoster can run one documented container with one persistent volume.
- Backup restore and upgrade migrations have been tested.

The live-sharing extension is ready for release only when its separate
implementation plan passes: concurrent editing converges, reconnect and
resynchronization preserve acknowledged work, cursors and selections map
correctly, optional password gates protect every access path, rooms expire and
clean up at their configured lifetime (never longer than 24 hours), presence remains session-only, and the existing paste
journeys remain unchanged apart from the approved loading-bar visual update.
