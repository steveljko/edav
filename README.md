# edav

A self-hosted CalDAV, CardDAV and WebDAV server: calendars and contacts you own,
served from a single static binary with SQLite behind it.

It ships with a small htmx admin interface for managing users and collections.
There is nothing to install alongside it — no PHP, no separate database server.

## Status

Early. CardDAV works: address book creation and deletion, contact PUT/GET/DELETE,
multiget and property queries, principal discovery and the well-known redirect.
CalDAV and the admin UI are not built yet, so address books are created by a
client rather than through a UI.

## Running

```sh
EDAV_ADMIN_PASSWORD=… make run
curl localhost:8080/healthz
```

## Client setup

Point the client at the server root and let it discover the rest:

```
http://localhost:8080/
```

Sign in with the admin username and password. The URLs behind discovery are:

| Resource | Path |
| --- | --- |
| Well-known | `/.well-known/carddav` (301 to the DAV root) |
| DAV root | `/dav/` |
| Principal | `/dav/principals/{user}/` |
| Address book home | `/dav/addressbooks/{user}/` |
| Address book | `/dav/addressbooks/{user}/{name}/` |

`/.well-known/carddav` is served without authentication on purpose: iOS and
macOS probe it before they have credentials to send, and answering `401` there
ends discovery without showing the user a useful error.

Contacts are stored exactly as the client sends them. Properties this server
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
