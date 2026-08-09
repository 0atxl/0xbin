# 0xbin Product Requirements Document

**Status:** Active product requirements
**Owner:** Project maintainer  
**Related:** `../spec.md`

## 1. Problem

Developers and technical users frequently need to share a block of text, code, logs, or configuration without creating an account or starting a permanent document. Existing services may be unavailable, difficult to self-host, visually cluttered, permanently oriented, or unclear about encryption.

0xbin should make temporary sharing immediate while giving users an optional encrypted path for sensitive content and a simple self-hosted distribution.

## 2. Goals

- Create and share a paste in seconds without an account.
- Produce memorable three-word links.
- Automatically remove pastes after a short lifetime.
- Offer optional browser-side encryption whose key never reaches the server.
- Prevent preview bots from accidentally consuming burn-after-read pastes.
- Provide the same core product as a hosted service and self-hosted application.
- Keep deployment and maintenance understandable for a student maintainer.
- Remain usable for large developer-oriented text up to the validated limit.

The approved post-MVP live-sharing extension adds temporary collaborative rooms
without changing these paste goals or guarantees. It is tracked separately
from the initial paste MVP.

## 3. Non-Goals

- Strong secrecy for unencrypted URLs
- Permanent publishing
- Authentication, profiles, teams, RBAC, comments, or revisions
- Public browsing or search
- Git-backed snippet management
- Arbitrary file or image storage
- Multi-region or multi-instance scale
- Guaranteed anonymity
- Custom URL slugs
- Visual design definition in this document
- Real-time live rooms in the initial paste MVP; live sharing is an approved
  post-MVP extension with its own requirements and exit criteria

## 4. Users and Jobs

### 4.1 Browser user

> When I have text that is awkward to send in chat, I want to paste it and receive a link immediately.

### 4.2 Developer sharing logs or code

> When I share technical content, I want it to remain readable, searchable, copyable, and optionally syntax highlighted.

### 4.3 User sharing sensitive text

> When the content is sensitive, I want to encrypt it locally so the 0xbin server cannot read it.

### 4.4 Self-hoster

> When I want my own paste service, I want to launch one container with persistent storage and configure it without external infrastructure.

### 4.5 Hosted-service operator

> When anonymous users abuse the service, I need limits, visibility, deletion controls, and an emergency creation switch.

### 4.6 Live-room participant

> When I need to work on text or code with someone immediately, I want one
> temporary link where we can edit together without accounts or setup.

## 5. Product Principles

- The default path is fast and uncomplicated.
- Privacy language is exact: plaintext is unlisted; encrypted content is client-side encrypted.
- Security controls should not pretend to provide stronger protection than they do.
- Temporary content is the default and hosted storage is bounded.
- Self-hosting is a supported product surface, not just source availability.
- Behaviour and accessibility precede visual polish.

## 6. Core User Journeys

### 6.1 Create a plaintext paste

1. User opens the creation page.
2. User enters content and may add a title or language.
3. User selects expiry and optional burn-after-read.
4. Encryption remains off.
5. User generates the paste.
6. Service returns a clean three-word URL.
7. Browser copies the canonical sharing URL, navigates directly to the new
   paste viewer, and gives a minimal copy-status confirmation.

### 6.2 Create an encrypted paste

1. User enters content and options.
2. User enables encryption.
3. Browser encrypts title, language, and content locally.
4. Server stores only the ciphertext envelope.
5. Browser appends the locally held key as a fragment to the returned URL.
6. Browser copies the complete encrypted URL, including its locally appended
   key fragment, then navigates directly to the new paste viewer.

### 6.3 Open an encrypted paste

1. Viewer obtains the ciphertext envelope from the server.
2. If a key exists after `#`, the browser imports it and decrypts locally.
3. If the key is missing, the page requests the key in a dialog.
4. Correct keys reveal the paste; wrong or malformed keys show a generic decryption error.

### 6.4 Consume a burn-after-read paste

1. Opening the link shows a confirmation without retrieving the content.
2. User selects **Reveal and destroy**.
3. One atomic consume request returns and deletes the paste.
4. Later requests see the generic not-found state.

### 6.5 Self-host

1. Operator creates a persistent data directory or volume.
2. Operator starts the documented container with a base URL and storage path.
3. Application applies migrations, validates configuration, and starts.
4. Operator can back up, restore, upgrade, and inspect health using documented procedures.

### 6.6 Create and use a live room (post-MVP extension)

1. User selects `LiveBin` from the existing header.
2. From the create editor, the current unsaved title, language, and content are
   carried into a live draft in browser memory. No room is created until the
   user submits the live form.
