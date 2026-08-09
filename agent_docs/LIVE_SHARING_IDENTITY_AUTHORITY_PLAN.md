# Live-Sharing Identity and Authority Plan

## Status

**Phase 0 complete (2026-08-09). Phase 1 is next and has not started.**

This plan defines the bounded behavioral foundation that must be completed
before the live-workspace visual pass, final verification, and merge. It does
not reopen the general LiveBin feature scope.

The existing live-sharing implementation and release-hardening work are
complete. Phase 0 reconciled the former process-local creator authority,
one-connection participant model, creator session removal, and one-way
room-wide watch-only behavior across the normative documents. `spec.md`
remains the repository authority.

## 1. Objective

Provide predictable browser identity and creator controls without accounts:

- One browser profile represents one participant in a given live room.
- Normal tabs in that profile share the participant, nickname, colour, access
  class, and creator authority while retaining separate live connections.
- Reloading, closing, and reopening the room in that profile preserves the
  participant identity through room expiry.
- Incognito/private profiles, different browser profiles, different devices,
  cleared site data, and different origins receive different identities.
- Creator authority survives service restart through room expiry without
  becoming an account or transferable ownership record.
- Locking is reversible. The creator remains editable; collaborators are
  temporarily read-only; capacity viewers remain read-only.
- The roster remains one compact list with `creator`, `collaborator`, or
  `viewer` labels. There is no individual role-management interface.
- Active-participant removal is deleted from the product and wire contract.

## 2. Scope Boundary

### Included

- Room-scoped browser resume credentials
- Multiple simultaneous WebSocket connections for one participant
- Stable participant ID, generated nickname, renamed nickname, and colour
- Connection-aware heartbeat, reconnect grace, cursor presence, and cleanup
- Participant capacity counted per browser identity rather than per tab
- Durable hashed creator capability
- Durable room lock state
- Stable access class separated from effective editing ability
- Backward-compatible WebSocket rollout for already-open frontend tabs
- Minimal frontend wiring needed to exercise the new behavior
- Removal of creator kick controls and participant-removal transport handling
- Documentation, negative tests, concurrency tests, and release verification

### Excluded

- Accounts, login, ownership transfer, or creator recovery after site-data loss
- Individual promotion, demotion, banning, or per-user permissions
- A second participant list or separate management dashboard
- Durable cursor, heartbeat, connection, active-tab, or online/offline state
- Cross-device identity synchronization
- Shared active tab or shared scroll position
- Cosmetic workspace restructuring; that begins only after this plan passes its
  behavioral review gate
- Changes to paste routes, encryption, burn-after-read, expiry choices, slugs,
  or the companion CLI's existing paste contract

## 3. Settled Behavioral Contract

### 3.1 Identity

- Identity is scoped to one live-room slug and one browser storage profile.
- The frontend keeps a random 256-bit browser resume credential in
  `localStorage`. It is not creator authority, password authorization, or an
  account credential.
- Concurrent first-time tabs must converge on one stored credential before
  joining. Identity initialization uses a cross-tab critical section when
  available and a storage-event/re-read fallback; it never lets two competing
  initial values remain active for the same browser profile.
- Each mounted room page creates a separate random connection ID. A connection
  ID remains stable across automatic WebSocket reconnects for that mount, but a
  reload may create a new connection ID without creating a new participant.
- The server never broadcasts, logs, or persists the raw resume credential.
  It derives the stable public participant ID from a domain-separated hash of
  the room slug and resume credential.
- The last authoritative nickname is retained in browser storage. A reconnect
  reclaims the active participant. Following a process restart, the browser may
  offer that nickname as a validated preference; the authority resolves any
  active-room collision before broadcasting it.
- Colour is derived from the stable public participant ID, so it remains stable
  without durable presence storage.
- Clearing site data creates a new participant. Copying a room URL does not
  copy identity or creator authority.

### 3.2 Multiple tabs and presence

- A participant owns one or more active connections.
- A participant owns at most eight simultaneous connections. The ninth is
  rejected with a stable, non-destructive connection-limit result; an existing
  connection is never evicted to admit a newer tab.
- Capacity counts the participant once, even when it owns several connections.
  Global WebSocket connection limits still count every socket.
- A participant is connected while at least one connection is healthy.
- Reconnect grace begins only after the final connection is lost. Reopening the
  room with the browser credential during grace reclaims the participant.
- Each connection tracks its own heartbeat, active document, cursor, selection,
  and operation client ID.
