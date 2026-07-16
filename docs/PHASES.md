# 0xbin Delivery Phases

This document groups the detailed implementation plan into product milestones. A phase is complete only when its exit criteria pass; writing code is not itself completion.

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

Implementation-plan steps: 8–10.

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

- Creation/viewer behaviour and states
- Missing-key dialog and burn confirmation screen
- CodeMirror editor
- Copy/search/raw/wrap behaviours
- Accessibility baseline
- Embedded frontend
- Single container and persistent volume
- Self-host documentation

Implementation-plan steps: 11–12.

Exit criteria:

- Browser journeys pass end-to-end tests.
- One image serves frontend and API and persists SQLite data.
- A new self-hoster can start, restart, back up, and restore the service from documentation.

Visual styling may be refined within this phase after the behaviour is stable, but must not change product/security semantics.

## Phase 4 — Hosted Public Beta

**Objective:** Operate `0xbin.app` safely enough for anonymous early users.

Includes:

- Security headers and XSS hardening
- Operational metrics and alerts
- Abuse contact and protected operator controls
- Privacy and acceptable-use policies
- Backup/restore and rollback
- Persistent hosted deployment

Implementation-plan steps: 13–14.

Exit criteria:

- PRD launch criteria pass.
- Creation can be disabled quickly.
- Reported pastes can be removed.
- Trusted proxy and rate limits work in the real hosted topology.
- Restore and rollback are demonstrated.

## Phase 5 — Post-MVP Improvements

Candidate work, prioritized only from real usage:

- Official CLI using the stable API and local encryption
- 3-day/7-day expiry
- Creator deletion capability
- Improved large-log search and rendering
- Raise size limit toward 5 MiB after benchmarks
- Additional language support/detection
- Packaging improvements and platform guides
- Stronger hosted edge abuse controls

Each candidate requires its own requirements and acceptance criteria before implementation.

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
