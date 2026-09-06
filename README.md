# edav

A self-hosted CalDAV, CardDAV and WebDAV server: calendars and contacts you own,
served from a single static binary with SQLite behind it.

It ships with a small htmx admin interface for managing users and collections.
There is nothing to install alongside it — no PHP, no separate database server.

## Status

Early. Currently serves `GET /healthz` and nothing else.

## Running

```sh
make run
```

Listens on `:8080` by default; override with `EDAV_ADDR`.