- The roster contains one participant row and an authoritative connection
  count. Its displayed current tab is the most recently active connection's
  tab.
- Remote cursor events carry an internal connection ID. Multiple simultaneous
  cursors from one browser may render with the same participant colour and
  nickname without creating extra roster rows.
- A stale connection cannot mark the participant offline, erase a newer
  cursor, release its capacity slot, or invalidate another healthy connection.

### 3.3 Access class and editing ability

Every active participant has one stable access class:

- `creator`: the browser presents the valid room creator capability.
- `collaborator`: the participant owns one of the remaining writer-capacity
  slots.
- `viewer`: the participant uses viewer capacity because collaborator capacity
  is full.

Editing ability is derived separately:

| Access class | Room unlocked | Room locked |
| --- | --- | --- |
| Creator | editable | editable |
| Collaborator | editable | read-only |
| Viewer | read-only | read-only |

- Locking never changes an access class.
- Disconnecting beyond grace releases a collaborator capacity slot, but not the
  browser identity. A later rejoin keeps its identity and nickname while
  receiving the currently available access class.
- The creator does not promote or demote individual participants.

### 3.4 Creator capability and room lock

- Room creation generates a random 256-bit creator token.
- The browser receives the raw token only through the existing room-scoped,
  `HttpOnly`, `SameSite=Strict` cookie, with `Secure` in HTTPS deployments.
- SQLite stores only a domain-separated SHA-256 token hash. Random high-entropy
  capability tokens do not use password hashing; room passwords remain
  Argon2id.
- Creator validation uses constant-time hash comparison and the room expiry.
- The creator token remains valid across service restart until room expiry.
- Losing or clearing the cookie has no recovery path. Knowing the room password
  restores protected-room access, not creator authority.
- Room lock state is stored in SQLite. The authority commits a lock transition
  before broadcasting it.
- Lock and unlock messages are serialized with other room authority changes.
  All connected clients receive the resulting authoritative state; reconnecting
  clients receive it in bootstrap/join state.

### 3.5 Participant removal

- Remove the creator-only participant-removal button.
- Remove `participant_remove` client handling and the corresponding
  `participant_removed` kick semantics.
- During the compatibility window, an old client sending the removed operation
  receives a stable unsupported-operation error; it cannot mutate the roster.
- Normal disconnect, heartbeat timeout, reconnect grace, room expiry, and
  capacity cleanup remain.

## 4. Target Wire Model

The `join` message evolves additively:

```json
{
  "type": "join",
  "session_id": "room-scoped-browser-resume-credential",
  "connection_id": "connection-for-this-mounted-page",
  "client_id": "operation-client-for-this-mounted-page",
  "preferred_name": "Quiet Otter",
  "metadata_revision": 4,
  "document_revisions": [
    { "document_id": "doc-1", "revision": 12 }
  ]
}
```

- Keep `session_id` during the transition; its clarified meaning becomes the
  browser participant credential.
- `connection_id` is optional for old clients and defaults to the session ID,
  preserving their one-connection behavior.
- `preferred_name` is optional and is accepted only through the existing
  nickname validation and uniqueness rules.
- `client_id` remains connection-scoped operation identity and never grants
  participant or creator authority.
- `joined` and roster events add `access_class`, `can_edit`, and
  `connection_count`. Retain the legacy `role` field during the compatibility
  window as a derived alias.
- Connection-specific presence events add `connection_id`; durable document and
  metadata events remain unchanged.
- Replace the one-way UI action with the existing `room_watch_only` message
  carrying either `true` or `false`. The server response remains authoritative.

## 5. Storage Changes

This branch is an unreleased local-development schema. Update the existing
`002_live_rooms.sql` baseline directly; do not add an upgrade migration:

- `live_rooms.creator_token_hash BLOB NULL`, constrained to the selected hash
  length when present.
- `live_rooms.locked INTEGER NOT NULL DEFAULT 0`, constrained to `0` or `1`.

Do not store browser resume credentials, participant IDs, nicknames, colours,
cursors, heartbeats, active tabs, or connection state in SQLite. Stable browser
identity is reconstructed from the high-entropy browser credential; active
presence remains process-local.

All automated verification uses fresh databases created from the revised
baseline. Local preview data created with the old schema is disposable and must
be recreated before running the revised binary. Never delete the user's current
local database without explicit approval; use a fresh temporary data directory
until the user requests a reset.

## 6. Execution Rules

1. Work only on `feature/live-sharing` and inspect the worktree before every
   phase.
