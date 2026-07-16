# 0xbin Step-by-Step Implementation Plan

**Purpose:** Ordered execution plan for building the MVP without pulling later-phase work forward.  
**Inputs:** `../spec.md`, `PRD.md`, `TECHNICAL_DESIGN.md`, `../AGENTS.md`

Each step ends with a verification gate. Do not begin a dependent step until its gate passes. Commit boundaries may follow steps or coherent substeps; avoid one enormous final commit.

## 0. Repository Baseline

### Tasks

1. Initialize Git repository metadata if not already present.
2. Add Go module and supported Go version.
3. Scaffold `web/` with React, TypeScript, and Vite.
4. Add `.editorconfig`, `.gitignore`, license decision placeholder, and contribution/security-policy skeletons.
5. Establish stable commands through a Makefile or equivalent:
   - `format`
   - `lint`
   - `test`
   - `test-race`
   - `test-e2e`
   - `build`
6. Add CI for Go and frontend checks on supported Linux architecture.
7. Copy/retain the product documents in their specified locations.

### Verification gate

- Empty Go server compiles.
- Frontend production build succeeds.
- CI runs the same documented local commands.
- `AGENTS.md` commands match reality.

## 1. Configuration and Server Skeleton

### Tasks

1. Create typed configuration loading and validation.
2. Add HTTP server with explicit timeouts and graceful shutdown.
3. Add request IDs and panic recovery.
4. Implement `/health/live`.
5. Reserve `/health/ready` until database initialization exists.
6. Define stable API error response type.
7. Add tests for invalid configuration and graceful cancellation.

### Verification gate

- Server starts with minimal safe configuration.
- Invalid base URL, limits, durations, and data path fail clearly.
- Shutdown completes within configured grace period.
- API unknown routes return JSON; frontend routes do not swallow API errors.

## 2. SQLite Foundation

### Tasks

1. Select a maintained SQLite Go driver based on deployment/CGo requirements; record the decision.
2. Implement database open, busy timeout, foreign-key enablement, and WAL verification.
3. Create embedded numbered migration runner.
4. Add the initial `pastes` migration and expiry index.
5. Implement readiness check.
6. Create temporary-database integration-test helper.
7. Document the SQLite runtime/version requirement for `STRICT` tables and `RETURNING`.

### Verification gate

- Fresh database migrates from zero.
- Re-running migration is safe.
- Binary rejects an unsupported/newer schema.
- Readiness reflects database accessibility.
- Integration tests run against a real SQLite database.

## 3. Word Lists and Slug Generation

### Tasks

1. Source and license adjective/noun lists.
2. Curate them for lowercase ASCII, safe content, and intended vocabulary.
3. Decide exact list sizes producing approximately two million combinations.
4. Add validation tool/test for duplicates, invalid characters, empty entries, and resulting duplicate slugs.
5. Implement unbiased `crypto/rand` selection.
6. Add deterministic random-source injection for tests.
7. Implement bounded insert-and-retry orchestration.

### Verification gate

- Generated slugs always match `^[a-z]+$` and the two-adjective/one-noun construction.
- Combination-count test reports the intended size.
- Forced collision test proves insertion retries.
- Non-unique storage errors are not mistaken for collisions.

## 4. Plaintext Paste Domain and Storage

### Tasks

1. Define versioned plaintext payload types.
2. Validate content required, UTF-8 policy, title/language limits, and decoded 1 MiB maximum.
3. Implement allowed expiry identifiers and server-side calculation.
4. Implement `Create` and `GetActive` storage methods.
5. Ensure active retrieval filters expiry in SQL.
6. Map missing and expired records to the same domain error.
7. Add create/retrieve unit and integration tests.

### Verification gate

- Valid paste round-trips exactly.
- Client cannot set arbitrary timestamps.
- Expired rows are never returned even without cleanup.
- Oversized and malformed input fails before excessive allocation.

## 5. Plaintext HTTP API

### Tasks

1. Implement `POST /api/v1/pastes` for plaintext.
2. Implement `GET /api/v1/pastes/{slug}`.
3. Implement safe raw endpoint for normal plaintext pastes.
4. Add `Cache-Control: no-store`, robots, content-type, and nosniff headers.
5. Validate slug syntax before database access while preserving generic public not found.
6. Add request-body limit and stable errors.
7. Draft OpenAPI schemas matching actual handlers.

### Verification gate

- API contract tests cover success and every public error.
- Missing and expired responses are equivalent in status/body.
- Raw endpoint cannot be content-sniffed as executable HTML.
- Request ID is present in errors without sensitive data.

## 6. Expiry Worker

### Tasks

1. Implement bounded `DeleteExpiredBatch`.
2. Run cleanup once at startup after migration.
3. Run periodic cancellation-aware cleanup with ticker shutdown.
4. Add per-run timeout and safety cap.
5. Add structured count/duration/error telemetry.
6. Test cancellation and simulated database failures.

### Verification gate

- Worker physically removes expired rows.
- Active rows remain.
- Failed cleanup does not affect read-time expiry enforcement.
- No goroutine/ticker leak is detected in tests.

## 7. Rate Limiting and Client IP

### Tasks

1. Implement bounded in-memory limiter registry with stale eviction.
2. Add categories for creation, successful reads, misses, and consume.
3. Implement trusted-proxy configuration and client-IP extraction.
4. Ignore spoofed forwarding headers from untrusted connections.
5. Add missing-slug consecutive-failure escalation.
6. Add `Retry-After` and stable rate-limit response.
7. Exempt health checks.

