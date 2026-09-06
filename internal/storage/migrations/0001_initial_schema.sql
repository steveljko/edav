CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL,
    display_name  TEXT    NOT NULL DEFAULT '',
    email         TEXT    NOT NULL DEFAULT '',
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE UNIQUE INDEX users_username_idx ON users (username COLLATE NOCASE);

CREATE TABLE collections (
    id           INTEGER PRIMARY KEY,
    owner_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type         TEXT    NOT NULL CHECK (type IN ('calendar', 'addressbook')),
    uri          TEXT    NOT NULL,
    display_name TEXT    NOT NULL DEFAULT '',
    description  TEXT    NOT NULL DEFAULT '',
    color        TEXT    NOT NULL DEFAULT '',
    timezone     TEXT    NOT NULL DEFAULT '',
    ctag         TEXT    NOT NULL,
    -- Monotonic per-collection counter. The value stamped on the newest row in
    -- object_changes; sync tokens are a position in this sequence.
    sync_seq     INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE UNIQUE INDEX collections_owner_uri_idx ON collections (owner_id, uri);

CREATE TABLE objects (
    id             INTEGER PRIMARY KEY,
    collection_id  INTEGER NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    uri            TEXT    NOT NULL,
    etag           TEXT    NOT NULL,
    -- The client's bytes, verbatim. Never regenerated from a parsed model:
    -- clients store custom X- properties that our serializer would drop.
    raw            BLOB    NOT NULL,
    -- Derived from raw on write, for querying only.
    uid            TEXT    NOT NULL DEFAULT '',
    component_type TEXT    NOT NULL DEFAULT '',
    start_at       INTEGER,
    end_at         INTEGER,
    recurring      INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE UNIQUE INDEX objects_collection_uri_idx ON objects (collection_id, uri);
CREATE INDEX objects_collection_uid_idx ON objects (collection_id, uid);
CREATE INDEX objects_time_range_idx ON objects (collection_id, start_at, end_at);

CREATE TABLE object_changes (
    id            INTEGER PRIMARY KEY,
    collection_id INTEGER NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    object_uri    TEXT    NOT NULL,
    change_type   TEXT    NOT NULL CHECK (change_type IN ('created', 'updated', 'deleted')),
    created_at    INTEGER NOT NULL
);

CREATE UNIQUE INDEX object_changes_collection_seq_idx ON object_changes (collection_id, seq);

CREATE TABLE sessions (
    token      TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
