# Live-Sharing Release Remediation Plan

## Purpose

This plan addresses the unresolved findings from the Phase 7 independent
release audit. The live-sharing branch is not ready to merge until every phase
below passes its gate and the final independent audit reports no unresolved
release blocker.

**Progress:** Phases 0–2 are complete. Phase 3 is next; Phases 3–7, including
the Phase 5A maintainability gate, remain.

This document does not change a settled product decision. `spec.md` remains
authoritative, followed by `AGENTS.md`, the PRD, technical design, frontend
baseline, implementation plan, and delivery phases. Stop and report any
material conflict rather than silently choosing new behavior.

## Execution rules

1. Work only on `feature/live-sharing`; do not push implementation directly to
   `main`.
2. Begin each phase by checking `git status --short` and inspecting overlapping
   changes. Preserve all user-owned worktree changes.
3. Implement one phase at a time. Do not opportunistically begin a later phase.
4. Add regression tests for every corrected failure path before considering a
   phase complete.
5. A browser-state assertion is insufficient when convergence is at issue;
   compare the local editor, a second browser, and the authoritative HTTP
   snapshot.
6. Do not commit, push, open a pull request, or merge unless explicitly asked.
7. Never claim CI verification from a local test or successful push. Confirm
   the pull request's required `verify` check directly.

## Phase 0 — Preserve and review the audit baseline

**Severity:** Release-process gate

**Objective:** Establish a reviewable baseline without losing the existing
uncommitted and untracked remediation work.

### Work

- Review the current tracked, untracked, committed, and remote branch state.
- Separate unrelated work from the live-sharing remediation without deleting or
  overwriting user changes.
- Identify focused commit boundaries for the existing remediation work.
- Record the Phase 7 audit findings as the acceptance baseline for Phases 1–6.
- Keep `main` deployable and make no direct implementation push to it.

### Gate

- The exact audit subject is documented and recoverable.
- No user-owned work is lost.
- Remaining work maps cleanly to the phases below.

## Phase 1 — Make operation retries durable and idempotent

**Severity:** Release blocker

**Objective:** A committed operation whose acknowledgement is lost must never
be applied twice, including after room eviction, process restart, compaction,
or HTTP resynchronization.

### Work

- Persist enough identity with every accepted document and metadata operation
  to recognize a retry after process-local hub state is lost. At minimum,
  preserve the stable operation ID, client ID, operation fingerprint, accepted
  revision, and authoritative result needed for deduplication.
- Commit the operation identity, change, and resulting snapshot atomically.
- Reload a bounded deduplication window with room authority after eviction or
  restart. Keep storage and in-memory retention explicitly bounded.
- Reject reuse of an operation ID with a different fingerprint.
- Ensure an already accepted local update is acknowledged during HTTP
  reconciliation rather than reapplied as a synthetic remote update.
- Preserve durable-before-publish ordering and per-room revision ordering.

### Required tests

- Commit an operation, drop the acknowledgement, discard process-local room
  state, reconnect, and retry with the same operation ID.
- Repeat the sequence across a process restart and after history compaction.
- Prove the retry returns the original accepted result without creating a new
  revision or a second copy of the edit.
- Prove operation-ID reuse with different content is rejected.
- Prove the local editor, a second browser, and the HTTP snapshot contain
  exactly one copy of the edit.

### Gate

- Focused hub, SQLite, HTTP publication, and collaboration tests pass.
- The commit-success/acknowledgement-loss E2E journey passes.
- `make lint`, `make test`, and `make test-race` pass.

## Phase 2 — Reauthenticate protected reconnects before retrying sockets

**Severity:** Release blocker

**Objective:** An expired protected-room access session must lead to the
password gate and renewal flow even though a browser hides the HTTP status of a
pre-upgrade WebSocket rejection.

### Work

- Stop depending on WebSocket close code `1008` to detect every authentication
  failure.
- On an abnormal protected-room reconnect failure, use the authenticated HTTP
  bootstrap/resynchronization boundary to distinguish `password_required` from
  network failure, expiry, removal, overload, and generic unavailability.
- Present the password gate when ordinary access expired, renew the room-access
  cookie after a valid password, and reconnect only after bootstrap succeeds.
- Preserve the separate creator-capability cookie through reauthentication.
- Keep unprotected reconnects free of unnecessary password or account UI.
- Bound authentication probes and reconnect attempts so failures cannot create
  a request loop.

### Required tests

- Advance beyond the 15-minute access-session lifetime, disconnect the socket
  without reloading the page, and reconnect a protected creator.
- Prove the browser shows the password gate, successful unlock restores the
  room, and creator controls remain authorized until room expiry.
