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

The initial binary is only a compile-time service placeholder. Configuration,
HTTP serving, SQLite, and container packaging are intentionally implemented in
later steps of [the implementation plan](docs/IMPLEMENTATION_PLAN.md).

Docker packaging is a project requirement and is scheduled for Implementation
Step 12; no runtime image is provided by this foundation baseline.

## Licence

0xbin is released under the [MIT License](LICENSE).
