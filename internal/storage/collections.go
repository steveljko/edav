package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

type CollectionType string

const (
	CollectionCalendar    CollectionType = "calendar"
	CollectionAddressBook CollectionType = "addressbook"
)

type Collection struct {
	ID          int64
	OwnerID     int64
	Type        CollectionType
	URI         string
	DisplayName string
	Description string
	Color       string
	Timezone    string
	CTag        string
	SyncSeq     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const collectionColumns = `id, owner_id, type, uri, display_name, description, color, timezone,
	ctag, sync_seq, created_at, updated_at`

func scanCollection(row interface{ Scan(...any) error }) (*Collection, error) {
	var c Collection
	var created, updated int64
	if err := row.Scan(&c.ID, &c.OwnerID, &c.Type, &c.URI, &c.DisplayName, &c.Description,
		&c.Color, &c.Timezone, &c.CTag, &c.SyncSeq, &created, &updated); err != nil {
		return nil, err
	}
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.UpdatedAt = time.Unix(updated, 0).UTC()
	return &c, nil
}

func CreateCollection(ctx context.Context, db *sql.DB, c *Collection) (*Collection, error) {
	if c.Type != CollectionCalendar && c.Type != CollectionAddressBook {
		return nil, fmt.Errorf("create collection %q: unknown type %q", c.URI, c.Type)
	}
	if c.URI == "" {
		return nil, fmt.Errorf("create collection: uri must not be empty")
	}

	now := time.Now().Unix()
	res, err := db.ExecContext(ctx,
		`INSERT INTO collections (owner_id, type, uri, display_name, description, color, timezone,
		                          ctag, sync_seq, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		c.OwnerID, c.Type, c.URI, c.DisplayName, c.Description, c.Color, c.Timezone,
		ctagFor(0), now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create collection %q: %w", c.URI, ErrConflict)
		}
		return nil, fmt.Errorf("create collection %q: %w", c.URI, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create collection %q: last insert id: %w", c.URI, err)
	}
	return CollectionByID(ctx, db, id)
}

func CollectionByID(ctx context.Context, db *sql.DB, id int64) (*Collection, error) {
	c, err := scanCollection(db.QueryRowContext(ctx,
		`SELECT `+collectionColumns+` FROM collections WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("collection %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("collection %d: %w", id, err)
	}
	return c, nil
}

func CollectionByURI(ctx context.Context, db *sql.DB, ownerID int64, uri string) (*Collection, error) {
	c, err := scanCollection(db.QueryRowContext(ctx,
		`SELECT `+collectionColumns+` FROM collections WHERE owner_id = ? AND uri = ?`, ownerID, uri))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("collection %q: %w", uri, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("collection %q: %w", uri, err)
	}
	return c, nil
}

// ListCollections returns a user's collections, optionally restricted to one
// type. Pass an empty type for all of them.
func ListCollections(ctx context.Context, db *sql.DB, ownerID int64, typ CollectionType) ([]*Collection, error) {
	query := `SELECT ` + collectionColumns + ` FROM collections WHERE owner_id = ?`
	args := []any{ownerID}
	if typ != "" {
		query += ` AND type = ?`
		args = append(args, typ)
	}
	query += ` ORDER BY uri`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list collections for user %d: %w", ownerID, err)
	}
	defer rows.Close()

	var collections []*Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, fmt.Errorf("list collections for user %d: %w", ownerID, err)
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

// UpdateCollection writes the user-editable properties. It does not touch the
// ctag: those properties are not collection members, and bumping it would force
// every client into a needless resync.
func UpdateCollection(ctx context.Context, db *sql.DB, c *Collection) error {
	res, err := db.ExecContext(ctx,
		`UPDATE collections SET display_name = ?, description = ?, color = ?, timezone = ?, updated_at = ?
		 WHERE id = ?`,
		c.DisplayName, c.Description, c.Color, c.Timezone, time.Now().Unix(), c.ID)
	if err != nil {
		return fmt.Errorf("update collection %d: %w", c.ID, err)
	}
	return checkAffected(res, fmt.Sprintf("update collection %d", c.ID))
}

func DeleteCollection(ctx context.Context, db *sql.DB, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete collection %d: %w", id, err)
	}
	return checkAffected(res, fmt.Sprintf("delete collection %d", id))
}

func ctagFor(syncSeq int64) string {
	return strconv.FormatInt(syncSeq, 10)
}
