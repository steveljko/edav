# edav

A self-hosted CalDAV, CardDAV and WebDAV server: calendars and contacts you own,
served from a single static binary with SQLite behind it.

It ships with a small htmx admin interface for managing users and collections.
There is nothing to install alongside it — no PHP, no separate database server.

## Status

Early. CardDAV and CalDAV both work: collection creation and deletion, object
PUT/GET/DELETE, multiget, property and time-range queries, principal discovery
and the well-known redirects. Recurring events are expanded with EXDATE,
RDATE and RECURRENCE-ID overrides, honouring embedded VTIMEZONE definitions.

Incremental synchronisation works through `sync-collection`, including
reporting deletions to a client whose token predates them.

There is a web interface at `/admin` for managing users and collections, and a
client setup page listing the exact addresses to paste into each client.

Not yet built: scheduling (RFC 6638 invitations and free/busy), which is out of
scope for v1, and packaging.

## Running

```sh
EDAV_ADMIN_PASSWORD=… make run
curl localhost:8080/healthz
```

## Administration

Open `/admin` and sign in with `EDAV_ADMIN_USERNAME` and `EDAV_ADMIN_PASSWORD`.
From there you can add users, reset passwords, and create or edit collections.
The **Client setup** page shows the addresses to give each client.

The admin session cookie is `Secure` by default, which browsers refuse over
plain HTTP. For local development set `EDAV_SECURE_COOKIES=false`.

## Client setup

Point the client at the server root and let it discover the rest:

```
http://localhost:8080/
```

Sign in with the admin username and password. The URLs behind discovery are:

| Resource | Path |
| --- | --- |
| Well-known | `/.well-known/carddav`, `/.well-known/caldav` (301 to the DAV root) |
| DAV root | `/dav/` |
| Principal | `/dav/principals/{user}/` |
| Address book home | `/dav/addressbooks/{user}/` |
| Address book | `/dav/addressbooks/{user}/{name}/` |
| Calendar home | `/dav/calendars/{user}/` |
| Calendar | `/dav/calendars/{user}/{name}/` |

`/.well-known/carddav` is served without authentication on purpose: iOS and
macOS probe it before they have credentials to send, and answering `401` there
ends discovery without showing the user a useful error.

The principal advertises both home sets, so a client that discovers one
protocol finds the other from the same response.

Contacts and events are stored exactly as the client sends them. Properties this server
does not model, including vendor `X-` extensions, are preserved byte for byte
and the ETag changes only when those bytes do.

## Configuration

All configuration comes from the environment and is validated at startup; the
server refuses to start rather than failing at the first request.

| Variable | Default | Description |
| --- | --- | --- |
| `EDAV_ADDR` | `:8080` | Listen address, `host:port` |
| `EDAV_DB_PATH` | `edav.db` | SQLite database file |
| `EDAV_BASE_URL` | — | Absolute public URL, used for client setup instructions |
| `EDAV_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `EDAV_ADMIN_USERNAME` | `admin` | Admin account username |
| `EDAV_ADMIN_PASSWORD` | — | Required, at least 8 characters |
| `EDAV_CALDAV_ENABLED` | `true` | Serve calendars |
| `EDAV_CARDDAV_ENABLED` | `true` | Serve address books |
| `EDAV_WEBDAV_ENABLED` | `false` | Serve plain WebDAV |
| `EDAV_SECURE_COOKIES` | `true` | Secure attribute on the admin session cookie; set `false` only for local plain-HTTP development |