### Verification gate

- Limits act independently per category.
- Shared successful reads do not consume the miss budget.
- Spoofed `X-Forwarded-For` cannot rotate identity from an untrusted client.
- Registry size falls after stale-entry cleanup.

## 8. Browser Cryptography Module

### Tasks

1. Implement tested Base64url encode/decode helpers.
2. Define versioned plaintext and ciphertext-envelope TypeScript schemas.
3. Implement 256-bit AES-GCM key generation/export.
4. Implement 96-bit IV generation.
5. Implement encrypt and decrypt functions using Web Crypto.
6. Implement fragment serialization/parsing without transmitting the key.
7. Add fixed and round-trip test vectors, including Unicode and negative cases.

### Verification gate

- Correct key decrypts exact content.
- Wrong key and modified ciphertext fail authentication.
- Key decoder requires exactly 32 bytes.
- Network test confirms fragment/key never appears in request target/body/headers.

## 9. Encrypted API Mode

### Tasks

1. Extend create request to accept encrypted envelope.
2. Validate envelope version, algorithm identifier, Base64url fields, IV length, and size.
3. Store envelope without inspecting ciphertext content.
4. Return envelope and encryption metadata on retrieval.
5. Ensure logs and errors cannot include envelope content unnecessarily.

### Verification gate

- Encrypted envelope round-trips byte-for-byte.
- Server rejects structurally invalid/unsupported envelopes.
- Server never requires or accepts an encryption key.
- Plaintext and encrypted modes cannot be confused through malformed flags.

## 10. Burn-After-Read

### Tasks

1. Return confirmation metadata rather than content from GET for burn pastes.
2. Implement atomic conditional delete-and-return in SQLite.
3. Implement `POST /api/v1/pastes/{slug}/consume`.
4. Apply active expiry condition and consume-specific limits.
5. Disable raw endpoint for burn pastes.
6. Add concurrent consume integration test with many contenders.
7. Document encrypted wrong-key-after-consume behaviour.

### Verification gate

- GET does not consume or expose content.
- Exactly one concurrent consume returns content.
- Expired burn paste cannot be consumed.
- Subsequent attempts share generic not-found behaviour.

## 11. Frontend Behavioural MVP

Visual design is intentionally open.

### Tasks

1. Build typed API client and route shell.
2. Implement creation form states and validation.
3. Integrate CodeMirror 6 editor with plaintext fallback and size counter.
4. Implement plaintext result and viewer behaviours.
5. Implement encryption toggle and client-side encrypt-before-submit.
6. Construct copied encrypted URL with fragment locally.
7. Implement fragment parsing and missing-key dialog.
8. Implement burn confirmation and deliberate consume.
9. Implement copy, raw/download, search, wrap/no-wrap behaviour.
10. Implement loading/error/not-found/decryption states.
11. Add semantic accessibility and keyboard behaviour.

### Verification gate

- Complete plaintext, encrypted, missing-key, wrong-key, expiry, and burn journeys pass browser tests.
- Fragment key is absent from server/network logs.
- Malicious paste corpus cannot execute.
- Keyboard-only and automated accessibility checks pass agreed baseline.

## 12. Embedded Frontend and Container

### Tasks

1. Embed production frontend assets into the Go binary.
2. Serve client routes without interfering with `/api` and `/health`.
3. Create multi-stage container build.
4. Run as non-root with persistent `/data`.
5. Add container health check.
6. Provide Docker/Podman and Compose examples.
7. Test amd64/arm64 build targets where CI supports them.

### Verification gate

- One image serves UI and API.
- Restart preserves pastes.
- Missing volume/config produces clear safe behaviour.
- Container stops gracefully and passes health checks.

## 13. Security and Operational Hardening

### Tasks

1. Implement strict CSP compatible with production bundle.
2. Add HSTS deployment rule, referrer, permissions, frame, and MIME protections.
3. Audit logs for payload/key leakage.
4. Add fuzzing for parsers and forwarded IP handling.
5. Add Go race, static analysis, dependency audit, frontend audit, and container scan to CI as appropriate.
6. Implement protected operator delete and creation-disable control.
7. Add aggregate metrics and alerts.
8. Create backup/restore script or documented command and test it.
9. Create privacy, acceptable-use, security, and abuse-contact pages.

### Verification gate

- Security-header test passes.
- Stored-XSS corpus remains inert.
- Restore produces a working instance with expected active/expired behaviour.
- Operator can stop creation and delete a reported slug without database shell access.
- No critical known dependency issue remains unexplained.

## 14. Public Beta

### Tasks

1. Deploy one hosted instance with persistent disk and TLS.
2. Configure trusted proxy list and verify real client-IP behaviour.
3. Run smoke, encrypted traffic, expiry, burn concurrency, and restore tests against staging/production-like environment.
4. Set conservative resource, rate, and storage alerts.
5. Publish self-host guide and versioned image.
6. Open beta with a rollback/creation-disable procedure ready.

### Verification gate

- All PRD launch criteria are evidenced.
- Monitoring and abuse contact are live.
- A tested rollback exists.
- Documentation matches the released image.

## Working Rule for Codex

Ask Codex to implement one numbered step or tightly related substep at a time. Example:

```text
Read AGENTS.md, spec.md, and Step 3 of docs/IMPLEMENTATION_PLAN.md.
Implement only Step 3. Explain assumptions before editing, run its verification
gate, and do not begin Step 4.
```

At the end of each step, review the diff and test evidence before proceeding.