2. Phase 0 changes the normative contract before code. Stop on any unresolved
   conflict with `spec.md`.
3. Inspect the companion `0xbin-cli` contract before editing the public HTTP or
   WebSocket surface. Do not introduce CLI or MCP dependencies.
4. Make schema and wire changes additive before changing frontend assumptions.
5. Preserve durable-before-broadcast behavior for lock transitions.
6. Bound credentials, connections per participant, aggregate connections,
   cursor entries, preferred-name input, and reconnect state.
7. Never log or return raw creator tokens, browser resume credentials, access
   cookies, or password material.
8. Add tests with each backend phase and run focused Go and race verification.
9. Add and run focused frontend tests with foundational frontend behavior. Run
   the complete frontend/browser gate in Phase 8. The later cosmetic-work rule
   about user-requested tests does not apply to this behavioral plan.
10. Do not commit, push, merge, or begin cosmetic workspace work unless
    explicitly requested.

## 7. Implementation Phases

## Phase 0 — Reconcile the contract

**Objective:** Make the approved behavior authoritative and remove conflicting
requirements before implementation.

### Work

- Update `spec.md` with browser-profile identity, durable creator capability,
  creator-editable reversible locking, role categories, and kick removal.
- Update `docs/PRD.md` with observable acceptance criteria.
- Update `agent_docs/TECHNICAL_DESIGN.md` with the two-level
  participant/connection model, token boundary, storage fields, and restart
  behavior.
- Update `agent_docs/FRONTEND.md`,
  `agent_docs/LIVE_SHARING_IMPLEMENTATION_PLAN.md`, and
  `agent_docs/PHASES.md` to remove the superseded one-connection, process-local
  creator, one-way lock, and kick behavior.
- Update `docs/live-sharing-websocket.md` with the additive transition contract.
- Inspect `docs/openapi.yaml` and the companion CLI contract; record explicitly
  whether either consumes a live endpoint.

### Gate

- No normative document still requires process-local creator invalidation,
  creator read-only locking, or active participant removal.
- The plan has no unresolved product or security choice.

### Completion record — 2026-08-09

- Reconciled `spec.md`, the PRD, technical and frontend designs, delivery and
  implementation plans, WebSocket contract, and OpenAPI description.
- Confirmed the sibling `0xbin-cli` repository consumes only the paste API and
  encrypted-envelope contracts. It has no LiveBin endpoint or WebSocket
  consumer, so Phase 0 requires no companion change.
- Preserved the existing paste routes, encrypted envelope, URL-fragment key
  handling, expiry, and burn-after-read contracts.
- The Phase 0 gate passes. Phase 1 remains intentionally unstarted.

## Phase 1 — Add durable creator and lock storage

**Objective:** Establish the revised fresh-install persistence baseline without
changing participant behavior yet.

### Work

- Revise and embed `002_live_rooms.sql`; do not add an upgrade migration.
- Extend live room snapshots, insert, load, expiry, and cleanup paths.
- Generate the creator token before room insertion and atomically insert its
  hash with the room.
- Replace the bounded process-local creator registry with database-backed hash
  validation. Preserve generic public errors and cookie attributes.
- Load and persist the room lock flag without exposing the creator hash.
- Use fresh temporary databases for tests and previews until an explicit local
  data reset is requested.

### Required backend verification

- Fresh schema creation, schema constraints, and foreign-key cleanup
- Creator survives close/reopen and a service restart
- Wrong, missing, cross-room, expired, and malformed creator tokens fail
- Raw creator tokens never appear in SQLite, responses, or logs
- Concurrent room creation cannot cross-bind tokens

### Gate

- Storage and HTTP creator tests pass, including race-enabled concurrent cases.
- Paste schema and paste API behavior remain unchanged.

## Phase 2 — Introduce browser and connection identity

**Objective:** Accept the new join model while old clients still connect.

### Work

- Add bounded `connection_id` and optional `preferred_name` decoding.
- Clarify `session_id` as the browser participant credential.
- Derive the stable participant ID through a reviewed, domain-separated hash.
- Keep `client_id` connection-scoped for operation recovery.
- Add `access_class`, `can_edit`, and `connection_count` response fields while
  retaining the derived legacy role.
- Add stable protocol errors for malformed identifiers and unsupported removed
  operations.

### Required backend verification

- Old join messages retain one-connection behavior
- New joins distinguish participant, connection, and operation identities
- Participant IDs are stable per room but different across rooms
- Public participant IDs do not expose the resume credential
- Identifier lengths, encodings, control characters, and empty values fail
  safely

