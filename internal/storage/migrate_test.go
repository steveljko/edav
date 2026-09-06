package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateCreatesSchema(t *testing.T) {
	db := testDB(t)

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Strings(got)

	want := []string{"collections", "object_changes", "objects", "schema_migrations", "sessions", "users"}
	if len(got) != len(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tables = %v, want %v", got, want)
			break
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	defer db.Close()

	var before int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if before == 0 {
		t.Fatal("no migrations applied")
	}

	for i := 0; i < 3; i++ {
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("Migrate() run %d = %v", i+2, err)
		}
	}

	var after int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if after != before {
		t.Errorf("applied migrations = %d after reruns, want %d", after, before)
	}
}

func TestMigrateReopensExistingFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open() = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES ('alice', 'x', 0, 0)`,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	db.Close()

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open() = %v", err)
	}
	defer db.Close()

	var username string
	if err := db.QueryRow(`SELECT username FROM users`).Scan(&username); err != nil {
		t.Fatalf("select user: %v", err)
	}
	if username != "alice" {
		t.Errorf("username = %q, want %q", username, "alice")
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := testDB(t)

	_, err := db.Exec(
		`INSERT INTO collections (owner_id, type, uri, ctag, created_at, updated_at)
		 VALUES (999, 'calendar', 'work', 'c1', 0, 0)`,
	)
	if err == nil {
		t.Fatal("insert with dangling owner_id succeeded, want foreign key violation")
	}
}

func TestCascadeDeleteRemovesCollections(t *testing.T) {
	db := testDB(t)

	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES (1, 'bob', 'x', 0, 0)`,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO collections (owner_id, type, uri, ctag, created_at, updated_at)
		 VALUES (1, 'calendar', 'work', 'c1', 0, 0)`,
	); err != nil {
		t.Fatalf("insert collection: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM collections`).Scan(&n); err != nil {
		t.Fatalf("count collections: %v", err)
	}
	if n != 0 {
		t.Errorf("collections = %d after owner delete, want 0", n)
	}
}

func TestParseMigrationName(t *testing.T) {
	tests := []struct {
		filename    string
		wantVersion int
		wantName    string
		wantErr     bool
	}{
		{filename: "0001_initial_schema.sql", wantVersion: 1, wantName: "initial_schema"},
		{filename: "0042_add_thing.sql", wantVersion: 42, wantName: "add_thing"},
		{filename: "initial.sql", wantErr: true},
		{filename: "abc_initial.sql", wantErr: true},
		{filename: "0000_zero.sql", wantErr: true},
		{filename: "0001_.sql", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			version, name, err := parseMigrationName(tt.filename)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseMigrationName(%q) = %d, %q, nil; want error", tt.filename, version, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMigrationName(%q) = %v", tt.filename, err)
			}
			if version != tt.wantVersion || name != tt.wantName {
				t.Errorf("parseMigrationName(%q) = %d, %q; want %d, %q",
					tt.filename, version, name, tt.wantVersion, tt.wantName)
			}
		})
	}
}

func TestLoadMigrationsAreOrderedAndUnique(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations() = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("loadMigrations() returned nothing")
	}

	for i, m := range migrations {
		if m.sql == "" {
			t.Errorf("migration %d has empty body", m.version)
		}
		if i > 0 && migrations[i-1].version >= m.version {
			t.Errorf("migrations out of order at %d: %d then %d", i, migrations[i-1].version, m.version)
		}
	}
}
