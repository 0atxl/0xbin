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

The named `0xbin-data` volume persists pastes and durable live-room state
through container recreation. Durable live state includes room documents,
expiry, lock state, and the hash of the creator capability. Active
participants, cursors, reconnect timers, and ordinary password-access sessions
remain process-local and are rebuilt or renewed after restart. For a bind mount
instead, replace the Compose volume with a host directory that is writable by
the container's non-root user. Run only one 0xbin container per SQLite data
directory; live rooms do not support multi-instance coordination.

Set `OXBIN_LIVE_ENABLED=false` before startup when the installation should
serve only ordinary pastes. This omits the live routes, hub, and frontend entry
point rather than running an unused collaboration service.

### Reverse proxying live rooms

The live editor uses `GET /api/v1/live/{slug}/ws`. A reverse proxy must pass
through the WebSocket `Upgrade` and `Connection` headers, preserve the
`Origin` and room-session cookie, avoid response buffering on the WebSocket
route, and allow an idle timeout longer than the configured heartbeat interval
(the default heartbeat is 20 seconds; a 60-second or longer proxy timeout is
recommended). Keep the proxy and application on the same public HTTPS origin
so the `Secure`, `HttpOnly`, `SameSite=Strict` room cookie and origin check
continue to work.

Do not cache `/api/v1/live/*` responses or the HTML application shell, and do
not strip `Set-Cookie` from live create or unlock responses. The application
serves HTML with `Cache-Control: no-store`; each HTML build references
content-hashed JavaScript and CSS assets that may safely use a long immutable
cache lifetime. Deploy the binary and its embedded frontend together so a
reload cannot combine a new shell with an old, incompatible bundle.

### Public deployment: Cloudflare Tunnel and rate limiting

The current public deployment uses one host with this path:

```text
Browser
  -> Cloudflare edge (TLS, WAF/DDoS controls, optional rate limiting)
  -> cloudflared outbound tunnel
  -> Docker host loopback :8080
  -> 0xbin-0xbin-1 (0xbin :8080)
```

The application container is published only on `127.0.0.1:8080`; Tailscale
is used for administration rather than public application ingress. Cloudflare
Tunnel is outbound-only, so the laptop does not need a public inbound port.
The current container uses `OXBIN_BASE_URL=https://0xbin.app` and
`OXBIN_TRUSTED_PROXIES=172.18.0.1/32`, the Docker gateway through which the
host's `cloudflared` process reaches the container. Do not broaden that value
to arbitrary networks. Keep the tunnel token outside the repository.

Cloudflare rate limiting is an edge safety layer; the application's own
bounded rate limits remain enabled because Cloudflare rules do not replace
per-message WebSocket limits and distributed clients can still use multiple
IP addresses. Configure rules in Cloudflare under **Security rules → Create
rule → Rate limiting rules**. Start each rule in **Log** mode, compare it with
normal traffic, then change it to **Block** after false positives are ruled
out. Available matching fields and counting characteristics vary by Cloudflare
plan, so use method matching when available and otherwise scope the path
carefully.

Recommended hosted starting points:

| Traffic | Match | Counter | Starting threshold |
| --- | --- | --- | --- |
| Paste creation | `POST /api/v1/pastes` | IP | 20 requests / 10 minutes |
| Live room creation | `POST /api/v1/live` | IP | 10 requests / hour |
| Live password unlock | `POST /api/v1/live/*/unlock` | IP | 15 requests / 15 minutes |
| Burn consumption | `POST /api/v1/pastes/*/consume` | IP | 30 requests / 10 minutes |

Keep `/health/live`, `/health/ready`, static assets, and ordinary paste reads
out of the initial strict rules. Do not apply a low per-IP rule to the live
WebSocket upgrade path: a dorm or campus NAT can put many legitimate viewers
behind one public IP, and Cloudflare counts the HTTP upgrade rather than the
ongoing WebSocket messages. If connection abuse appears, add a generous,
separately monitored handshake rule and continue relying on the application's
room, connection, and message bounds.

