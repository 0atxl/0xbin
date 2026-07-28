# 0xbin

0xbin is an ephemeral paste service with memorable links, automatic expiry,
and optional client-side encryption. It is intended for hosted use and simple
self-hosting from the same codebase.

The product requirements and architecture are defined in [spec.md](spec.md)
and [docs/](docs/). Agent-specific guidance and implementation notes are
grouped in [agent_docs/](agent_docs/); the root
[AGENTS.md](AGENTS.md) remains the repository instruction entry point.

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
[implementation plan](agent_docs/IMPLEMENTATION_PLAN.md) for the verification gates.

## Command-line client

The separate Rust [`0xbin-cli`](https://github.com/0atxl/0xbin-cli) project is
a client for this service. It uses `https://0xbin.app` by default and can also
target a self-hosted instance.

Install it with Cargo:

```text
cargo install zeroxbin-cli
```

On Arch Linux, install the AUR package with an AUR helper:

```text
yay -S 0xbin-cli
# or: paru -S 0xbin-cli
```

The package is in the AUR, not an official Arch repository, so
`pacman -S 0xbin-cli` is not available. See the
[CLI release page](https://github.com/0atxl/0xbin-cli/releases) for release
archives and checksums.

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
which require SQLite 3.37 or newer. Atomic consume operations use SQLite
`RETURNING`, available since SQLite 3.35. The bundled driver must therefore
provide SQLite 3.37 or newer.

## Licence

0xbin is released under the [MIT License](LICENSE).
