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

The current binary loads validated configuration, initializes SQLite
migrations, and exposes liveness and readiness health endpoints. Paste HTTP
operations and the frontend product flows are implemented in later steps of
[the implementation plan](docs/IMPLEMENTATION_PLAN.md). Container packaging is
scheduled for Step 12.

Docker packaging is a project requirement and is scheduled for Implementation
Step 12; no runtime image is provided by this foundation baseline.

## SQLite

0xbin uses the pure-Go `modernc.org/sqlite` driver, so local and container
builds do not require CGo. The embedded schema uses SQLite `STRICT` tables,
which require SQLite 3.37 or newer. Future atomic consume operations use
`RETURNING`, available since SQLite 3.35. The bundled driver must therefore
provide SQLite 3.37 or newer.

## Licence

0xbin is released under the [MIT License](LICENSE).
