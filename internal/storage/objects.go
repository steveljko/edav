package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type ChangeType string

const (
	ChangeCreated ChangeType = "created"
	ChangeUpdated ChangeType = "updated"
	ChangeDeleted ChangeType = "deleted"
)

// Object is a stored calendar or address object. Raw holds the client's bytes
// verbatim; every other payload field is derived from it on write and exists
// only so queries can be answered in SQL.
type Object struct {
	ID            int64
	CollectionID  int64
	URI           string
	ETag          string
	Raw           []byte
	UID           string
	ComponentType string
	DisplayName   string
	StartAt       *time.Time
	EndAt         *time.Time
	Recurring     bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const objectColumns = `id, collection_id, uri, etag, raw, uid, component_type, display_name,
	start_at, end_at, recurring, created_at, updated_at`

func scanObject(row interface{ Scan(...any) error }) (*Object, error) {
	var o Object
	var start, end sql.NullInt64
	var created, updated int64
	if err := row.Scan(&o.ID, &o.CollectionID, &o.URI, &o.ETag, &o.Raw, &o.UID, &o.ComponentType,
		&o.DisplayName, &start, &end, &o.Recurring, &created, &updated); err != nil {
		return nil, err
	}
	if start.Valid {
		t := time.Unix(start.Int64, 0).UTC()
		o.StartAt = &t
	}
	if end.Valid {
		t := time.Unix(end.Int64, 0).UTC()
		o.EndAt = &t
	}
	o.CreatedAt = time.Unix(created, 0).UTC()
	o.UpdatedAt = time.Unix(updated, 0).UTC()
	return &o, nil
}

// ETagFor derives an object's ETag from its stored bytes, so that the ETag
// changes if and only if those bytes change.
func ETagFor(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// PutObject inserts or replaces an object and, when the stored bytes actually
// change, advances the collection's sync sequence and records the change.
// Rewriting an object with identical bytes is a no-op: the ETag and ctag stay
// put and clients are not sent looking for a change that did not happen.
func PutObject(ctx context.Context, db *sql.DB, obj *Object) (*Object, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("put object %q: begin: %w", obj.URI, err)
	}
	defer tx.Rollback()

	var existingID int64
	var existingETag string
	err = tx.QueryRowContext(ctx,
		`SELECT id, etag FROM objects WHERE collection_id = ? AND uri = ?`,
		obj.CollectionID, obj.URI).Scan(&existingID, &existingETag)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("put object %q: look up existing: %w", obj.URI, err)
	}
	exists := existingID != 0

	etag := ETagFor(obj.Raw)
	if exists && etag == existingETag {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("put object %q: commit: %w", obj.URI, err)
		}
		return ObjectByURI(ctx, db, obj.CollectionID, obj.URI)
	}

	now := time.Now().Unix()
	start, end := nullableUnix(obj.StartAt), nullableUnix(obj.EndAt)

	if exists {
		_, err = tx.ExecContext(ctx,
			`UPDATE objects SET etag = ?, raw = ?, uid = ?, component_type = ?, display_name = ?,
			                    start_at = ?, end_at = ?, recurring = ?, updated_at = ?
			 WHERE id = ?`,
			etag, obj.Raw, obj.UID, obj.ComponentType, obj.DisplayName,
			start, end, obj.Recurring, now, existingID)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO objects (collection_id, uri, etag, raw, uid, component_type, display_name,
			                      start_at, end_at, recurring, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			obj.CollectionID, obj.URI, etag, obj.Raw, obj.UID, obj.ComponentType, obj.DisplayName,
			start, end, obj.Recurring, now, now)
	}
	if err != nil {
		return nil, fmt.Errorf("put object %q: %w", obj.URI, err)
	}

	change := ChangeCreated
	if exists {
		change = ChangeUpdated
	}
	if err := recordChange(ctx, tx, obj.CollectionID, obj.URI, change, now); err != nil {
		return nil, fmt.Errorf("put object %q: %w", obj.URI, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("put object %q: commit: %w", obj.URI, err)
	}
	return ObjectByURI(ctx, db, obj.CollectionID, obj.URI)
}

