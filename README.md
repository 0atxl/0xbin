# 0xbin

0xbin is an ephemeral paste service with memorable links, automatic expiry,
and optional client-side encryption. It is intended for hosted use and simple
self-hosting from the same codebase.

The product requirements and architecture are defined in [spec.md](spec.md)
and [docs/](docs/).

## Development

Prerequisites:

- Go 1.26 (the current stable Go release when this baseline was created)
- Node.js 24 or newer and npm
- GNU Make

Install frontend dependencies and run the repository checks:

```text
npm --prefix web ci
make format
make lint
make test
make test-race
make test-e2e
make build
```

## Implementation status

Implementation Plan Steps 0–10A are complete. The current binary loads validated
configuration, initializes SQLite migrations, exposes health endpoints, and
serves the rate-limited plaintext create, retrieve, and raw APIs. It also runs
a bounded expiry cleanup worker. The frontend contains the tested browser
AES-256-GCM and URL-fragment key module, but the rendered application remains a
scaffold.

The usable browser interface begins at Step 11, and embedded frontend/container
packaging is Step 16. See [the implementation plan](docs/IMPLEMENTATION_PLAN.md)
for the complete sequence and verification gates.

Docker packaging is a project requirement and is scheduled for Implementation
Step 16; no runtime image is provided yet.

## SQLite

0xbin uses the pure-Go `modernc.org/sqlite` driver, so local and container
builds do not require CGo. The embedded schema uses SQLite `STRICT` tables,
which require SQLite 3.37 or newer. Future atomic consume operations use
`RETURNING`, available since SQLite 3.35. The bundled driver must therefore
provide SQLite 3.37 or newer.

## Licence

0xbin is released under the [MIT License](LICENSE).
