# 0xbin Live Sharing Extension

## Implementation Plan

**Status:** Implemented through Steps 0A–13. Follow-up release-hardening Phases
0–10 in
[`LIVE_SHARING_REMEDIATION_PLAN.md`](LIVE_SHARING_REMEDIATION_PLAN.md) are
complete. The approved browser-identity and creator-authority evolution is
planned separately in
[`LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md`](LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md)
and its contract, durable-storage, additive-identity, grouped-connection,
lock-authority, kick-removal, minimal frontend-wiring, final design,
compatibility/documentation, and bounded code-quality Phases 0–7A are
complete. Phase 8, the final independent release audit, is next and has not
started.

**Related:** [`spec.md`](../spec.md), [`docs/PRD.md`](../docs/PRD.md),
[`agent_docs/TECHNICAL_DESIGN.md`](TECHNICAL_DESIGN.md),
[`agent_docs/IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md)

This plan adds a live collaborative editor beside the existing paste service.
The existing paste routes, payloads, encryption envelope, expiry choices, and
burn-after-read semantics remain unchanged. Live sharing is a separate room
namespace and a separate storage/API surface.

LiveBin remains an optional mode of the same one-service deployment. A
self-hosted operator can disable live routes and run the bare paste service
without the live UI or room workload. The live transport is server-authoritative
WebSocket fan-out; it deliberately does not introduce P2P/WebRTC, accounts,
video, audio, screen sharing, end-to-end encryption, terminal execution, file
uploads, permanent storage, or a second service.

## 1. Settled Product Contract

### 1.1 Entry point and routes

- Add a `LiveBin` control to the normal application header immediately to
  the left of the existing theme toggle.
- The control is available from the normal create/viewer shell and is hidden
  by focused security gates in the same way as the existing shell controls.
- From the normal create editor, `LiveBin` carries the current unsaved
  title, selected language, and content in browser memory into the first live
  tab. It does not create or upload a room until `Create LiveBin room` is
  pressed.
- From any existing paste viewer, `LiveBin` opens a blank live-room creator.
  It never automatically copies or uploads viewed content, including decrypted
  encrypted-paste content.
- `/live` opens the live-room creation flow.
- `/live/{slug}` opens a live room.
- Live URLs use the existing three-word generator but keep the `/live/`
  namespace. A live slug and a paste slug may therefore have the same words
  without colliding, because their routes are different.
- Live pages are unlisted and receive `noindex, nofollow, noarchive` metadata.
- The existing `/{slug}` paste route is not changed.

### 1.2 Room behaviour

- A room starts with one document tab.
- Users can add, rename, delete, and reorder tabs.
- Every tab has a name, one of the existing CodeMirror language identifiers,
  and its own shared text document.
- The active tab is local to each browser. It is not a shared room setting.
- All tabs are plaintext/server-readable. The live feature does not use the
  existing client-side AES-GCM paste mode.
- The default room lifetime is 24 hours from server-side creation time; an
  operator may configure a shorter lifetime. Editing or joining does not
  extend expiry.
- Expiry is enforced on room bootstrap, password unlock, WebSocket upgrade,
  and every accepted room operation. Cleanup only reclaims storage.
- Anyone with the room URL can join an unprotected room. Collaborator capacity
  and the durable room lock determine effective editing ability.
- One browser profile represents one room participant across reload, reopen,
  service restart, and multiple normal tabs. Incognito/private profiles, other
  profiles, other devices, cleared site data, and other origins join as separate
  participants. Each tab keeps a separate connection, operation client, active
  tab, and cursor beneath the shared browser participant.
- The creator receives a room-scoped random capability at creation time. It is
  not an account or transferable ownership record. Only its hash is stored in
  SQLite, so the HttpOnly cookie retains authority across service restart until
  room expiry; site-data loss has no recovery path.
- The creator can lock and unlock collaboration while remaining editable.
  Collaborators keep their category and pause/resume editing with the lock;
  viewers remain read-only. Active participant removal and individual role
  management are not part of the product.
- A room supports at most 10 collaborator-capacity browser participants
  including the creator, up to 100 additional viewers, 110 total browser
  participants, and 8 simultaneous tab connections per participant.
  These are hard room bounds; request and message rate limits remain
  operator-configurable. A hosted deployment may add Cloudflare or another
  edge limiter, while a personal self-hosted deployment may choose lenient
  application limits.
- A participant can save room content as a normal static paste. The action
  offers `Current tab` or `Every tab`; the latter appends all tabs in one paste
  with clear tab separators. It returns to the normal paste flow so the user
  can choose expiry, encryption, and burn-after-read settings before upload.
- The server automatically assigns each participant a temporary display name
  made from one existing adjective and one existing noun, displayed as two
  readable title-cased words such as `Quiet Otter`.
- Generated names are unique among active participants in that room; reroll on
  collision instead of adding digits. A participant can rename themselves
  from the participant popover, and renamed values are also case-insensitively
  unique within the active room.
- Nicknames are room-scoped browser identity, not accounts or cross-device
  identities.
- A room may optionally require one shared password. Password protection gates
  entry; it does not encrypt the room content from the server or database
  operator.
- A lost room password cannot be recovered or reset without accounts. The room
  expires normally.

### 1.3 Presence and network state

Active presence is ephemeral process memory, not durable room data. A random,
room-scoped browser credential in `localStorage` produces the stable public
participant ID and colour; the browser also retains the last authoritative
nickname. The live hub groups up to eight tab connections beneath that
participant. It tracks joined time, heartbeat, connection status, active tab,
cursor/selection, and operation client identity per connection, sends one
participant roster row, and broadcasts connection-specific presence.

SQLite stores the room/document state, creator-token hash, and room lock, not
the browser credential or active participant roster. This avoids stale users
and a database write per heartbeat. A process restart clears active presence;
the browser credential reconstructs the same participant ID, colour, and
validated nickname when tabs reconnect.

The UI exposes:

- For the local user: Connecting, Connected, Reconnecting, and Offline
- For other participants: Connected, Connection lost, and Offline

The status control opens on hover, click, and keyboard focus. Its popover
shows each participant's nickname, current connection state, and session join
time. A browser cannot reliably know whether another participant is actively
trying to reconnect, so the UI must not label remote users as Reconnecting or
claim to measure anyone's Wi-Fi quality.

Participants in the same active tab see each other's cursor and selection as
restrained CodeMirror decorations. The colour remains stable for the session
even if the nickname changes. Show a small nickname label while that remote
participant is active and fade the label when idle; keep the caret/selection
available without covering code. Hide remote cursor decorations when the
participant switches tabs, disconnects, or becomes stale. Cursor and selection
data is ephemeral presence and is never written to SQLite or change history.

### 1.4 Explicit non-goals

- Signup, login, accounts, ownership transfer, cross-device identity, or
  creator recovery after site-data loss
- Per-user permissions, user-selected view-only access, participant removal,
  promotion, demotion, or banning; the creator's reversible room lock remains
  part of the settled contract
- Saved rooms, user-visible version history, revisions, or forks (internal
  synchronization counters and bounded rebase history are not product
  versioning)
- Video calls, audio calls, screen sharing, or WebRTC
- Code execution, terminals, previews, or language servers
- Client-side encryption for live rooms
- Public room listings or room search
- Shared scroll position or shared active-tab selection
- Draggable/resizable tmux-style split panes in the initial UI

Tabs are the initial multi-document UX. A split-pane layout can be considered
later without changing the room data model.

### 1.5 Frontend content and visual discipline

The live UI must feel like a small, native extension of the current deployed
0xbin interface. Keep the existing visual density, design tokens, editor-first
layout, and restrained header. Do not turn the feature into a landing page or
product demo.

Visible text must either label an action/input, report actionable state, or
help the user make an immediate choice. If removing a sentence, badge, panel,
or placeholder does not make the feature harder or less safe to use, remove
it. In particular, do not add:

- `Alpha`, `Beta`, `local-first`, `browser-first`, `collaborative`,
  `unencrypted`, `plaintext`, `server-readable`, or similar product/technical
  badges and slogans
- Marketing headings, feature descriptions, onboarding cards, explanatory
  architecture text, “powered by” copy, or repeated descriptions of what live
  sharing does
- Fake participants, sample documents, decorative empty states, skeletons
  that remain after loading, “coming soon” controls, or disabled future
  features
- Redundant helper text, tooltips that repeat visible labels, or input
  placeholders when a proper visible label already explains the field
- Permanent notices for facts that belong in privacy, security, or
  self-hosting documentation

`Plain text` remains valid inside the language selector because it names a
functional editor mode; it must not be presented as a promotional or security
label. Necessary errors, expiry, password entry, connection state, participant
state, and destructive-action confirmation remain concise and contextual.
Security and storage details stay accurate in project/privacy documentation
without becoming persistent editor chrome.

Live-room feedback must reuse the current 0xbin notification and warning
language, components, and visual treatment defined in `agent_docs/FRONTEND.md`.
Do not create a second live-specific toast, alert, banner, or warning design.
Transient results use the existing compact top-right toast stack with the same
accent treatment, six-second lifetime, progress bar, close action, hover/focus
pause behavior, and ARIA live announcements. Blocking states such as an
expired or unavailable room remain persistent in the page; field errors stay
beside their field; destructive confirmations use the existing restrained
warning treatment. Copy stays short, direct, and consistent with messages
such as `Link copied` and `Could not create paste — try again`.

Use the shared top progress bar for actual loading/connection work only:
initial static-paste retrieval, initial live-room bootstrap, first WebSocket
connection, reconnect, and HTTP resynchronization. Replace the current visible
`Loading paste…` placeholder with this bar while retaining a visually hidden
live-region announcement. Hide the bar before rendering a persistent error or
unavailable state. Do not run it for keystrokes, cursor movement, copy actions,
or other already-optimistic interactions. Under reduced motion, show stable
progress without continuous decorative animation.

## 2. Architecture

```text
Browser
  ├── React live-room shell
  ├── CodeMirror 6 editors, one per tab
  ├── collaboration client and reconnect state
  └── temporary presence identity
          │ HTTPS + WebSocket
          ▼
Go application
  ├── live HTTP handlers
  ├── password gate and room session registry
  ├── live room hub and collaboration authority
  ├── ephemeral presence registry
  ├── expiry/cleanup integration
  └── SQLite live-room snapshots and bounded change history
```

The live hub is process-local. The initial deployment remains one Go process
and one SQLite database. Do not add Redis, PostgreSQL, a message broker, or a
second runtime. Multiple application instances are not supported by this
extension.

### 2.1 Frontend technology

- Reuse React, TypeScript, Vite, existing design tokens, the language registry,
  and CodeMirror 6 language loaders.
- Add `@codemirror/collab` for the editor-side collaborative state and local
  operational transformation. The official CodeMirror collaboration model
  uses a central authority, document revisions, `sendableUpdates`,
  `receiveUpdates`, and rebasing for concurrent changes:
  <https://codemirror.net/examples/collab/>.
- Add a maintained Go WebSocket package. The initial choice is
  `github.com/coder/websocket`; it is a small, context-aware package with
  JSON helpers, ping support, close handling, and no runtime dependencies:
  <https://github.com/coder/websocket>.
- Add a maintained Argon2id implementation for room-password hashing. Do not
  implement password hashing or use a fast hash such as SHA-256.
- Add a focused TypeScript live-socket client and React hooks that provide the
  useful behavior of a Phoenix Channels client/LiveView helpers: connection
  state, join/rejoin, message references, acknowledgements, heartbeat,
  bounded queues, and exponential reconnect backoff with jitter. Do not add
  Phoenix or LiveView packages and do not make the Go server speak the Phoenix
  wire protocol.
- Add `topbar` as the shared minimal loading indicator. Configure it with the
  existing accent and surface tokens and a roughly 200 ms delayed show so fast
  requests do not flash. Use one shared loading coordinator rather than
  route-specific progress-bar implementations.

### 2.2 Collaboration authority

The server is the authority for ordering accepted changes. Every tab has an
independent document revision. Room metadata has a separate revision for tab
create, rename, language, delete, and reorder operations. Editing one tab must
not advance another tab's revision. A client submits serialized CodeMirror
changes with the tab ID, the document revision it last synchronized, and its
collaboration client ID.

The authority must:

1. Validate the tab, client, revision, change-set shape, inserted byte size,
   and operation limits.
2. Accept changes based on the current revision.
3. Rebase concurrent client updates over accepted history when the submitted
   revision is behind.
4. Apply accepted changes to the authoritative document.
5. Assign a monotonically increasing revision in the affected stream: the
   room-metadata stream or one document stream.
6. Persist the accepted operation before sending its acknowledgement or
   broadcasting it as accepted.
7. Broadcast accepted updates and acknowledgements.
8. Persist snapshots and bounded change history without exposing internals in
   public errors.

Do not implement collaboration as last-write-wins full-document replacement.
That would silently discard concurrent edits.

The first engineering gate is a focused Go compatibility spike for the
CodeMirror change-set JSON format and rebase behaviour. Generate fixtures in
TypeScript and replay them through Go for insert/insert, insert/delete,
overlapping replace, multi-range, empty-document, and Unicode cases. Do not
start the full UI until the authority passes those fixtures. If a correct
wire-compatible Go authority cannot be kept small and well-tested, stop and
select a maintained CRDT/OT implementation before proceeding; do not replace
it with last-write-wins logic.

The internal revision counter is a synchronization mechanism, not a new
public API or product version. The live endpoints remain under `/api/v1`.

### 2.3 WebSocket message families

Use a typed JSON message envelope with bounded payloads. Message types are
part of the live-room contract; unknown types are rejected without crashing
the connection. The complete initial room snapshot is loaded with the HTTP
bootstrap endpoint, not sent through the WebSocket. This avoids a custom
chunking protocol for a room that may be larger than the WebSocket frame
limit.

Client to server:

- `join`: browser participant credential, per-page connection/client IDs,
  optional last authoritative nickname, and last-known metadata/document
  revisions; the server returns the grouped participant identity and effective
  editing state
- `push_changes`: document ID, base revision, serialized change sets
- `document_create`: requested tab name and language
- `document_update`: tab name/language/order changes
- `document_delete`: document ID
- `document_reorder`: ordered document IDs
- `presence`: current tab, document revision, cursor/selection anchor and head
- `participant_rename`: requested temporary display name
- `room_watch_only`: creator-only durable lock or unlock request
- `ack`: highest metadata revision and per-document revisions the client has
  applied

Server to client:

- `joined`: room expiry, current revisions, and presence, followed by any
  retained changes newer than the client's HTTP snapshot
- `changes`: accepted document updates and the resulting revision
- `document_created`, `document_updated`, `document_deleted`,
  `document_reordered`
- `presence_snapshot`
- `presence_joined`, `presence_updated`, `presence_left`,
  `participant_renamed`
- `status`: synchronized, HTTP resync required, rate limited, or expired
- `error`: stable public error code without content or password details

The HTTP bootstrap response has its own bounded response limit and includes
the full document snapshot plus metadata/document revisions. Individual
WebSocket updates remain within the frame limit. If the changes needed to
bridge the HTTP-to-WebSocket race have already been compacted, the server
sends `HTTP resync required`; the client fetches a fresh HTTP snapshot and
joins again.

### 2.4 Connection lifecycle

- Validate the request `Origin` against the configured public origin before
  accepting a WebSocket.
- Require the room password session before the upgrade for protected rooms.
- Use one reader and one writer per connection; never write concurrently to a
  connection without the library's supported mechanism.
- Set a maximum frame size and per-room message budget.
- Send periodic server pings and require pong/heartbeat progress.
- Mark one connection lost after its bounded heartbeat timeout. Remove the
  participant's transient presence only after its final connection has been
  lost for the reconnect-grace interval.
- Close all live connections during application shutdown and flush pending
  room snapshots before closing SQLite.
- Configure the reverse proxy to pass WebSocket upgrades and allow an idle
  timeout longer than the heartbeat interval.
- Do not rely on the normal HTTP write/idle timeout as the WebSocket lifecycle
  mechanism. Use connection-specific deadlines and cancellation.

## 3. Data Model

Add a new migration; do not alter the existing `pastes` table.

The schema direction is:

```sql
CREATE TABLE live_rooms (
    slug TEXT PRIMARY KEY,
    password_hash TEXT,
    content_size INTEGER NOT NULL CHECK (content_size >= 0),
    metadata_revision INTEGER NOT NULL CHECK (metadata_revision >= 0),
    metadata_snapshot_revision INTEGER NOT NULL
        CHECK (metadata_snapshot_revision >= 0),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE live_documents (
    room_slug TEXT NOT NULL REFERENCES live_rooms(slug) ON DELETE CASCADE,
    document_id TEXT NOT NULL,
    name TEXT NOT NULL,
    language TEXT NOT NULL,
    content TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    current_revision INTEGER NOT NULL CHECK (current_revision >= 0),
    snapshot_revision INTEGER NOT NULL CHECK (snapshot_revision >= 0),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (room_slug, document_id)
) STRICT;

CREATE TABLE live_changes (
    room_slug TEXT NOT NULL REFERENCES live_rooms(slug) ON DELETE CASCADE,
    stream_kind TEXT NOT NULL CHECK (stream_kind IN ('metadata', 'document')),
    stream_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    change_kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (room_slug, stream_kind, stream_id, revision)
) STRICT;

CREATE INDEX idx_live_rooms_expires_at ON live_rooms(expires_at);
CREATE INDEX idx_live_changes_stream_revision
    ON live_changes(room_slug, stream_kind, stream_id, revision);
```

The exact migration may use a room-level metadata document for tab create,
delete, and reorder events, but the invariants remain:

- Room and document content is server-readable and never logged.
- Passwords are represented only by an adaptive password hash.
- Timestamps are UTC Unix seconds, matching existing storage.
- Active presence and short-lived room-password sessions are process memory
  only. The room stores its creator-token hash and lock state; the browser
  stores its room-scoped participant credential and last authoritative
  nickname.
- Room metadata and each document have independent revision streams.
- Metadata changes use one reserved, non-user document `stream_id`; document
  changes use their stable server-generated document ID.
- Change history is bounded and compacted into current metadata/document
  snapshots.
- Expired room deletion cascades to documents and changes.

Use short SQLite transactions. Batch accepted changes for at most 50-100 ms so
normal typing does not create one transaction per browser key event. Commit
the batch before acknowledging or broadcasting acceptance. The editor may
show a user's local unacknowledged input immediately, but a server crash must
not lose an update that the server already acknowledged. On failed commit,
leave updates unacknowledged so clients can retry after resynchronization.

Keep the active authority in memory and persist a current snapshot plus enough
recent history for reconnect/rebase. The compaction policy must have a hard
byte and row bound; if a room exceeds its operation budget, temporarily stop
edits and return a clear room-limit state rather than allowing unbounded
growth.

Initial safety defaults, subject to benchmark confirmation:

- 8 tabs per room
- 1 MiB aggregate document content per room
- The aggregate content setting also bounds each individual document; there is
  no independent per-document operator setting.
- 10 collaborator-capacity browser participants per room, including the creator
- 100 additional viewers per room
- 110 total browser participants per room
- 8 simultaneous tab connections per browser participant
- 32 UTF-8 bytes for nicknames
- 64 UTF-8 bytes for tab names
- 64 UTF-8 bytes for language identifiers
- 64 KiB maximum individual WebSocket message
- 24-hour room lifetime

## 4. HTTP and Authentication Contract

Add these routes without changing existing paste routes:

```text
POST /api/v1/live
GET  /api/v1/live/config
GET  /api/v1/live/{slug}
POST /api/v1/live/{slug}/unlock
GET  /api/v1/live/{slug}/ws       WebSocket upgrade
```

### 4.1 Create

`POST /api/v1/live` accepts:

```json
{
  "password": "optional shared password",
  "documents": [
    {
      "name": "main",
      "language": "plaintext",
      "content": ""
    }
  ]
}
```

The server validates all limits, generates the live slug, calculates the
24-hour expiry, hashes the optional password, and returns:

```json
{
  "slug": "quietbrightotter",
  "url": "https://0xbin.app/live/quietbrightotter",
  "expires_at": "2026-08-06T12:00:00Z",
  "password_required": true
}
```

The response never echoes the password, password hash, creator token, or
creator-token hash. Creation issues the raw random creator capability only in a
room-scoped HttpOnly cookie and stores only its domain-separated SHA-256 hash in
SQLite. A protected room also receives the short-lived room-access cookie so
the creator is not immediately asked to enter the same password again.

### 4.2 Bootstrap and unlock

- Unprotected `GET /api/v1/live/{slug}` returns the room snapshot and expiry.
- Protected `GET` returns only a password-required response; it does not
  return documents, tab names, language metadata, or participants.
- `POST /api/v1/live/{slug}/unlock` accepts the password over HTTPS, verifies
  the stored Argon2id hash, and issues a short-lived, room-scoped,
  `HttpOnly`, `SameSite=Strict` session cookie.
- Set the cookie's `Secure` attribute whenever the public origin uses HTTPS;
  allow a non-Secure cookie only for explicit local HTTP development.
- The room-access cookie is not an account credential and is not stored in
  SQLite. The separate creator cookie is also not an account credential; only
  its high-entropy token hash is durable through room expiry.
- The WebSocket handshake accepts a protected room only with that session.
- Passwords never appear in URLs, query strings, local storage, analytics,
  request IDs, error reports, or logs.
- Rate-limit unlock attempts by both client identity and room slug.
- Bound concurrent Argon2id checks as well as request frequency so password
  guesses cannot exhaust server memory or CPU.
- A wrong password exposes no room content. Public response wording should
  remain generic enough not to become a room-content oracle.

### 4.3 Errors and headers

Use the existing stable JSON error envelope and add live-specific codes such
as `password_required`, `invalid_password`, `room_limit_reached`,
`room_expired`, `message_too_large`, and `connection_limit_reached`.

All live bootstrap, unlock, and WebSocket-adjacent HTTP responses use:

- `Cache-Control: no-store`
- `X-Robots-Tag: noindex, nofollow, noarchive`
- `X-Content-Type-Options: nosniff`

Do not log room bodies, serialized changes, passwords, password sessions,
nicknames when they can identify a person, or complete WebSocket frames.
Aggregate room counts, connection counts, latency, and error categories are
acceptable operational metrics.

## 5. Ordered Implementation Work

### Step 0A — Branch and commit workflow

Implement the extension on a dedicated branch created from the latest stable
`main` branch:

```text
feature/live-sharing
```

Keep `main` deployable and do not mix unrelated cleanup or redesign work into
this branch. Make focused commits at the major verification gates so each
stage can be reviewed or reverted independently:

1. Documentation alignment and live-room contract
2. CodeMirror collaboration compatibility prototype
3. Dependencies, configuration, and SQLite storage
4. Room authority, presence, and WebSocket transport
5. Frontend routes, header entry point, and shared loading bar
6. Live creation flow and draft handoff
7. Multi-tab editor, cursors, selections, and participant popover
8. Password hardening, expiry, cleanup, and shutdown behavior
9. Security, accessibility, performance, browser tests, and release docs

Before starting implementation, inspect the branch and working tree:

```text
git status --short
git branch --show-current
```

If the tree is clean and you are on `main`, update it and create the feature
branch:

```text
git pull --ff-only
git switch -c feature/live-sharing
```

If the tree is already dirty, identify and preserve those changes first. Do
not switch branches or pull over uncommitted work blindly. The live-plan
documentation changes may become the first focused commit on the feature
branch; unrelated work must remain separate. After the branch is created:

```text
git status --short
```

If the working tree contains existing user changes, preserve them and do not
silently include them in the first live-sharing commit. Commit only the files
belonging to the current stage. The branch may be merged only after the full
verification suite passes and the existing paste API, encryption, expiry,
burn, rendering, and action behavior remain unchanged.

### Step 0 — Contract and documentation baseline

Before code changes:

1. Inspect the companion `0xbin-cli` contract and confirm that existing paste
   create/retrieve/consume/encryption behavior is unaffected.
2. Record this extension as a separate post-MVP product mode in `spec.md` and
   `docs/PRD.md`. Preserve the historical MVP scope and explain that live
   sharing is a later extension instead of silently deleting the old
   non-goal/deferred decision.
3. Add the live-room architecture and security boundary to
   `agent_docs/TECHNICAL_DESIGN.md`.
4. Add this plan to the agent documentation index and add a dedicated live
   sharing phase to `agent_docs/PHASES.md`.
5. Update `agent_docs/FRONTEND.md` for the shared top loading indicator and
   LiveBin handoff rules. Record the static viewer loading-bar replacement
   as the only intentional change to an existing paste journey.
6. Keep the existing paste API and encrypted envelope unchanged.

**Gate:** The documentation agrees on `/live/{slug}`, plaintext room content,
optional password gating, 24-hour expiry, tabs, temporary presence, no
accounts, and no media features.

### Step 1 — Collaboration authority spike

1. Add `@codemirror/collab` in a small browser fixture.
2. Generate serialized ChangeSet fixtures for concurrent edits, Unicode,
   deletes, replacements, and multiple ranges.
3. Implement the minimum Go ChangeSet decode/apply/map/rebase authority needed
   by the official CodeMirror protocol.
4. Test that Go-produced results match browser `ChangeSet` results byte for
   byte and converge for all peers.
5. Include stale revision, duplicate update, malformed change, and oversized
   change cases.
6. Add concurrent cursor/selection fixtures. Verify remote positions map
   correctly through accepted inserts, deletes, rebases, tab switches, and
   resynchronization without affecting document convergence.

**Gate:** Two or more browser peers converge on the same document under
concurrent editing and reconnect without a last-write-wins data loss path.
This is a hard stop/go gate: if the Go compatibility layer is not small,
understandable, and correct under the fixtures, select a maintained CRDT/OT
library and revise the technical design before building the full feature.

**Step 1 result (2026-08-05):** The spike passes. `@codemirror/collab` is
installed in the frontend, browser-generated fixtures are written to
`tests/livecollab/fixtures.json`, and `internal/livecollab` provides the
minimum JSON decode, UTF-16-safe apply, mapping, selection mapping, stale
revision rebase, duplicate retry, and inserted-byte accounting needed for the
compatibility gate. Go replays the browser fixtures for insert/insert,
insert/delete, overlapping replacement, multi-range, empty-document, and
Unicode edits. The tests also cover malformed JSON, stale revisions, changed
duplicate operation IDs, reconnect-style duplicate retries, and cursor/
selection mapping.

This is deliberately still a prototype: it has no HTTP handlers, WebSocket
transport, SQLite persistence, room limits/configuration, or production
history compaction. Those belong to the following steps. Before using the
authority in the live feature, repeat the fixture suite through the eventual
wire envelope and add property/fuzz coverage around the configured limits.

### Step 2 — Dependencies, configuration, and shared limits

1. Add the selected WebSocket package, password-hashing package, CodeMirror
   collaboration package, and `topbar`.
2. Add validated configuration for live room lifetime, maximum tabs, total
   bytes, collaborator/viewer/total browser-participant bounds, message size,
   global and per-participant connection limits, heartbeat, unlock attempts,
   and snapshot/compaction bounds.
   `OXBIN_LIVE_MAX_BYTES` is the one content budget: it applies to the room
   aggregate and therefore to every individual document. The public
   `max_document_bytes` field is an equal compatibility/semantic alias, not a
   second operator control.
3. Extend the rate limiter with live creation, unlock, connection, and message
   categories, or add a focused live limiter that shares the existing bounded
   registry principles.
4. Add input-validation helpers for nicknames, tab names, language IDs,
   document IDs, and room operations.
5. Reuse the reviewed adjective/noun wordlists through a focused participant
   name generator. Generate one adjective plus one noun, reroll active-room
   collisions, and validate renamed display names using the same byte and text
   safety rules.

**Gate:** Invalid configuration fails at startup; all live limits are tested;
no existing paste configuration default changes unexpectedly.

**Step 2 result (2026-08-05):** The selected WebSocket, Argon2id, CodeMirror
collaboration, and topbar dependencies are installed. Live configuration,
rate-limit categories, bounded label/content validation, supported language
validation, document-order validation, and adjective+noun participant-name
generation are implemented and covered by focused tests. The standard `make
test` and `make test-race` targets regenerate the browser-owned collaboration
fixtures and fail on fixture drift before running Go tests. Full repository
lint, unit, race, frontend, formatting, and build verification passed.

This step adds no live routes, room storage, WebSocket handlers, password gate,
or frontend live UI. Those remain intentionally deferred to the following
steps.

### Step 3 — SQLite migration and live store

1. Add the ordered live-room migration.
2. Implement `CreateRoom`, `GetRoomSnapshot`, `SaveSnapshot`,
   `AppendChanges`, `LoadChangesSince`, `CompactChanges`, and
   `DeleteExpiredRooms` behind a focused interface.
3. Enforce expiry in every live read, unlock, mutation, and cleanup query.
4. Use parameterized SQL, foreign keys, WAL, short transactions, and bounded
   change compaction.
5. Test persistence/reopen, password hash presence without plaintext password,
   cascade deletion, expired access, snapshot compaction, and slug handling.
6. Test that room metadata and each document advance independently, and that
   an accepted update is committed before its acknowledgement is emitted.

**Gate:** A room with multiple tabs survives process restart with its content,
language metadata, expiry, and password requirement intact; presence does not
survive or appear in the database.

**Implementation result (2026-08-05):** Added migration `002_live_rooms.sql`
with separate room, document, and bounded change-history tables, foreign-key
cascades, expiry indexing, and strict SQLite constraints. Added the focused
`live.RoomStore` boundary and SQLite implementation for room creation,
active snapshots, independent metadata/document revisions, transactional
change append, history loading/compaction, and expired-room cleanup. Presence
and room-session data remain absent from the durable types and schema. Tests
cover reopen persistence, password-hash storage, expiry on every live access
path, slug collisions, atomic batch rollback, independent revision streams,
history compaction, and cascade deletion.

The repository checks for this step pass. No live routes, WebSocket handlers,
password verification, or frontend live UI were added; those remain in later
steps.

### Step 4 — Room domain and in-memory hub

1. Extend the `internal/live` domain package with room/document lifecycle rules
   and in-memory authority state.
2. Create an in-memory room registry keyed by live slug.
3. Load a room snapshot when the first client connects and evict it after the
   final client leaves or expiry is reached.
4. Add per-tab authority state and bounded recent history.
5. Add the participant registry keyed by a stable room/browser identity, with a
   bounded connection map for per-tab joined time, active tab,
   cursor/selection, heartbeat, generation, operation client, and status.
6. Serialize room operations so document changes, tab metadata, and expiry
   transitions cannot race.
7. Add cancellation-aware shutdown that closes connections and flushes dirty
   snapshots.
8. Define structural conflict rules in the authority: server-generated stable
   document IDs; deletion wins over a concurrent rename/language/reorder; the
   last remaining tab cannot be deleted; duplicate creates are idempotent by
   operation ID; and a stale reorder receives the latest metadata state for
   resynchronization instead of partially applying.
9. Enforce creator-editable lock/unlock, stable access categories, the
   10-collaborator/100-viewer/110-participant bounds, and the eight-connection
   participant bound in the authority. Do not add participant removal.
10. Keep generated/renamed nickname uniqueness and active connection/cursor
   presence in process memory. A browser credential reclaims the same
   participant across tabs, reload, reopen, and restart; reconnect grace begins
   only after its final active connection is lost.

**Gate:** Unit and race tests prove deterministic room mutation, durable-before-
acknowledgement ordering, tab conflict rules, presence join/leave behavior,
and clean shutdown.

**Implementation result (2026-08-05):** Added the process-local `live.Hub` and
`RoomSession` domain boundary. Rooms load lazily from SQLite, serialize all
document, metadata, presence, reconnect, expiry, and shutdown operations, and
repair a snapshot from committed history after an interrupted save. Document
edits use the CodeMirror-compatible Go rebase logic with bounded in-memory
history; accepted changes append and commit before snapshot save, compaction,
and acknowledgement. Metadata operations enforce server-generated IDs,
idempotent creates, deletion-wins behavior, final-tab protection, and stale
reorder resynchronization.

Participant names, session generations, reconnect grace, colors, cursor and
selection state, heartbeat status, and joined times remain process memory only.
The hub has no HTTP or WebSocket routes yet. Focused tests cover concurrent
edits, persistence ordering, interrupted-save replay, metadata conflicts,
bounded-history resync, reconnect identity reclamation, stale-connection
rejection, presence updates, room eviction, and race safety.

### Step 5 — HTTP handlers, password gate, and WebSocket transport

1. Add typed live handlers to `internal/httpapi` while keeping handlers thin.
2. Implement create, full HTTP bootstrap, unlock, and WebSocket upgrade routes.
3. Add origin validation, session-cookie checks, read limits, write deadlines,
   pings, pong tracking, close codes, and rate limits.
4. Reject expired, unauthorized, malformed, oversized, and unknown operations
   without exposing room content.
5. Map persistence, room, auth, and limit errors to the stable public error
   envelope.
6. Add reverse-proxy/self-hosting documentation for WebSocket upgrades and
   idle timeouts.

**Gate:** Two real clients can create/join a room, receive the initial snapshot
over HTTP, bridge to WebSocket updates without an edit gap, edit concurrently,
add/rename/reorder/delete tabs, observe presence changes, reconnect, and
receive a generic expired-room response.

**Implementation result (2026-08-05):** Added the live create, bootstrap,
unlock, and WebSocket routes. Protected rooms use bounded Argon2id checks and
short-lived room-scoped HttpOnly/SameSite cookies; live responses are private
and no-indexed. The transport validates the configured origin, preserves
WebSocket hijacking through HTTP middleware, bounds frames and connection
queues, rate-limits room actions, sends heartbeat pings, and bridges document,
metadata, presence, rename, acknowledgement, reconnect, and expiry operations
through the existing `live.Hub`. Focused HTTP and WebSocket integration tests
cover the bootstrap/password boundary and a durable document change bridge.

The existing Step 6 API client is compatible with these responses: protected
bootstrap remains an error-only password gate, successful unlock returns the
full snapshot with `password_required: false`, and document revisions use the
same snake_case wire fields that the frontend normalizes. The full paste
frontend E2E suite also remains green.

### Step 6 — Frontend routing and header entry point

1. Extend `web/src/router.ts` with live-create and live-room routes.
2. Add a right-side header action group containing `LiveBin` followed by
   the theme toggle; preserve the existing logo and hosted menu behavior.
3. Add live API types and request helpers in a separate module rather than
   mixing them into the paste API client.
4. Add live route loading, password-required, service-error, unavailable, and
   expired states.
5. Ensure live URLs and room session cookies never enter local storage or
   analytics.
6. Match the current deployed application's spacing, typography, controls,
   surfaces, and editor-first composition. Add no live-feature hero, promo
   panel, badge, illustration, or introductory feature copy.
7. Reuse or carefully extract the existing toast, persistent-state, inline
   validation, and warning components without changing their current paste
   behavior or appearance. Do not duplicate their CSS for live routes.
8. Add the shared top progress bar and loading coordinator. Use it for current
   static-paste retrieval as well as live bootstrap/connect/reconnect/resync;
   remove the visible centered `Loading paste…` placeholder but preserve an
   accessible hidden loading announcement and all existing failure states.
9. Implement the `LiveBin` handoff in memory: create-editor title maps to
   the first tab name (falling back to `tab1`), language maps to language, and
   content maps exactly to content. Do not carry paste expiry, burn, encryption,
   or other paste-only settings.
10. From every paste-viewer state, open a blank live draft. Add a negative test
    proving plaintext and decrypted encrypted viewer content never enters the
    live handoff, URL, history state, browser storage, or create request.

**Gate:** The existing create, viewer, key-gate, policy, theme, and hosted
navigation tests remain green; keyboard users can reach the new button; static
paste loading uses the minimal shared bar; create drafts transfer locally; and
viewer content never transfers.

### Step 7 — Live creation flow

1. Build `/live` with a borderless, directly editable title-style first-tab name
   whose internal `tab1` default is represented by an `Untitled tab` placeholder,
   the existing right-aligned language selector, an
   icon-only optional-password toggle with an animated placeholder field and
   compact confirmation action plus a show/hide-password control, and a text-only
   `Create` action. Enter in the password field confirms and focuses Create
   without submitting the room.
2. Reuse the existing reviewed language choices and CodeMirror editor setup.
3. Keep the configured fixed room lifetime out of the creation toolbar; the
   public configuration and documentation remain authoritative. Do not add
   `unencrypted`, `plaintext`, `browser-first`, `collaborative`, or similar
   descriptive copy to the page.
4. Give the icon-only optional control the accessible name `Require password`;
   do not surround it with a security explainer or promotional copy.
5. Keep programmatic labels for the borderless title-style tab-name and compact
   password inputs while using `Untitled tab` and `Password` as their visible
   in-field cues. Keep the generated `tab1` name internal until the room opens,
   and keep password text hidden unless the user activates its visibility
   control. Do not populate the editor with fake example content.
6. Validate tab/password/content limits before submitting. Do not add a
   nickname field or nickname gate; identity is generated on room join.
7. On success, copy the room URL and navigate to `/live/{slug}`. Creation must
   carry the creator capability, and a protected room must also carry its new
   password-access session without another prompt.
8. Handle clipboard failure with the existing retryable feedback pattern.
9. Route create, validation, copy, and network outcomes through the shared
   notification system; do not duplicate an error inline. Reset an empty first
   tab name to `tab1`, toast `Tab name cannot be empty`, and stop that submit.

**Gate:** Plain and password-protected rooms create successfully; the password
never appears in the returned URL or response; invalid and oversized requests
are presented clearly.

**Implementation result (2026-08-05):** Added the `/live` first-tab creation
flow with the existing language selector and CodeMirror editor, visible labels,
inline name/password/content validation, the compact 24-hour lifetime, and the
optional `Require password` control. Successful creation copies the room URL,
navigates to the room, and preserves the scoped creator and room-access cookies;
clipboard failure has retryable toast feedback. The live snapshot response now
always includes its zero-valued metadata revision so the frontend and backend
share one bootstrap contract. The password, encryption, and paste-only expiry
settings remain out of the live create request.

**Pre-Step 8 performance cleanup (2026-08-06):** The LiveBin route is now
loaded as a separate browser chunk, so normal paste routes do not download its
route code. Vite now cleans the embedded asset directory on each build and
restores its source marker, preventing old hashed bundles from being embedded
in the Go binary. Live `changes` WebSocket events carry the CodeMirror change
set and revisions only; full documents remain in HTTP snapshots and are used
for initial load or explicit resynchronization.

**Completion update (2026-08-08):** The original collaborative room UI,
one-way watch-only/session-removal controls, reconnect/cursor hardening, API
contract, and browser coverage were implemented. The later approved browser
identity and authority contract supersedes those control/session semantics and
is sequenced in
[`LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md`](LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md).

### Recommended performance sequence

The following order preserves the current editor-first visuals while reducing
startup and live-room work:

1. Finish Step 8 with CodeMirror-owned document state, immediate local echo,
   20–50 ms outbound change coalescing, server-authoritative ordering, and
   bounded reconnect queues.
2. Run an early benchmark checkpoint after Step 8: measure initial load,
   editor readiness, edit-to-render latency with 2 and 10 editors, scaling
   toward 100 viewers, browser memory, server CPU, SQLite write rate, and
   reconnect/resync behaviour.
3. Apply safe route splitting: keep the homepage creation editor immediately
   available, lazy-load the read-only paste viewer route, retain the lazy
   LiveBin route, and retain lazy language modes.
4. Tune runtime costs without reducing functionality: avoid React rerenders on
   each keystroke, throttle cursor/presence traffic, bound tab/history/queue
   state, batch safe snapshot writes, and serve hashed assets with compression
   and caching.
5. Use the formal Step 12 security, accessibility, and performance gate to
   confirm the changes on the supported browser and self-hosted topology.

Code splitting changes when code is downloaded, not the settled appearance or
editor capabilities. Do not remove syntax highlighting, replace CodeMirror,
or introduce P2P merely to improve a bundle-size score. The target is about
8/10 performance while preserving the current visuals; any remaining score
should be decided by measured latency and startup data rather than the raw
bundle warning alone.

### Step 8 — Multi-tab collaborative room UI

1. Build a tab strip with active, add, rename, delete, and reorder interactions.
2. Mount one collaborative CodeMirror state per active document and preserve
   the synchronized state when switching tabs.
3. Do not keep collaborative documents as React-controlled `value` strings;
   let CodeMirror own document state and dispatch remote transactions.
4. Apply local edits immediately for local echo, coalesce outbound changes over
   a short 20–50 ms window, and apply accepted remote change sets as they
   arrive. Do not wait for a full document response after each keystroke.
5. Load language extensions per tab through the existing language loader.
6. Keep active tab, local search state, selection, scroll position, and theme
   local to each browser.
7. Show room URL copy, expiry, participant status, and connection status in the
   room toolbar.
8. Show a creator-only Lock/Unlock control. Keep the creator editable, pause
   collaborators while locked, leave viewers read-only, and do not add
   participant removal or account ownership UI.
9. Add `Save as paste` with `Current tab` and `Every tab` choices. For every
   tab, append clearly separated tab contents into one normal paste and return
   to the standard paste options before upload.
10. While disconnected, keep a bounded queue of local text changes and replay
   them through normal revision/rebase handling after reconnect. When the
   queue limit is reached, make the editor read-only and clearly preserve the
   unsent local text for manual recovery.
11. Disable tab create, rename, language change, delete, and reorder while
   reconnecting or offline. Restore them only after room metadata has
   resynchronized.
12. Apply the settled conflict rules: the room always has at least one tab,
   deletion wins over concurrent metadata edits, and stale reorder reloads the
   latest server order.
13. Keep room chrome limited to working controls and current state. Do not add
    editor welcome text, instructional panels, fake cursors/participants,
    empty metadata rows, or badges describing the collaboration technology.
14. Add remote cursor and selection CodeMirror decorations for participant
    connections in the active tab. Keep colours stable by browser participant
    ID, render a compact name
    label while active, fade the label when idle, and clear decorations on tab
    switch, leave, timeout, and resync.
15. Throttle/coalesce cursor traffic, bound presence payloads, associate every
    cursor with a document revision, map positions through collaborative
    changes, and drop stale/unmappable cursor state rather than moving it to an
    incorrect location.

**Gate:** A user can use every existing language, switch tabs without data
loss, edit a room from two browser contexts while changes converge, recover
queued text after a temporary disconnect, and cannot perform unsafe structural
tab operations before metadata resynchronizes. Creator-editable lock/unlock,
stable access categories, grouped participant capacity, and current/all-tab
paste export work without introducing durable accounts or participant-removal
controls. Remote cursors and selections track the correct connection/text under
concurrent edits without entering durable server state.

### Step 9 — Presence and network-status popover

1. Create a small status indicator with an accessible label and state text.
2. Open the participant popover on hover, click, focus, and Escape dismissal.
3. Show Connected/Reconnecting/Offline for the local user. Show Connected,
   Connection lost, or Offline for remote participants, plus relative joined
   time; do not infer that a remote browser is actively reconnecting.
4. Broadcast connection-specific current-tab/cursor changes as ephemeral
   presence while keeping one roster row per browser participant.
5. Mark a participant stale only after its final connection exceeds heartbeat
   timeout and reconnect grace; announce the change without exposing IP
   addresses or private diagnostics.
6. Add reduced-motion and narrow-screen behavior.
7. Render only real current participants. Do not show sample avatars, empty
   participant slots, or explanatory text when the roster is otherwise
   self-explanatory.
8. Assign a unique adjective+noun name automatically on first join. Add a
   compact rename action to the participant popover, enforce active-room
   uniqueness, and retain the participant's colour when renamed.
9. Keep browser identity stable across tabs, reload, reopen, and restart. Limit
   one participant to eight active connections and expose no nickname field
   before an unprotected room opens.

**Gate:** Presence is correct for join, leave, reconnect, duplicate tabs, and
server restart; it is absent from SQLite and not emitted into logs.

### Step 10 — Password-gated room UX and hardening

1. Focus the password form when required and keep room content hidden until
   unlock succeeds.
2. Keep the gate to a visible `Password` label, field, submit action, and
   contextual error when needed. Do not add encryption/storage explanations
   to the gate.
3. Do not place the password, password-access session, or creator token in the
   URL, script-visible storage, clipboard, error messages, or telemetry. The
   separate non-authorizing browser participant credential may use
   `localStorage` only under its versioned room-scoped key.
4. Provide a retry path for wrong passwords without additional account or
   recovery copy.
5. Test cookie scope, expiry, same-origin WebSocket behavior, invalid Origin,
   brute-force request limits, Argon2id concurrency limits, password-session
   invalidation after server restart, and creator-capability survival across
   restart.
6. Verify successful protected-room creation authenticates the creator and
   production HTTPS uses a `Secure`, `HttpOnly`, `SameSite=Strict` cookie.

**Gate:** Protected rooms cannot be read, joined, or modified through any
unauthenticated HTTP or WebSocket path.

### Step 11 — Lifecycle, cleanup, and shutdown integration

1. Extend the cleanup interface and worker to delete expired live rooms in
   bounded batches.
2. Evict expired active rooms and close their sockets with a normal room-expired
   state.
3. Ensure expired rooms are denied even when cleanup has not run.
4. Flush dirty room snapshots during graceful shutdown within the existing
   shutdown budget.
5. Ensure a process restart does not resurrect expired rooms or stale presence.

**Gate:** Expiry tests pass with cleanup disabled, delayed, and run normally;
shutdown/restart preserves active room documents but not connections.

### Step 12 — Security, abuse, accessibility, and performance hardening

1. Test stored-XSS payloads in every tab and participant-facing label.
2. Test malformed JSON, malformed ChangeSets, duplicate operations, stale
   revisions, oversized frames, tab-limit exhaustion, and message floods.
3. Add connection-per-IP, connection-per-room, unlock, room-create, and
   operation limits.
4. Verify no body, password, session token, or raw change payload reaches
   logs, request IDs, error responses, analytics, or browser storage.
5. Run Go race tests with concurrent room operations and disconnects.
6. Measure memory, SQLite write rate, snapshot size, and browser performance
   with eight tabs and a 1 MiB aggregate room.
7. Verify full keyboard operation, focus restoration, screen-reader status
   announcements, contrast, reduced motion, and mobile popover behavior.
8. Review every visible string and empty state against Section 1.5. Compare
   live pages with the current deployed app at desktop and mobile widths and
   remove non-functional copy, badges, placeholders, and decorative panels.
9. Verify live toasts and warnings match the current placement, colour,
   spacing, motion, timeout, pause, close, stacking, keyboard, reduced-motion,
   and screen-reader behavior. Persistent failures must not disappear as the
   result of a toast timeout, and reconnect events must be deduplicated rather
   than producing notification spam.
10. Test the live-socket client independently with connect timeout, heartbeat
    timeout, exponential backoff/jitter, join rejection, duplicate frames,
    reconnect grace, HTTP resync, queued-change limits, clean unmount, and
    browser online/offline transitions.
11. Verify the top progress bar delays fast loads, remains visible through
    connect/reconnect/resync work, handles overlapping work without hiding
    early, hides on success/error, does not run for typing/cursor traffic, and
    preserves accessible loading announcements and reduced-motion behavior.

**Gate:** Security-negative, race, accessibility, and performance checks pass
on the supported browser and self-hosted single-instance topology.

### Step 13 — Embedded bundle, API documentation, and release readiness

1. Add live HTTP schemas and examples to `docs/openapi.yaml`; document the
   WebSocket message contract beside it because OpenAPI does not describe the
   full WebSocket stream.
2. Build the frontend into `internal/webassets/dist` and verify the embedded
   Go binary serves `/live` and `/live/{slug}` correctly.
3. Add live-room smoke journeys to the existing browser test command.
4. Document WebSocket proxy configuration, room expiry, password semantics,
   plaintext storage, backup implications, and the no-account model.
5. Update privacy/terms/acceptable-use copy to say that live rooms are
   server-readable and that participant presence is ephemeral.
6. Run the repository verification commands:

   ```text
   make format
   make lint
   make test
   make test-race
   make test-e2e
   make build
   ```

**Gate:** A clean self-hosted instance can create, protect, share, edit,
reconnect to, expire, clean up, and restart a multi-tab live room with one
container and one SQLite volume.

## 6. Verification Matrix

### Product journeys

- LiveBin button opens the live creation flow.
- From the create editor, LiveBin carries the unsaved title/language/content
  into the first live draft without uploading it before room creation.
- From every existing paste viewer, LiveBin opens a blank live draft and
  never transfers viewed or decrypted content.
- Live creation and room pages match the current 0xbin interface and contain
  no marketing hero, architecture explanation, maturity badge, fake data,
  decorative placeholder, or redundant feature description.
- Live notifications and warnings are visually and behaviorally identical to
  the current shared system; blocking errors remain persistent and transient
  events do not produce duplicate or repeated toast noise.
- Unprotected room opens directly after URL navigation.
- Protected room shows only the password gate before unlock.
- Correct password opens the room; wrong password never exposes content.
- Two participants see each other's edits in the same tab.
- Participants receive unique adjective+noun names automatically, can rename
  themselves, and retain their session colour after renaming.
- Participants in the same tab see correctly mapped remote cursors and
  selections; switching tabs or leaving removes those decorations.
- Participants can use different tabs and different languages.
- Add, rename, reorder, and delete operations converge for all participants.
- Nickname and connection roster update on join, leave, and reconnect.
- Static paste loads and live connect/reconnect/resync states use the same
  minimal delayed top progress bar without replacing persistent failures.
- Room URL copies successfully or presents a retry action.
- Room expires at its configured lifetime (up to 24 hours) and becomes
  unavailable.
- Existing paste, encrypted paste, burn, theme, policy, and CLI semantics and
  actions are unchanged; only the settled static-loading indicator and new
  LiveBin entry point differ visually.

### Concurrency and recovery

- Concurrent inserts at the same position converge.
- Concurrent insert/delete and overlapping replacements converge.
- Duplicate messages are idempotent or rejected safely.
- Stale revisions rebase or return a recoverable resync response.
- Cursor/selection positions map through concurrent inserts, deletes, rebases,
  reconnects, and resyncs, or are safely dropped when no longer mappable.
- Room metadata and each tab advance revisions independently.
- Late joiners receive the complete current room state.
- The initial room snapshot loads over HTTP; the WebSocket delivers only the
  retained changes after that snapshot and requests an HTTP resync if needed.
- Reconnecting clients do not silently discard acknowledged edits.
- Bounded unacknowledged text edits queue through a temporary disconnect;
  structural tab actions remain disabled until metadata resynchronizes.
- A server acknowledgement is never sent before the corresponding SQLite
  commit succeeds.
- Concurrent tab conflicts follow deterministic rules: deletion wins, the
  final tab cannot be deleted, duplicate creates are idempotent, and stale
  reorder requests resynchronize.
- Server restart restores room snapshots, creator authority, lock state, and
  browser participant identity. Protected-room access still requires unlock
  again when the short-lived in-memory access session was lost.
- Expiry wins over a pending update and closes all active connections.

### Security and privacy

- Password is absent from URL, response, logs, storage, and telemetry.
- Only an adaptive password hash is stored.
- Protected-room creation authenticates the creator without a second password
  prompt, and HTTPS deployments use a Secure room-session cookie.
- Password verification is bounded by both attempt rate and concurrent
  Argon2id work.
- Unauthorized bootstrap, unlock, and WebSocket attempts cannot read content.
- WebSocket `Origin` validation rejects cross-site connection attempts.
- Oversized frames, message floods, and connection floods are bounded.
- Nicknames, tab names, and content render as inert text.
- Raw browser credentials, cursor positions, selections, joined times,
  connection status, and other active presence are not durable server data.
  Stable participant ID/colour derive from the browser credential, and the
  browser may resubmit its last authoritative nickname after restart.
- Decrypted encrypted-paste content is never placed in a live handoff or live
  create request by the viewer's LiveBin control.
- Live plaintext storage is documented honestly; no end-to-end-encryption claim
  is made.
- Privacy/security documentation explains that an unprotected room is editable
  by anyone who obtains or guesses its URL; this is not repeated as permanent
  editor chrome.

## 7. Completion Definition

The extension is complete when:

- Existing paste API, encryption, URL, expiry, burn, rendering, and action
  behavior remains unchanged. The only existing-journey visual change is the
  settled minimal loading bar, alongside the new LiveBin entry point.
- A user can create a plaintext or password-gated live room, with a
  server-configured lifetime up to 24 hours, without an account.
- Multiple CodeMirror language tabs synchronize correctly under concurrency.
- Participants can see temporary nicknames and connection state.
- Participants can rename generated adjective+noun identities and see remote
  cursors/selections in the active tab without durable presence storage.
- One browser profile occupies one participant roster/capacity entry across
  reload, reopen, service restart, and multiple normal tabs; incognito and
  other profiles remain separate.
- Creator authority and lock state survive restart through room expiry. Lock
  and unlock converge for every connection while the creator remains editable,
  collaborators pause/resume, and viewers remain read-only.
- No participant kick or individual role-management path remains.
- Local and remote connection labels only claim states the browser/server can
  actually observe.
- The frontend follows Section 1.5: every visible element is functional or
  immediately useful, and the live pages remain as visually restrained as the
  current deployed application.
- Notifications, warnings, validation, and unavailable states reuse the
  existing components and interaction rules consistently.
- Static and live loading/connection work uses one minimal shared top progress
  bar with accessible status and no keystroke/cursor noise.
- Create-page drafts transfer locally into live creation, while existing paste
  viewers always start a blank live draft.
- Reconnect, restart, expiry, cleanup, and shutdown behavior are tested.
- Password gates protect every room access path but do not claim encryption.
- No room body, password, session token, or change payload is logged or
  persisted outside the intended live-room storage.
- All repository formatting, lint, unit, race, browser, and build checks pass.