### Gate

- Wire decoder and HTTP/WebSocket compatibility tests pass without changing
  document-edit semantics.

## Phase 3 — Refactor the Hub for multiple connections

**Objective:** Count and display one participant per browser while supporting
several simultaneous tabs.

### Work

- Replace the single connection generation on each participant with a bounded
  connection map.
- Track heartbeat, current tab, cursor, selection, last activity, and generation
  per connection.
- Aggregate participant connected state, connection count, latest active tab,
  nickname, colour, access class, and capacity ownership.
- Start reconnect grace only when the final connection is lost.
- Make stale disconnect/heartbeat/presence events connection-specific.
- Release capacity only after the participant's final connection exceeds grace.
- Preserve operation ordering and replay per `client_id`.
- Keep presence process-local and remove every participant connection cleanly on
  shutdown or room expiry.

### Required backend verification

- Two, three, and maximum bounded connections share one participant row
- Closing one tab leaves the participant connected
- Closing the final tab starts grace; reopening reclaims identity
- Reload overlap cannot create a duplicate participant
- A stale connection cannot erase a current cursor or disconnect the group
- Different browser credentials and incognito-style profiles remain distinct
- Capacity counts participants while the global connection bound counts sockets
- Server restart reconstructs stable ID/colour and accepts the preferred name
- Hub shutdown and expiry terminate every grouped connection without leaks
- Focused multi-connection tests pass repeatedly under the race detector

### Gate

- Hub and transport concurrency matrices pass with no participant, cursor,
  capacity, or goroutine leak.

## Phase 4 — Separate access class and lock state

**Objective:** Make locking reversible without converting collaborators into
viewers or disabling the creator.

### Work

- Allocate creator, collaborator, and viewer classes per active participant.
- Count the creator within the writer-capacity limit.
- Derive `can_edit` from access class and durable room lock.
- Persist lock/unlock before broadcasting `room_mode_changed`.
- Keep the creator editable in both states.
- Restore collaborator editing on unlock without reallocating their class.
- Assign participants joining a locked room to collaborator/viewer capacity
  normally, while applying read-only behavior until unlock.
- Serialize concurrent creator-tab toggles; the last committed transition wins.

### Required backend verification

- The access/editability truth table passes in unlocked and locked rooms
- Every connected tab receives lock/unlock exactly once in authority order
- Offline/reconnecting clients receive current state on join
- Persistence failure produces no broadcast or partial role change
- Restart preserves the lock
- Concurrent creator tabs converge on the last committed state
- Non-creators cannot change the lock

### Gate

- Storage, Hub, HTTP, and WebSocket lock tests pass under race.

## Phase 5 — Remove participant kicking

**Objective:** Remove a weak moderation action without disturbing normal
presence cleanup.

### Work

- Remove the frontend control and creator-only row action.
- Remove participant-removal authority methods and peer termination paths.
- Reject the legacy wire operation without mutating participant state.
- Remove removed-session registries and limits that exist only for kicking.
- Preserve heartbeat timeout, reconnect grace, capacity release, expiry, and
  shutdown cleanup.
- Update tests and documentation so no acceptance gate expects kicking.

### Required backend verification

- A legacy kick message cannot remove or disconnect a participant
- Normal disconnect and stale-participant cleanup still work
- Removing kick state reduces rather than increases retained in-memory state

### Gate

- No reachable participant-removal product or transport path remains.

## Phase 6 — Wire the minimal frontend behavior

**Objective:** Exercise the new foundation without beginning the cosmetic
workspace redesign.

### Work

- Add a versioned, room-scoped browser identity helper using `localStorage`.
- Coordinate concurrent first-use tabs so they settle on one browser credential
  before opening their WebSockets.
- Keep one connection ID and operation client ID per mounted page.
- Save only the stable participant credential and last authoritative nickname;
  never save passwords, access cookies, or creator tokens in script-visible
  storage.
- Handle storage denial/corruption by creating an in-memory identity and showing
  no technical failure copy.
- Render one roster row per participant with concise creator/collaborator/viewer
  labels and connection-aware cursor state.
- Replace the one-way action with Lock/Unlock. Keep the creator editor enabled
  while collaborators become read-only.
- Remove all kick UI.
- Preserve minimal layout and existing accessibility behavior; defer broader
  styling requests.
- Add and run focused frontend unit cases. Add browser cases for the final batch
  gate.

### Gate

