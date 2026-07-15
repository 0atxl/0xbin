# 0xbin Goals

**Status:** Active  
**Scope:** MVP through public beta  
**Related documents:** `spec.md`, `docs/PRD.md`, `docs/PHASES.md`

This file defines what 0xbin is trying to achieve and how success will be judged. It does not define implementation details or task order.

## Product Goal

Build a small, reliable, ephemeral paste service that is immediately usable at `0xbin.app` and simple enough for anyone to self-host from the same open-source codebase.

0xbin should feel faster and simpler than creating a document or gist while remaining honest about the difference between unlisted plaintext pastes and client-side encrypted pastes.

## Primary Goals

### 1. Make temporary text sharing effortless

- A user can create a paste without registering or signing in.
- The default flow requires only entering content and selecting Generate.
- Every paste receives a clean, memorable URL such as `radiantcolorfulpomeranian`.
- Pastes expire automatically instead of becoming permanent forgotten content.

### 2. Provide meaningful optional encryption

- Encryption is easy to enable but remains off by default.
- Encryption and decryption happen entirely in the browser.
- The server never receives the plaintext or 256-bit encryption key.
- The key is carried after `#` in the shared URL.
- If the fragment is missing, the viewer asks the recipient for the key.
- Product language clearly states that only encrypted paste content is confidential from the server.

### 3. Make self-hosting genuinely simple

- Hosted and self-hosted deployments use the same application and SQLite design.
- A self-hoster can run one container with one persistent volume.
- The application requires no Redis, PostgreSQL, Kubernetes, or external scheduler.
- Installation, configuration, backup, restore, upgrade, and health checks are documented and tested.

### 4. Handle ephemeral content correctly

- Expired pastes are never served, even if physical cleanup is delayed.
- A background worker reclaims expired SQLite rows without requiring cron.
- Burn-after-read pastes are not consumed by link-preview bots.
- Exactly one deliberate concurrent consume request can succeed.

### 5. Be safe enough to operate publicly

- Paste content is always treated as hostile and cannot execute in the viewer.
- Request sizes, creation rates, scanning attempts, and in-memory state are bounded.
- Logs never contain paste bodies, encryption keys, or decrypted metadata.
- The hosted service has basic abuse reporting, administrative deletion, monitoring, backup, and an emergency creation switch before public beta.
- Security and privacy claims remain accurate and modest.

### 6. Work well for developers

- Code, logs, configuration, and plain prose remain readable and copyable.
- The viewer supports search and wrap/no-wrap behaviour.
- The initial 1 MiB paste limit is validated through testing rather than assumed.
- Browser behaviour is accessible by keyboard and usable on desktop and mobile.
- A CLI can be added after the web MVP without redesigning the API.

## Engineering Goals

- Keep the system understandable to one maintainer.
- Prefer standard-library Go and small, justified dependencies.
- Use one Go service with an embedded React frontend.
- Use versioned APIs, database migrations, plaintext payloads, and ciphertext envelopes.
- Make correctness observable through unit, integration, concurrency, browser, security-negative, and end-to-end tests.
- Maintain stable repository commands for formatting, linting, testing, race testing, browser testing, and builds.
- Make decisions based on measured needs rather than hypothetical scale.

## Learning Goals

The project should provide practical experience with:

- Go HTTP service and lifecycle design
- SQLite concurrency, migrations, backup, and cleanup
- Browser Web Crypto and AES-GCM
- Capability-style URL fragments
- Rate limiting and trusted-proxy handling
- Concurrency-safe burn-after-read behaviour
- React and CodeMirror integration
- Accessibility and browser end-to-end testing
- Container packaging and public-service operations
- Security tradeoffs that can be explained honestly in an interview or code review

Learning goals may justify additional investigation, but they must not introduce unnecessary production infrastructure or weaken the user-facing product.

## MVP Success Criteria

The MVP is successful when:

1. A new user can create and open a plaintext paste without instructions.
2. An encrypted paste can be shared and decrypted while its key and plaintext remain absent from server requests and logs.
3. An encrypted URL without its fragment prompts for and accepts the correct key.
4. Expired pastes are inaccessible regardless of cleanup timing.
5. Concurrent burn-after-read requests produce exactly one successful result.
6. Slug collisions are rejected by SQLite and retried without overwriting data.
7. Malicious paste content cannot execute in the application.
8. A clean self-hosted installation works with one container and persistent volume.
9. Backup, restore, migration, and graceful shutdown have been tested.
10. The hosted beta can be monitored, rate-limited, paused, and administered safely.

## Public-Beta Goals

- Deploy a stable single-instance service at `0xbin.app`.
- Publish the source code, container image, setup guide, privacy policy, acceptable-use policy, and security contact.
- Collect aggregate operational data without collecting paste content.
- Observe real usage before raising limits or adding infrastructure.
- Keep a tested rollback and creation-disable procedure ready.

## Non-Goals

The current project is not trying to provide:

- Cryptographic secrecy for unencrypted three-word links
- Accounts, profiles, teams, RBAC, or social features
- Public paste discovery, indexing, or search
- Comments, revisions, forks, or collaboration
- Permanent hosted storage
- File or image hosting
- Custom slugs
- Guaranteed anonymity
- Multi-region or multi-instance scale
- PostgreSQL, Redis, or Kubernetes experience for its own sake
- AI-generated or AI-dependent product features
- Feature parity with Pastebin, GitHub Gist, PrivateBin, or every self-hosted alternative

## Prioritization Rules

When goals compete, use this order:

1. Prevent content exposure, execution, corruption, or incorrect burn/expiry behaviour.
2. Preserve the simple create-and-share experience.
3. Preserve one-service self-hosting and maintainability.
4. Meet accessibility and reliability requirements.
5. Improve performance based on benchmarks.
6. Add convenience and visual polish.
7. Add post-MVP features only after observing real demand.

## Deferred Goals

After the public beta is stable, consider:

- Official CLI with local encryption
- Longer bounded expiry options
- Creator-controlled deletion capability
- Better large-log search and rendering
- Increasing the paste-size limit after benchmarks
- Additional language support
- More deployment guides

Deferred goals must receive explicit requirements and acceptance criteria before implementation. They are not authorization to expand the MVP.