func ObjectByURI(ctx context.Context, db *sql.DB, collectionID int64, uri string) (*Object, error) {
	o, err := scanObject(db.QueryRowContext(ctx,
		`SELECT `+objectColumns+` FROM objects WHERE collection_id = ? AND uri = ?`, collectionID, uri))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("object %q: %w", uri, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("object %q: %w", uri, err)
	}
	return o, nil
}

func ListObjects(ctx context.Context, db *sql.DB, collectionID int64) ([]*Object, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+objectColumns+` FROM objects WHERE collection_id = ? ORDER BY uri`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list objects in collection %d: %w", collectionID, err)
	}
	defer rows.Close()

	var objects []*Object
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, fmt.Errorf("list objects in collection %d: %w", collectionID, err)
		}
		objects = append(objects, o)
	}
	return objects, rows.Err()
}

// ObjectsInTimeRange narrows a collection to the objects that could overlap
// [from, to). It is a prefilter, not an answer: a recurring object is kept
// whenever its overall span reaches the range, but only expanding its rule can
// say whether an occurrence actually falls inside. Objects with no start, such
// as a VTODO carrying neither DTSTART nor DUE, always survive.
func ObjectsInTimeRange(ctx context.Context, db *sql.DB, collectionID int64, from, to time.Time) ([]*Object, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+objectColumns+` FROM objects
		  WHERE collection_id = ?
		    AND (start_at IS NULL
		         OR (start_at < ? AND (end_at IS NULL OR end_at > ?)))
		  ORDER BY uri`,
		collectionID, to.Unix(), from.Unix())
	if err != nil {
		return nil, fmt.Errorf("time range query in collection %d: %w", collectionID, err)
	}
	defer rows.Close()

	var objects []*Object
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, fmt.Errorf("time range query in collection %d: %w", collectionID, err)
		}
		objects = append(objects, o)
	}
	return objects, rows.Err()
}

func DeleteObject(ctx context.Context, db *sql.DB, collectionID int64, uri string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete object %q: begin: %w", uri, err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM objects WHERE collection_id = ? AND uri = ?`, collectionID, uri)
	if err != nil {
		return fmt.Errorf("delete object %q: %w", uri, err)
	}
	if err := checkAffected(res, fmt.Sprintf("delete object %q", uri)); err != nil {
		return err
	}

	if err := recordChange(ctx, tx, collectionID, uri, ChangeDeleted, time.Now().Unix()); err != nil {
		return fmt.Errorf("delete object %q: %w", uri, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete object %q: commit: %w", uri, err)
	}
	return nil
}

// recordChange advances the collection's sync sequence and appends to its
// change log. The UPDATE ... RETURNING keeps the increment and the read of the
// new value in one statement, so concurrent writers cannot be handed the same
// sequence number.
func recordChange(ctx context.Context, tx *sql.Tx, collectionID int64, uri string, change ChangeType, now int64) error {
	var seq int64
	err := tx.QueryRowContext(ctx,
		`UPDATE collections SET sync_seq = sync_seq + 1, ctag = CAST(sync_seq + 1 AS TEXT), updated_at = ?
		 WHERE id = ? RETURNING sync_seq`, now, collectionID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("collection %d: %w", collectionID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("bump collection %d: %w", collectionID, err)
	}

	// A URI can change more than once; only the latest matters to a syncing
	// client, and keeping one row per URI stops the log growing without bound.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM object_changes WHERE collection_id = ? AND object_uri = ?`,
		collectionID, uri); err != nil {
		return fmt.Errorf("prune change log for collection %d: %w", collectionID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO object_changes (collection_id, seq, object_uri, change_type, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		collectionID, seq, uri, change, now); err != nil {
		return fmt.Errorf("record change for collection %d: %w", collectionID, err)
	}
	return nil
}

func nullableUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}