- Code review shows no secret in local or session storage.
- Manual inspection confirms no cosmetic scope expansion.
- Focused frontend type, format, unit, and production-build checks pass.

## Phase 7 — Compatibility and documentation closure

**Objective:** Finish the transition without stranding open tabs or stale
documents.

### Work

- Review whether the legacy role alias and missing-connection fallback must ship
  for one release or can be removed before the first public deployment.
- Verify cache headers and hashed assets cannot indefinitely retain an
  incompatible frontend.
- Update self-hosting, WebSocket proxy, privacy, and restart documentation.
- Document that the unreleased baseline requires a fresh local database rather
  than an upgrade path.
- Review the complete diff for accidental paste, encryption, CLI, or MCP scope.

### Gate

- One explicit compatibility policy is documented; no indefinite dual protocol
  remains by accident.
- Normative and operational documentation matches the implementation.

## Phase 8 — Batch verification and behavioral review

**Objective:** Prove the foundation once, then stop before cosmetic work.

### Backend verification

- Formatting and static analysis
- Full Go unit and integration suite
- Full Go race suite
- Repeated multi-connection, final-disconnect, creator-restart, and lock race
  matrices
- Migration tests from a pre-change database and a fresh database

### Frontend/browser verification

- Frontend type, format, and unit checks
- Production frontend and embedded Go build
- Full browser suite
- Same browser with one, two, and several tabs
- Simultaneous first open in two tabs with no existing browser credential
- Reload overlap and close/reopen
- Normal versus incognito browser contexts
- Creator restart recovery
- Lock, unlock, collaborator read-only, creator edit, and late join
- Viewer overflow and capacity release
- Protected-room unlock after service restart
- Stored-XSS payloads in nickname, tab name, and content
- No raw credentials in DOM, URLs, browser storage beyond the scoped participant
  credential, logs, screenshots, or error messages

### Gate

- The authoritative snapshot, every connected editor, roster, roles, and lock
  state converge in every required journey.
- No unresolved release blocker remains.
- Stop and obtain user approval before beginning cosmetic workspace changes.

## 8. Recommended Checkpoint Sequence

Commits are created only when explicitly requested. Recommended boundaries:

1. `docs: define live identity and authority behavior`
2. `feat: persist live creator and lock state`
3. `feat: support browser participants across connections`
4. `fix: make live room locking reversible`
5. `refactor: remove participant removal controls`
6. `feat: restore live browser identity`
7. `test: cover live identity and authority boundaries`

Push only the feature branch. Before merge, require a green pull-request
`verify` check on the final head and review the complete branch diff.

## 9. Principal Risks and Controls

| Risk | Control |
| --- | --- |
| One stale tab disconnects the whole participant | Per-connection generations and final-connection grace |
| Tabs duplicate roster entries | Cross-tab credential initialization and a participant key derived only from that credential |
| Multiple tabs consume writer capacity | Capacity is participant-scoped |
| Creator token leaks | HttpOnly cookie, hash-only SQLite storage, no response/log echo |
| Restart silently unlocks a room | Persist lock before broadcast |
| Lock converts collaborators into viewers | Separate access class from `can_edit` |
| Old open tabs cannot reconnect | Optional additive connection field and legacy fallback |
| Browser storage is unavailable | In-memory identity fallback |
| Unbounded browser identities or connections | Existing room bounds plus a new per-participant connection cap |
| Multiple cursors overwrite each other | Cursor state keyed by participant and connection |
| Identity token is mistaken for room authorization | Keep password and creator checks independent on every protected path |
| Change destabilizes paste service | Keep the baseline schema change limited to live rooms and keep live packages/routes isolated |

## 10. Completion Definition

This plan is complete only when:

- One normal browser profile appears once in a room across reload, reopen, and
  multiple tabs.
- Incognito, another profile, another device, cleared storage, and another
  origin appear as separate participants.
- Nickname and colour remain stable while active and after service restart.
- Multiple tabs maintain independent connection and cursor state without
  duplicating participant capacity.
- Creator authority and room lock survive service restart through room expiry.
- Lock and unlock converge for every client; the creator stays editable,
  collaborators pause/resume, and viewers remain read-only.
- No participant-kick UI or reachable authority path remains.
- Active presence remains process-local and bounded.
- Paste, encryption, burn, slug, expiry, CLI, and one-service deployment
  contracts remain unchanged.
- Backend, frontend, and browser gates pass.
- The user approves the behavioral review and explicitly starts the cosmetic
  live-workspace pass.
