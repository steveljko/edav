# edav

A self-hosted CalDAV, CardDAV and WebDAV server: calendars and contacts you own,
served from a single static binary with SQLite behind it.

It ships with a small htmx admin interface for managing users and collections.
There is nothing to install alongside it — no PHP, no separate database server.

## Status

Early. Currently loads its configuration, migrates the database and serves
`GET /healthz`. No DAV endpoints yet.

## Running

```sh
EDAV_ADMIN_PASSWORD=… make run
curl localhost:8080/healthz
```

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