3. From an existing paste viewer, the action opens a blank live draft and does
   not copy viewed or decrypted content.
4. User creates a room with one initial tab, an optional shared password, and
   the operator-configured lifetime (up to 24 hours).
5. Browser opens the room, receives a temporary adjective+noun participant
   name, and shares the room URL.
6. Participants edit tabs together, see active cursors/selections, observe
   connection state, and can rename their temporary participant name.
7. The creator can switch the room to watch-only mode or remove an active
   collaborator. The room supports up to 10 writers, 100 watch-only viewers,
   and 110 total participants.
8. A participant can save either the current tab or every tab appended into a
   single normal paste, then choose the normal paste's expiry and protection
   options.
9. The room expires, closes active connections, and becomes unavailable at its
   server-configured lifetime (up to 24 hours) even if cleanup has not yet run.

## 7. Functional Requirements

Requirements use `FR-<area>-<number>` identifiers.

### 7.1 Creation

- **FR-CREATE-01:** A user can create a paste without an account.
- **FR-CREATE-02:** Content is required; title and language are optional.
- **FR-CREATE-03:** The hosted service accepts at most 1 MiB of decoded paste content initially.
- **FR-CREATE-04:** Expiry options include View once, 1 hour, 1 day, and 3 days. View once uses burn-after-read and a 3-day unopened-paste safety expiry.
- **FR-CREATE-05:** User may select burn after one deliberate read.
- **FR-CREATE-06:** Encryption is visible near Generate and off by default.
- **FR-CREATE-07:** Server, not client, generates slug and expiry timestamp.
- **FR-CREATE-08:** Creation returns the canonical URL and expiry.
- **FR-CREATE-09:** Creation errors distinguish validation, payload-too-large, rate-limit, and temporary service failure without exposing internals.

### 7.2 Slugs

- **FR-SLUG-01:** Slug contains two adjectives followed by one noun.
- **FR-SLUG-02:** Words are lowercase and concatenated without separators, digits, or suffixes.
- **FR-SLUG-03:** Random selection uses a cryptographically secure source.
- **FR-SLUG-04:** Database uniqueness is authoritative.
- **FR-SLUG-05:** A collision causes bounded regeneration and insertion retry.
- **FR-SLUG-06:** Word lists are reviewed, versioned, and tested for duplicate resulting slugs.

### 7.3 Plaintext viewing

- **FR-VIEW-01:** Active plaintext pastes open directly.
- **FR-VIEW-02:** Viewer supports copy, search, permanent line wrapping, and
  raw/download behaviour. The MVP does not offer horizontal scrolling or a
  wrap toggle.
- **FR-VIEW-03:** Content is rendered as untrusted text and cannot execute.
- **FR-VIEW-04:** Expired, missing, deleted, and consumed pastes share one public not-found state.
- **FR-VIEW-05:** Paste pages are marked against indexing and excluded from sitemaps.

### 7.4 Encryption

- **FR-CRYPT-01:** Browser generates a random 256-bit AES-GCM key and unique 96-bit IV.
- **FR-CRYPT-02:** Title, language, and content are encrypted together.
- **FR-CRYPT-03:** Server receives only a versioned ciphertext envelope.
- **FR-CRYPT-04:** Browser appends the Base64url key to the URL fragment after creation.
- **FR-CRYPT-05:** Server requests and logs never contain the key or plaintext.
- **FR-CRYPT-06:** Viewer uses the fragment key locally when present.
- **FR-CRYPT-07:** Viewer prompts for a key when the fragment is missing.
- **FR-CRYPT-08:** User can paste a raw key or full encrypted URL into the prompt.
- **FR-CRYPT-09:** The client handles wrong keys, malformed envelopes, unsupported versions, and authentication failures safely.

### 7.5 Expiry

- **FR-EXP-01:** Server calculates expiry from allowed duration identifiers.
- **FR-EXP-02:** Every retrieval and consume operation excludes expired rows.
- **FR-EXP-03:** A startup and periodic worker deletes expired rows in bounded batches.
- **FR-EXP-04:** Cleanup failure is logged and measured but does not make expired content readable.

### 7.6 Burn after read

- **FR-BURN-01:** GET never consumes burn-after-read content.
- **FR-BURN-02:** GET returns a reveal confirmation without paste content.
- **FR-BURN-03:** Explicit POST atomically deletes and returns one active paste.
- **FR-BURN-04:** Under concurrency exactly one consume succeeds.
- **FR-BURN-05:** Confirmation warns encrypted users that a wrong key cannot be checked before server-side consumption.

