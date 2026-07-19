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

Steps 0–16 are committed. The production React bundle is embedded in the Go
binary, and the repository includes self-hosted container packaging. See the
[implementation plan](docs/IMPLEMENTATION_PLAN.md) for the verification gates.

## Self-hosting

0xbin runs as one container and stores its SQLite database in `/data`. Set the
public URL before starting so copied links use the correct host.

```text
cp .env.example .env
# Edit OXBIN_BASE_URL in .env for your public HTTPS URL.
docker compose up --build -d
```

Open `http://localhost:8080` for a local instance. Confirm service health with:

```text
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
```

The named `0xbin-data` volume persists pastes through container recreation.
For a bind mount instead, replace the Compose volume with a host directory that
is writable by the container's non-root user. Run only one 0xbin container per
SQLite data directory.

### Upgrade and restart

```text
git pull
docker compose up --build -d
```

Database migrations run automatically at startup. Keep the volume mounted;
without it, all pastes disappear when the container is removed.

## SQLite

0xbin uses the pure-Go `modernc.org/sqlite` driver, so local and container
builds do not require CGo. The embedded schema uses SQLite `STRICT` tables,
which require SQLite 3.37 or newer. Future atomic consume operations use
`RETURNING`, available since SQLite 3.35. The bundled driver must therefore
provide SQLite 3.37 or newer.

## Licence

0xbin is released under the [MIT License](LICENSE).
