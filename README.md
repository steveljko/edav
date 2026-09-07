# edav

A self-hosted CalDAV and CardDAV server: calendars and contacts you own, served
from a single static binary with SQLite behind it.

It ships with a small htmx admin interface for managing users and collections.
There is nothing to install alongside it — no PHP, no separate database server.

## Status

CardDAV and CalDAV both work: collection creation and deletion, object
PUT/GET/DELETE, multiget, property and time-range queries, principal discovery,
the well-known redirects, and incremental synchronisation through
`sync-collection`. Recurring events are expanded with EXDATE, RDATE and
RECURRENCE-ID overrides, honouring embedded VTIMEZONE definitions.

Not built: scheduling (RFC 6638 invitations and free/busy), and plain WebDAV
file storage. Both are out of scope for v1.

Objects are stored exactly as the client sends them. Properties this server does
not model, including vendor `X-` extensions, are preserved byte for byte, and an
ETag changes only when those bytes do.

## Install

### Docker

```sh
docker run -d --name edav \
  -p 8080:8080 \
  -v edav-data:/data \
  -e EDAV_BASE_URL=https://dav.example.com \
  -e EDAV_ADMIN_PASSWORD=… \
  ghcr.io/steveljko/edav:latest
```

`docker-compose.yml` in this repository is a working example. The image is built
from `scratch` and runs as UID 65532; the database lives in the `/data` volume.

### From source

Go 1.26 or newer. No CGO, and no build step for the assets.

```sh
go build -o bin/dav ./cmd/dav
EDAV_ADMIN_PASSWORD=… ./bin/dav
```

Or `make build`, `make test`, `make lint`, `make run`.

## Configure

All configuration comes from the environment and is validated at startup; the
server reports every problem it finds and refuses to start, rather than failing
at the first request.

| Variable | Default | Description |
| --- | --- | --- |
| `EDAV_ADDR` | `:8080` | Listen address, `host:port` |
| `EDAV_DB_PATH` | `edav.db` | SQLite database file |
| `EDAV_BASE_URL` | — | Absolute public URL. Set it behind a proxy |
| `EDAV_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `EDAV_ADMIN_USERNAME` | `admin` | Admin account username |
| `EDAV_ADMIN_PASSWORD` | — | Required, at least 8 characters |
| `EDAV_CALDAV_ENABLED` | `true` | Serve calendars |
| `EDAV_CARDDAV_ENABLED` | `true` | Serve address books |
| `EDAV_SECURE_COOKIES` | `true` | `Secure` on the admin session cookie |

`EDAV_ADMIN_PASSWORD` seeds the admin account on first start. It does not
overwrite the password afterwards, so a password changed in the admin interface
survives a restart.

Set `EDAV_LOG_LEVEL=debug` to log every request with its method, path, status,
size, duration, claimed user and client agent. That is the first thing to reach
for when a client will not connect; at the default level only server errors are
logged, so ordinary polling does not fill a disk.

`GET /healthz` returns 200 once the database is reachable. The binary can probe
it for you with `dav -healthcheck`, which is what the container's `HEALTHCHECK`
runs, since a `scratch` image has no shell.

## Scale

Idle memory is about 40MB and does not grow with the database. A request that
returns a whole collection builds its response in memory first, so peak memory
tracks concurrent requests multiplied by collection size rather than a fixed
figure. A full synchronisation only happens on first setup or after a sync
token is rejected; steady-state syncs return just what changed.

For a household, 128MB is comfortable and 256MB has headroom. Argon2id
verifications are capped at four at once, which bounds what a burst of wrong
credentials can allocate, and verified credentials are cached for five minutes
so a client resending them on every request of a sync does not pay for each one.

## Administration

Open `/admin` and sign in with `EDAV_ADMIN_USERNAME` and `EDAV_ADMIN_PASSWORD`.
From there you can add users, reset passwords, and create or edit collections.
The **Client setup** page shows the exact addresses to give each client.

The session cookie is `Secure` by default, which browsers refuse to send over
plain HTTP. To sign in locally without TLS, set `EDAV_SECURE_COOKIES=false`.

## Client setup

Point the client at the server root and let it discover the rest:

```
https://dav.example.com/
```

Sign in with the account's own username and password. The paths behind
discovery are:

| Resource | Path |
| --- | --- |
| Well-known | `/.well-known/carddav`, `/.well-known/caldav` |
| DAV root | `/dav/` |
| Principal | `/dav/principals/{user}/` |
| Calendar home | `/dav/calendars/{user}/` |
| Calendar | `/dav/calendars/{user}/{name}/` |
| Address book home | `/dav/addressbooks/{user}/` |
| Address book | `/dav/addressbooks/{user}/{name}/` |

The principal advertises both home sets, so a client that discovers one protocol
finds the other from the same response.

- **iOS and macOS** — Settings → Calendar (or Contacts) → Accounts → Add Account
  → Other → Add CalDAV (or CardDAV) Account. Give it the bare host name.
- **DAVx⁵** — Add account → Login with URL and user name, using the server root.
  It finds calendars and address books from one login.
- **Thunderbird** — point CalDAV at the calendar home and CardDAV at the address
  book home; it lists the collections it finds there.

## The well-known redirect requirement

Clients do not ask for `/dav/`. They ask the **root of the domain** for
`/.well-known/caldav` and `/.well-known/carddav` and follow the redirect
(RFC 6764 §6). This server answers both with a 301 to its DAV root.

Two things break this, and both are worth checking first when a client refuses
to connect for no visible reason.

**The well-known paths must be served from the domain root.** If you host edav
under a subpath, a request to `https://example.com/.well-known/caldav` must still
reach it. With nginx:

```nginx
location /.well-known/caldav  { return 301 https://example.com/dav/; }
location /.well-known/carddav { return 301 https://example.com/dav/; }

location /dav/   { proxy_pass http://127.0.0.1:8080; }
location /admin/ { proxy_pass http://127.0.0.1:8080; }
```

**They must not require authentication.** iOS and macOS probe these before they
have credentials to send, and a `401` there ends discovery with no useful error
shown to the user. This server serves both redirects outside its authentication
middleware on purpose; if you put your own auth in front of it, exempt these two
paths.

Set `EDAV_BASE_URL` to the address clients actually reach. Behind a proxy the
request arrives on an internal address, and without this the setup page will
confidently show a URL nothing can connect to.

## Attribution

`internal/dav` contains a fork of
[go-webdav](https://github.com/emersion/go-webdav) by Simon Ser, MIT licensed;
its licence is kept at `internal/dav/LICENSE`, and `internal/dav/README.md`
records what was changed and why.