### 7.7 Abuse and operations

- **FR-OPS-01:** Creation, reads, misses, and consume operations have separate configurable rate limits.
- **FR-OPS-02:** Forwarded client IP is trusted only from configured proxies.
- **FR-OPS-03:** Limiter state is bounded and stale entries are removed.
- **FR-OPS-04:** Operator can disable new creation without disabling reads.
- **FR-OPS-05:** Operator can delete a reported paste by slug through a protected administrative mechanism.
- **FR-OPS-06:** Application exposes liveness and readiness endpoints.
- **FR-OPS-07:** Hosted service publishes privacy, acceptable-use, and abuse-contact information before public beta.

### 7.8 Self-hosting

- **FR-HOST-01:** Official image runs as one application container.
- **FR-HOST-02:** SQLite data persists through one mounted directory/volume.
- **FR-HOST-03:** Configuration is validated at startup.
- **FR-HOST-04:** Migrations are ordered and repeatable.
- **FR-HOST-05:** Backup, restore, and upgrade procedures are documented and tested.

### 7.9 Live sharing extension

These requirements apply to the approved post-MVP live mode and do not modify
the paste requirements above.

- **FR-LIVE-01:** A user can create and join a live room without an account.
- **FR-LIVE-02:** Live rooms use `/live/{slug}` and a separate live API/SQLite
  namespace; existing paste routes and payloads remain unchanged.
- **FR-LIVE-03:** A room starts with one tab, supports configured tab limits,
  and supports every existing CodeMirror language mode.
- **FR-LIVE-04:** Participants can edit the same tab concurrently and all
  connected clients converge without last-write-wins data loss.
- **FR-LIVE-05:** Participants can add, rename, delete, and reorder tabs using
  deterministic conflict rules; the last tab cannot be deleted.
- **FR-LIVE-06:** The browser displays mapped temporary participant cursors and
  selections in the active tab.
- **FR-LIVE-07:** Each participant receives a unique adjective+noun temporary
  name, can rename it during the session, and sees it in the participant
  popover with joined time and connection state.
- **FR-LIVE-08:** Presence, participant colours, cursors, selections, and
  joined time are session-only and are not stored in SQLite.
- **FR-LIVE-09:** Live room content is server-readable plaintext and does not
  use the paste AES-GCM envelope.
- **FR-LIVE-10:** A room may require one shared password; protected bootstrap,
  unlock, and WebSocket access paths all require the room session.
- **FR-LIVE-11:** Live rooms expire at their server-configured lifetime (no
  longer than 24 hours); read, unlock, mutation, WebSocket, and cleanup paths
  enforce expiry.
- **FR-LIVE-12:** A temporary disconnect queues bounded text edits, restores
  them after reconnect, and disables structural tab actions until metadata is
  synchronized.
- **FR-LIVE-13:** The live frontend uses the existing visual, notification,
  warning, accessibility, and reduced-motion treatment without technical
  marketing copy. The creation screen mirrors the paste creator's compact
  editor-first layout: its first background tab name defaults internally to
  `tab1`, while the borderless title treatment shows an `Untitled tab`
  placeholder during creation; language stays right-aligned, and the byte count
  uses the paste creator's MiB presentation. Optional
  password entry is disclosed by an icon-only control, as is paste encryption;
  both creation actions are text-only. Password entry has a separate compact
  confirmation action; Enter confirms it and focuses Create without submitting
  the room. A separate accessible toggle reveals or hides password text, which
  is hidden by default.
  Creation errors use the shared toast system; an empty initial tab name is
  restored to the internal `tab1` default and `Untitled tab` placeholder, and
  reported as `Tab name cannot be empty`.
- **FR-LIVE-14:** The shared top progress bar covers static paste loading and
  live bootstrap/connect/reconnect/resync work without becoming keystroke or
  cursor feedback.
- **FR-LIVE-15:** LiveBin is optional in the same one-service deployment and
  can be disabled for self-hosted bare-paste installations.
- **FR-LIVE-16:** The creator's room-scoped temporary session can switch the
  room to watch-only mode and remove active collaborator sessions without
  introducing accounts or durable ownership.
- **FR-LIVE-17:** A room enforces a maximum of 10 writing participants, 100
  watch-only viewers, and 110 total participants.
- **FR-LIVE-18:** A participant can export the current tab or all tabs as one
  normal paste, with the normal paste flow choosing expiry, encryption, and
  burn-after-read settings.
- **FR-LIVE-19:** Live collaboration uses a server-authoritative WebSocket
  transport; P2P/WebRTC is not required.