- Repeat for a protected ordinary participant and an unprotected creator.
- Prove wrong passwords, expired rooms, removed sessions, HTTP 401 handshake
  rejection, and offline transitions do not loop or expose room content.
- Prove access and creator credentials remain absent from URLs, browser
  storage, logs, and SQLite.

### Gate

- Focused connection-controller, HTTP session, protected-handshake, and hub
  authorization tests pass.
- Multi-browser protected and unprotected post-expiry reconnect E2E passes.
- `make lint`, `make test`, and `make test-race` pass.

## Phase 3 — Make HTTP resynchronization bounded and terminal

**Severity:** Release blocker

**Objective:** Resynchronization must either reach one proven authoritative
state or stop in visible recovery without losing local text, regressing a
revision, or buffering events without bound.

### Work

- Treat a false snapshot-reconciliation result as a failed reconciliation; do
  not mark it complete or resume collaboration.
- Give every snapshot request a generation and cancel or ignore superseded
  requests deterministically.
- Retry transient snapshot failures with bounded exponential backoff and a
  finite attempt/time budget.
- Bound authority events buffered during resynchronization. If the bound is
  reached, request a fresh authoritative snapshot rather than growing memory.
- Define one deterministic rule for WebSocket events received during an HTTP
  request and apply it consistently to document and metadata revisions.
- Keep outbound document and structural operations paused while authority is
  uncertain while retaining local edits for replay or manual recovery.
- Resume only after snapshot state, retained events, and pending local updates
  have converged. On exhaustion, enter an actionable recovery state.

### Required tests

- Delay HTTP snapshot N, apply WebSocket revision N+1, and deliver the older
  response last.
- Return two overlapping snapshots in reverse order.
- Inject a transient HTTP failure followed by recovery and a permanent failure
  that exhausts the retry budget.
- Exercise document deletion and metadata advancement during resync.
- Fill the buffered-event limit and prove memory remains bounded.
- Prove the next local edit uses the authoritative base revision and is
  accepted exactly once.
- Prove local, remote, and HTTP state equality after every successful case and
  recoverable local text after every terminal case.

### Gate

- Focused reconciliation, connection, operation, and wire tests pass
  deterministically.
- The stale-snapshot and failed-resync E2E journeys pass.
- `make lint`, `make test`, and `make test-race` pass.

## Phase 4 — Complete the release-blocker browser regression matrix

**Severity:** Release gate

**Objective:** Exercise the three corrected blockers through real browser,
WebSocket, HTTP, hub, and SQLite boundaries instead of isolated helper tests.

### Work

- Add deterministic test seams for persistence failure, acknowledgement loss,
  delayed/out-of-order snapshots, access-session expiry, and reconnect.
- Inject `service_unavailable`, validation rejection, revision disagreement,
  and connection loss after send but before acknowledgement.
- Cover both protected and unprotected rooms, creators and ordinary
  participants, rapid Unicode edits, offline replay, tab deletion, and metadata
  changes during recovery.
- Assert visible connectivity and recovery states as well as authoritative
  content.
- Keep the journeys deterministic, bounded in runtime, and free of arbitrary
  sleeps where observable conditions can be awaited.

### Gate

- Stable operation retry cannot apply an edit twice.
- Stale or failed snapshots cannot regress or strand authority.
- Protected creator reauthentication works after access-session expiry.
- In every successful recovery, the local editor, second browser, and HTTP
  snapshot are equal.
- In every terminal recovery, local text remains available and the UI does not
  claim `connected`.
- `make test-e2e` passes repeatedly.

## Phase 5 — Reconcile limits and public contracts

**Severity:** Medium release gate

**Objective:** Make operator configuration, runtime enforcement, HTTP schemas,
WebSocket documentation, and release-status documents describe the same
behavior.

### Work

- Decide whether `max_document_bytes` is independently configurable or is
  intentionally identical to the aggregate room limit.
- If independent, add `OXBIN_LIVE_MAX_DOCUMENT_BYTES`, require it to be no
  greater than `OXBIN_LIVE_MAX_BYTES`, expose it through config/bootstrap, and
  enforce it on create, metadata, edit, and snapshot-load paths.
- If intentionally identical, document that constraint explicitly and stop
  presenting the values as independently configurable.
- Test document and aggregate room limits independently below and above the old
  1 MiB default.
- Align OpenAPI required fields with actual bootstrap and unlock responses,
  including optional participant data.
- Correct WebSocket handshake and capacity descriptions to match the actual
  pre- and post-upgrade boundaries.
- Update the live implementation plan, technical design, agent README, and
  `PHASES.md` to reflect the completed remediation state accurately.
- Validate OpenAPI syntax, references, examples, and representative runtime
  responses.

### Gate