After deployment changes, verify both layers: `https://0xbin.app/health/live`
must remain reachable, application `429` responses must retain `Retry-After`,
and Cloudflare Security Events should show only the intended API rules being
triggered. Cloudflare's [rate limiting rules documentation](https://developers.cloudflare.com/waf/rate-limiting-rules/),
[dashboard guide](https://developers.cloudflare.com/waf/rate-limiting-rules/create-zone-dashboard/),
[Tunnel documentation](https://developers.cloudflare.com/tunnel/), and
[HTTP header guidance](https://developers.cloudflare.com/fundamentals/reference/http-headers/)
are the operational references.

#### Logs and observability

There is no single network log on the laptop. The current sources are:

| Layer | Where to inspect | What it contains |
| --- | --- | --- |
| 0xbin application | `docker logs 0xbin-0xbin-1` | Startup/shutdown errors and expiry-cleanup counts; no per-request access log currently |
| Cloudflare edge | Security/Events and Security Analytics in the Cloudflare dashboard | Requests flagged or acted on by Cloudflare, plus aggregate traffic analysis |
| Detailed Cloudflare requests | Instant Logs or a configured Logpush destination, if available on the plan | Request metadata for correlating edge traffic and origin behaviour |
| Tunnel transport | `sudo journalctl -u cloudflared.service` | Tunnel connection, reconnect, and transport diagnostics; not a normal HTTP access log |
| Tailscale administration | `sudo journalctl -u tailscaled.service` | Tailscale control/data-plane diagnostics, not public 0xbin traffic |

Cloudflare Security Events shows traffic that a security product flagged or
acted on; it is not a complete request log. Use Security Analytics for broader
traffic summaries and Instant Logs/Logpush for detailed request metadata when
the account plan supports them. Do not enable request-body logging for 0xbin:
paste bodies, encryption keys in URL fragments, passwords, room cookies, and
raw live change payloads must stay out of logs. [Cloudflare Logs](https://developers.cloudflare.com/logs/)
and [Security Events](https://developers.cloudflare.com/waf/analytics/security-events/)
describe those distinctions.

For the anonymous default, do not enable persistent request-level application
logging. Keep only aggregate counts, latency buckets, cleanup results, health,
and security/error categories. If request-level visibility is temporarily
needed during an incident, use a short-lived redacted access mode that records
only the route template, method, status, duration, response bytes, client-IP
class, and `X-Request-Id`; never log the full URL, query string, paste body, or
WebSocket frames. Keep the application rate limiter and Cloudflare `CF-Ray`/
request identifiers available for manual correlation, then disable the mode
and remove the temporary logs. The current Docker `json-file` logging driver
has no size-rotation options configured, so configure bounded rotation before
enabling any high-volume access logs.

### Upgrade and restart

```text
git pull
docker compose up --build -d
```

Database migrations run automatically at startup. Keep the volume mounted;
without it, all pastes and durable live-room state disappear when the container
is removed.

The current live identity/authority schema was revised in place before its
first public release. A database created by an earlier development build of
LiveBin is not an upgrade source: stop the service and create a fresh local
database instead. This exception applies only to the unreleased development
baseline; published migrations must never be rewritten.

On an ordinary restart, live documents, expiry, creator authority, and room
lock state survive. Presence and cursors disappear until browsers reconnect.
A normal browser profile reuses its room-scoped participant identity and last
nickname from local storage; a protected room may ask for its shared password
again because the ordinary access session is process-local. The separate
creator-capability cookie remains authoritative through room expiry when it is
still present. Clearing site data loses both participant continuity and creator
authority, and neither has a recovery flow.

## SQLite

0xbin uses the pure-Go `modernc.org/sqlite` driver, so local and container
builds do not require CGo. The embedded schema uses SQLite `STRICT` tables,
which require SQLite 3.37 or newer. Atomic consume operations use SQLite
`RETURNING`, available since SQLite 3.35. The bundled driver must therefore
provide SQLite 3.37 or newer.

## Licence

0xbin is released under the [MIT License](LICENSE).
