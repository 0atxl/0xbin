# 0xbin Repository Guidance

## Purpose

Build 0xbin as specified in `spec.md`: an ephemeral hosted and self-hosted paste service with clean three-word slugs, optional browser-side AES-GCM encryption, SQLite storage, and one-service deployment.

Read these documents before planning substantial work:

1. `spec.md` — settled product and architecture decisions
2. `docs/PRD.md` — user-facing requirements and acceptance criteria
3. `docs/TECHNICAL_DESIGN.md` — component and data design
4. `docs/IMPLEMENTATION_PLAN.md` — ordered tasks and verification gates
5. `docs/PHASES.md` — scope boundaries and release phases

If documents conflict, `spec.md` wins. Do not silently change a settled decision; identify the conflict and ask before implementing a materially different design.

## Non-Negotiable Decisions

- Paste paths use exactly two adjectives plus one noun, concatenated in lowercase without separators, digits, or suffixes.
- Approximately two million slug combinations are an accepted project constraint.
- Unencrypted pastes are unlisted, not private.
- Encrypted paste keys are random 256-bit AES-GCM keys placed in the URL fragment. The server must never receive them.
- If an encrypted URL lacks a key fragment, the frontend prompts for the key.
- Encryption is optional and off by default.
- SQLite is the only initial database for hosted and self-hosted deployments.
- Expiry is enforced in read/consume queries; the cleanup worker only reclaims storage.
- Burn-after-read requires explicit reveal and atomic consume; a GET never burns content.
- Frontend visual design is not yet settled. Implement specified behaviour without inventing a rigid visual identity.
- Keep the initial deployment to one Go service, one SQLite database, and an embedded frontend.
- Do not introduce Redis, PostgreSQL, Kubernetes, accounts, or file uploads unless the relevant specification changes first.

## Expected Repository Shape

```text
cmd/0xbin/          Go server entry point
internal/           Server packages
web/                React + TypeScript frontend
db/migrations/      Ordered SQLite migrations
wordlists/          Reviewed adjective and noun sources
docs/               Product and engineering documents
tests/              Cross-component fixtures where needed
```

Follow the actual repository if it evolves; update this section when the structure becomes stable.

## Engineering Rules

- Prefer standard-library Go and small, justified dependencies.
- Keep handlers thin; put storage, lifecycle, validation, and rate-limit logic in focused packages.
- Use parameterized SQL and short transactions.
- Use primary-key insertion failure for slug collision detection; never check then insert.
- Represent SQLite timestamps consistently as UTC Unix seconds.
- Treat all paste content as hostile input. Render as text/tokens, never trusted HTML.
- Keep cryptographic formats explicitly versioned and covered by browser/Go-compatible test vectors where applicable.
- Never log paste bodies, decryption keys, deletion secrets, or decrypted metadata.
- Trust proxy headers only from configured proxy addresses.
- Bound request bodies and in-memory maps.
- Use cancellation-aware goroutines. Stop tickers and propagate shutdown contexts.
- Preserve generic public 404 behaviour for missing, expired, deleted, and consumed pastes.
- Prefer accessible semantic HTML and keyboard-complete interactions.

## Change Workflow

For each implementation task:

1. Identify the applicable requirement and phase.
2. Inspect existing code and tests before editing.
3. State assumptions when the documents leave a choice open.
4. Make the smallest coherent change.
5. Add or update tests for changed behaviour.
6. Run relevant formatting, lint, unit, race, integration, and frontend checks.
7. Report what changed, verification performed, and any remaining risk.

Do not implement later phases opportunistically. Record a follow-up instead.

## Verification Expectations

Once scaffolding exists, maintain stable repository-level commands for:

```text
make format
make lint
make test
make test-race
make test-e2e
make build
```

Until those targets exist, use the equivalent native Go and frontend commands and document them in the same change that establishes the toolchain. Never claim verification that was not run.

Security-critical changes require negative tests, including wrong keys, malformed envelopes, expired reads, concurrent burn attempts, oversized bodies, stored-XSS payloads, and spoofed forwarding headers.

## Documentation Maintenance

- Update `spec.md` only when a product or architecture decision changes.
- Update `docs/PRD.md` when observable requirements or acceptance criteria change.
- Update `docs/TECHNICAL_DESIGN.md` when APIs, schema, components, or security boundaries change.
- Update `docs/IMPLEMENTATION_PLAN.md` and `docs/PHASES.md` when sequencing or scope changes.
- Keep this file concise and focused on durable agent behaviour. Do not duplicate the full specification here.

