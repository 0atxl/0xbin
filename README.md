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

Steps 0–16 are implemented. The production React bundle is embedded in the Go
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

### Pulling a new project release

When a new commit is pushed to the project repository, update a checkout on the
homelab host. Run these commands from the directory containing `compose.yaml`:

```text
git status
git pull --ff-only
docker compose build --pull
docker compose up -d --force-recreate --remove-orphans
docker compose ps
curl --fail http://127.0.0.1:8080/health/ready
```

`docker compose build --pull` refreshes the base image and rebuilds the
application with the newly pulled source. `up` replaces the application
container and keeps the named `0xbin-data` volume mounted. Database migrations
run automatically at startup.

You do not need to remove the old container manually, and you must not run
`docker compose down -v` or delete `0xbin-data`: containers are replaceable,
but the volume contains the SQLite database and all active pastes. If you use a
bind mount instead of the named volume, keep the same host directory in the
Compose file.

For a quick update where the base image does not need refreshing, this shorter
form is sufficient:

```text
git pull --ff-only
docker compose up --build -d --remove-orphans
```

Before a release that changes storage or migrations, stop writes briefly and
take a backup of the mounted `/data` directory or volume. If an update fails,
inspect `docker compose logs --tail=100 0xbin` and check readiness before
attempting a rollback. Roll back the checkout to a known-good commit, rebuild,
and recreate the container only after confirming that the older binary supports
the database schema already present in the volume.

## SQLite

0xbin uses the pure-Go `modernc.org/sqlite` driver, so local and container
builds do not require CGo. The embedded schema uses SQLite `STRICT` tables,
which require SQLite 3.37 or newer. Atomic consume operations use SQLite
`RETURNING`, available since SQLite 3.35. The bundled driver must therefore
provide SQLite 3.37 or newer.

## Licence

0xbin is released under the [MIT License](LICENSE).