- Configuration and server/frontend limit tests pass with distinct aggregate
  and per-document cases when applicable.
- Runtime response fixtures validate against the documented schemas.
- Documentation review finds no contradiction with `spec.md`, the PRD, or the
  WebSocket contract.
- `make lint`, `make test`, and `make build` pass.

## Phase 5A — Remove stale code and reduce maintenance concentration

**Severity:** Maintainability release gate

**Objective:** Leave the completed live-sharing implementation understandable,
reachable, and cohesive without changing settled behavior or performing
line-count-driven refactors.

### Work

- Inventory live-sharing production code, tests, configuration, schemas, and
  documentation for abandoned prototypes, obsolete compatibility paths,
  unreachable branches, unused state, redundant helpers, and duplicated logic.
- Use compiler, lint, test, and targeted reachability evidence before removing
  code. Do not treat code as stale solely because a test does not execute it.
- Review files with concentrated responsibilities, especially the frontend
  workspace, connection/reconciliation controllers, hub and transport, HTTP
  test suites, and browser journey runner.
- Split a large file only when a cohesive responsibility can move behind a
  small, explicit interface. Record why any remaining large file is better kept
  together; file length alone is not a defect.
- Consolidate duplicated validation, error classification, state transitions,
  and test setup where doing so makes the authoritative behavior clearer.
- Preserve ordered migrations, public API and WebSocket contracts, encrypted
  envelopes, URLs, burn semantics, runtime limits, and all release-regression
  coverage.
- Add or adjust focused tests for every behavior-bearing cleanup. Do not add a
  new framework, product feature, or speculative abstraction in this phase.

### Gate

- Every removal and file split has reachability or responsibility evidence in
  the phase report.
- No known dead branch, abandoned prototype, duplicated authority path, or
  misleading compatibility fallback remains in live-sharing scope.
- Large live-sharing files have either been decomposed along tested boundaries
  or explicitly justified as cohesive.
- `make format`, `make lint`, `make test`, `make test-race`, `make test-e2e`,
  `make build`, and `git diff --check` pass.

## Phase 6 — Prepare the complete release candidate

**Severity:** Release-process gate

**Objective:** Produce a clean, reviewable feature branch and obtain the
required repository and pull-request verification without bypassing branch
rules.

### Work

- Review the complete `main...feature/live-sharing` diff and current worktree
  status, including untracked files.
- Run the full repository gate:

  ```text
  make format
  make lint
  make test
  make test-race
  make test-e2e
  make build
  git diff --check
  ```

- Review generated assets and confirm the embedded binary serves `/live`,
  `/live/{slug}`, ordinary paste routes, encrypted fragments, and burn flows.
- Create focused commits only when explicitly requested.
- Push the feature branch and open a pull request only when explicitly
  requested.
- Confirm the pull request's required `verify` check is green. Do not infer CI
  success from a push or local gate.

### Gate

- The local full gate is green with commands and results recorded.
- The branch diff contains no unrelated change or unresolved conflict.
- The pull request exists and its required `verify` check is green.

## Phase 7 — Independent release audit

**Objective:** Independently determine whether the complete live-sharing branch
is safe to merge. This phase is review-only; do not repair findings during the
same audit.

### Audit scope

- Recheck every acceptance criterion in Phases 1–6, including Phase 5A,
  against implementation and tests.
- Reproduce the durable retry, stale/failed snapshot, and protected post-expiry
  reconnect races.
- Review collaboration convergence, durable acknowledgement, idempotency,
  snapshot ordering, creator lifetime, protected access, capacity, presence,
  expiry, disabled mode, cleanup, shutdown, rate limits, hostile input,
  accessibility, responsive behavior, and public documentation.
- Look for regressions in ordinary paste creation/viewing, encrypted fragments,
  burn-after-read, CLI-compatible API behavior, and embedded production assets.
- Review in-memory bounds, goroutine/ticker cancellation, locks, SQLite
  transactions, logs, cookies, URLs, browser storage, and durable data for
  sensitive material.
- Review stale and unreachable code findings, remaining large-file
  justifications, responsibility boundaries, and duplicated authority paths.
- Confirm the pull request's required `verify` check directly.

### Required report

- Findings first, ordered by severity, with exact file and line references.
- Explicit closure status for durable operation retry, revision-safe
  resynchronization, and creator renewal/reconnect.
- Commands actually run and their results.
- Remaining risks and test gaps.
- One verdict: `ready to merge`, `ready after non-code documentation/CI work`,
  or `not ready to merge`.

## Completion criteria

Remediation is complete only when Phases 0–6 pass their gates, Phase 7 reports
no unresolved release blocker, the complete repository gate is green, the
branch diff has been reviewed, and the pull request's required `verify` check
has actually passed.
