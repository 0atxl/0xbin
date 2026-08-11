# 0xbin Delivery Phases

This document groups the detailed implementation plan into product milestones. A phase is complete only when its exit criteria pass; writing code is not itself completion.

**Current status (2026-08-09):** Phases 0–3 are complete. Phase 4 hosted
public-beta work remains open. The separate Phase 5 live-sharing extension,
release hardening, and independent audit are complete. A bounded
browser-identity and creator-authority evolution is planned in
[`LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md`](LIVE_SHARING_IDENTITY_AUTHORITY_PLAN.md)
through the final live-workspace design pass and release audit. Its contract,
durable-storage, additive-identity, grouped-connection, lock-authority, and
kick-removal Phases 0–5 are complete; Phase 6 is next and has not started.

## Phase 0 — Foundation

**Objective:** A reproducible repository and executable service skeleton.

Includes:

- Repository/toolchain scaffolding
- Go server and React build
- Configuration
- CI and stable local commands
- SQLite migrations and health checks

Implementation-plan steps: 0–2.

Exit criteria:

- Clean clone builds and tests through documented commands.
- Server starts, migrates a new database, reports health, and shuts down cleanly.
- Frontend build is reproducible.

Not included: Paste creation, encryption, UI design.

## Phase 1 — Plaintext Vertical Slice

**Objective:** A complete backend/API path for expiring plaintext pastes.

Includes:

- Curated three-word slugs
- Collision-safe create
- Plaintext storage and retrieval
- 1-hour and 1-day expiry
- Raw plaintext response
- Cleanup worker
- Initial request limits

Implementation-plan steps: 3–7.

Exit criteria:

- Plaintext API flow is usable end to end.
- Expiry is enforced in SQL even when cleanup is disabled.
- Collision and trusted-proxy tests pass.
- Slug-space limitations are documented in project documentation; equivalent
  user-facing copy is added with the browser interface in Phase 3.

Not included: Client encryption, burn after read, or a usable browser
interface.

## Phase 2 — Encrypted and Burn Flows

**Objective:** Deliver the security-sensitive differentiators.

Includes:

- Browser AES-256-GCM
- Versioned payload/envelope
- Key in URL fragment
- Missing-key parsing and key-input protocol support
- Server structural validation
- Non-consuming burn confirmation metadata
- Atomic consume

Implementation-plan steps: 8–10A.

Exit criteria:

- Keys/plaintext never reach server traffic or logs.
- Crypto negative and compatibility tests pass.
- Exactly one concurrent consume wins.
- Expired burn pastes cannot be consumed.

Not included: The user-facing missing-key dialog, burn confirmation screen,
complete browser journeys, final visual design, or public launch.

## Phase 3 — Frontend Behaviour and Self-Hosting

**Objective:** Make all MVP behaviour usable and distribute one self-hostable unit.

Includes:

- Frontend design system and light/dark theme baseline
- Creation/viewer behaviour and states, including direct viewer handoff after
  automatic link copy
- Missing-key dialog and burn confirmation screen
- CodeMirror editor
- Copy/search/raw/wrap/new-paste behaviours
- Accessibility, responsive, contrast, and reduced-motion baselines
- Embedded frontend
- Single container and persistent volume
- Self-host documentation

Implementation-plan steps: 11–16.

Exit criteria:

- Browser journeys pass end-to-end tests.
- One image serves frontend and API and persists SQLite data.
- A new self-hoster can start and restart the service from documentation.

The approved frontend design baseline is documented in
[`FRONTEND.md`](FRONTEND.md). Refinement within this phase must
not change product/security semantics.

## Phase 4 — Hosted Public Beta

**Objective:** Operate `0xbin.app` safely enough for anonymous early users.

Includes:

- Security headers and XSS hardening
- Operational metrics and alerts
- Abuse contact and protected operator controls
- Privacy and acceptable-use policies
- Backup/restore and rollback
- Persistent hosted deployment

Implementation-plan steps: 17–18.

Exit criteria:

- PRD launch criteria pass.
- Creation can be disabled quickly.
- Reported pastes can be removed.
- Trusted proxy and rate limits work in the real hosted topology.
- Restore and rollback are demonstrated.

## Phase 5 — Live Sharing Extension

**Objective:** Add a temporary, account-free collaborative editor as a separate
post-MVP room mode while preserving every existing paste guarantee.

Includes:

- Dedicated `/live` and `/live/{slug}` routes and live API namespace
- One 24-hour room with multiple CodeMirror language tabs
- Real-time concurrent editing with visible cursors and selections
- Temporary adjective+noun participant names with rename support
- Session-only presence, joined time, connection state, and participant colour
- Optional shared password gate without end-to-end encryption
- HTTP room bootstrap plus WebSocket updates, reconnect, resynchronization,
  bounded offline text queue, and deterministic tab conflicts
- SQLite room/document snapshots and bounded change history; no durable presence
- Shared top progress bar for static paste loading and live loading/connectivity
- Existing notification, warning, accessibility, reduced-motion, and visual
  baseline reuse with no technical frontend filler copy
- One-process, one-SQLite self-hosted deployment

Implementation plan: [`LIVE_SHARING_IMPLEMENTATION_PLAN.md`](LIVE_SHARING_IMPLEMENTATION_PLAN.md),
Steps 0A–13.

Release hardening and its independent audit are complete. The
single configured live content budget applies to the room aggregate and every
individual document; HTTP exposes `max_document_bytes` only as an equal
semantic alias. The maintainability review and release-candidate gate are also
complete.

Exit criteria:

- The collaboration authority converges under concurrent edits, Unicode,
  reconnect, and cursor/selection mapping tests.
- Password, origin, message-size, connection, expiry, and presence boundaries
  pass negative tests.
- A live room survives restart through durable snapshots, creator authority,
  and lock state. Browser identity reconstructs the same participant when tabs
  reconnect; active connections, cursors, heartbeats, and password-access
  sessions remain process-local.
- The existing paste API, encryption, burn, expiry, rendering, and action
  behavior remain unchanged apart from the approved loading-bar visual update.
- Full repository formatting, lint, unit, race, browser, build, and self-host
  checks pass.

Not included: accounts, media calls, screen sharing, execution, file uploads,
saved live rooms, user-visible history, per-participant permissions,
participant kicking, user-selected view-only roles, or multi-instance
coordination. The creator's reversible room lock is included; it pauses
collaborators while leaving the creator editable and viewers read-only.

## Phase 6 — Post-MVP Improvements

Candidate work, prioritized only from real usage:

- A separate `0xbin-mcp` interface built on the companion CLI library
- 7-day expiry or longer, following an explicit policy review
- Creator deletion capability
- Improved large-log search and rendering
- Raise size limit toward 5 MiB after benchmarks
- Additional language support/detection
- Packaging improvements and platform guides
- Stronger hosted edge abuse controls

Each candidate requires its own requirements and acceptance criteria before implementation.

The companion [`0xbin-cli`](https://github.com/0atxl/0xbin-cli) project already
implements the stable create, retrieve, consume, and local-encryption contract.
It is released independently and does not expand the 0xbin server scope.

## Explicitly Outside the Current Roadmap

- Accounts and team management
- Comments, revisions, forks, or public galleries
- File/image uploads
- Permanent hosted storage
- Redis, PostgreSQL, Kubernetes, multi-region operation
- Claims of anonymity or plaintext confidentiality

## Phase Control

- Do not pull Phase 5 features into MVP because they seem easy.
- Security fixes may cross phase boundaries when necessary.
- Update this document when scope changes; do not silently reinterpret a phase.
- Review costs and abuse signals before increasing hosted limits.
