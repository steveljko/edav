// Package storage owns the SQLite database: connection setup, schema
// migrations and queries.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

// Open opens the database at path, applies connection pragmas and runs any
// pending migrations. Pass ":memory:" for an ephemeral database.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to %s: %w", path, err)
	}
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return db, nil
}

func dsn(path string) string {
	pragmas := url.Values{}
	pragmas.Add("_pragma", "foreign_keys(1)")
	pragmas.Add("_pragma", "busy_timeout(5000)")
	// Every transaction here reads before it writes. Left deferred, the write
	// upgrade fails with SQLITE_BUSY the moment another connection has written
	// since the read began, and busy_timeout cannot wait that out because the
	// snapshot is already stale. Taking the write lock up front turns the race
	// into a wait.
	pragmas.Add("_txlock", "immediate")
	if path != ":memory:" {
		// WAL survives across connections and is meaningless for in-memory
		// databases, which the driver rejects.
		pragmas.Add("_pragma", "journal_mode(WAL)")
		pragmas.Add("_pragma", "synchronous(NORMAL)")
	}

	if strings.HasPrefix(path, "file:") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return path + sep + pragmas.Encode()
	}
	return "file:" + path + "?" + pragmas.Encode()
}