- **FR-LIVE-20:** Live application limits remain configurable for operators;
  hosted deployments may add an edge rate limiter, while self-hosted
  installations are not forced to use hosted-service rate-limit values.

## 8. Frontend Behaviour Requirements

This section defines behaviour, not visual design. The visual and interaction
baseline is documented in [`FRONTEND.md`](../agent_docs/FRONTEND.md).

### 8.1 Creation state

- Empty, editing, validating, encrypting, submitting, success, and failure states
- Size counter and clear limit feedback
- Keyboard-accessible controls and logical focus order
- Encryption explanation that says the key is part of the copied fragment URL and never sent to the server
- Disable duplicate submission while a request is active
- On success, copy the complete sharing URL when possible and navigate directly
  to the new paste viewer; surface a minimal retryable notice if clipboard
  access fails

### 8.2 Viewer state

- Loading, plaintext-ready, encrypted-key-required, decrypting, decrypted-ready, burn-confirmation, not-found, and service-error states
- Copy success announced without relying only on color or animation
- Key prompt traps focus correctly, closes safely, and never persists the key
- Raw/download unavailable or clearly ciphertext-only for encrypted content
- Generic unavailable state remains at the requested paste URL and does not
  expose whether it was missing, expired, deleted, or consumed

### 8.3 Live-room state

- `/live` creation, room bootstrap, password gate, connected, reconnecting,
  offline, expired, unavailable, and service-error states
- The existing top progress bar is used for actual live loading/connectivity
  work; persistent failures do not disappear when it hides
- The participant popover shows only real participants, their temporary names,
  active state, joined time, and current tab
- Remote cursors and selections remain subtle and do not cover editor content
- Live screens contain functional labels and concise state copy only; no
  architecture, maturity, encryption, or collaboration slogans are added

### 8.4 Accessibility

- Semantic labels and headings
- Full keyboard use
- Visible focus indicators
- Status announcements for asynchronous actions
- Sufficient contrast in the eventual design
- Reduced-motion support
- Mobile-compatible content viewing and horizontal scrolling
- Live participant popovers, tab operations, cursor/selection state, and
  reconnect status are keyboard and screen-reader usable

## 9. Non-Functional Requirements

- **NFR-SEC-01:** Paste content cannot cause stored XSS.
- **NFR-SEC-02:** Hosted frontend loads no third-party executable script.
- **NFR-SEC-03:** Sensitive values are excluded from logs and telemetry.
- **NFR-PERF-01:** Creation/retrieval supports 1 MiB within documented timeouts.
- **NFR-PERF-02:** Viewer remains usable with at least 10,000 lines on supported desktop browsers.
- **NFR-REL-01:** Process shuts down gracefully without corrupting SQLite.
- **NFR-REL-02:** Expiry remains correct when the cleanup worker is delayed or fails.
- **NFR-PORT-01:** Official container runs on common amd64 and arm64 Linux hosts where supported by CI.
- **NFR-MAINT-01:** Core behaviour has unit, integration, concurrency, and browser tests.
- **NFR-LIVE-01:** The live extension remains supported as one Go process, one
  SQLite database, and one embedded frontend instance.
- **NFR-LIVE-02:** Live collaboration, presence, reconnect, cursor mapping,
  expiry, password, accessibility, and abuse limits have negative tests.

## 10. Product Metrics

Use aggregate, privacy-conscious operational metrics:

- Creation success/error/rate-limit counts
- Encrypted versus plaintext creation counts
- Retrieval success and generic miss counts
- Payload-size distribution
- Request latency and errors
- Active database size and expired rows removed
- Cleanup failures
- Burn consume wins/misses

Do not record paste bodies, titles, encryption keys, or user-level behavioural profiles.

## 11. Launch Criteria

- All MVP functional requirements pass automated or documented verification.
- Encryption traffic inspection confirms keys and plaintext do not reach the server.
- Concurrent consume test produces exactly one winner.
- Stored-XSS test corpus does not execute.
- SQLite backup and restore are demonstrated.
- Rate limits work behind the chosen trusted proxy.
- Abuse deletion and creation shutdown are operational.
- Policies and security contact are published.
- One-command self-host instructions work on a clean environment.

The live-sharing extension has a separate release gate: its dedicated
implementation plan and browser journeys pass without changing the existing
paste API, encryption, burn, expiry, or rendering semantics.

## 12. Open Product Questions

- Add a bounded 7-day expiry after MVP testing?
- Offer a one-time deletion capability to paste creators?
- Should raw plaintext responses be enabled for burn-after-read pastes? Default answer: no.
- Which browsers and mobile versions receive official support?
