package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("storage: not found")

// ErrConflict is returned when a write violates a uniqueness constraint.
var ErrConflict = errors.New("storage: already exists")

type User struct {
	ID           int64
	Username     string
	DisplayName  string
	Email        string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const userColumns = `id, username, display_name, email, password_hash, is_admin, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created, updated int64
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email,
		&u.PasswordHash, &u.IsAdmin, &created, &updated); err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return &u, nil
}

func CreateUser(ctx context.Context, db *sql.DB, u *User) (*User, error) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, display_name, email, password_hash, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.DisplayName, u.Email, u.PasswordHash, u.IsAdmin, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create user %q: %w", u.Username, ErrConflict)
		}
		return nil, fmt.Errorf("create user %q: %w", u.Username, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create user %q: last insert id: %w", u.Username, err)
	}
	return UserByID(ctx, db, id)
}

func UserByID(ctx context.Context, db *sql.DB, id int64) (*User, error) {
	u, err := scanUser(db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", id, err)
	}
	return u, nil
}

// UserByUsername looks up a user case-insensitively, matching the unique index
// on users.username.
func UserByUsername(ctx context.Context, db *sql.DB, username string) (*User, error) {
	u, err := scanUser(db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("user %q: %w", username, err)
	}
	return u, nil
}

func ListUsers(ctx context.Context, db *sql.DB) ([]*User, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SearchUsers returns one page of users, optionally narrowed by name, and how
// many match in total.
func SearchUsers(ctx context.Context, db *sql.DB, query string, limit, offset int) ([]*User, int, error) {
	where := ""
	var args []any

	if query = strings.TrimSpace(query); query != "" {
		pattern := "%" + escapeLike(query) + "%"
		where = `WHERE username LIKE ? ESCAPE '\' OR display_name LIKE ? ESCAPE '\'
			OR email LIKE ? ESCAPE '\'`
		args = append(args, pattern, pattern, pattern)
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	if limit <= 0 {
		limit = 100
	}

	rows, err := db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users `+where+
			` ORDER BY username COLLATE NOCASE LIMIT ? OFFSET ?`,
		append(args, limit, max(offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("search users: %w", err)
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func UpdateUser(ctx context.Context, db *sql.DB, u *User) error {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET username = ?, display_name = ?, email = ?, is_admin = ?, updated_at = ?
		 WHERE id = ?`,
		u.Username, u.DisplayName, u.Email, u.IsAdmin, time.Now().Unix(), u.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("update user %d: %w", u.ID, ErrConflict)
		}
		return fmt.Errorf("update user %d: %w", u.ID, err)
	}
	return checkAffected(res, fmt.Sprintf("update user %d", u.ID))
}

// SetPassword stores an already-hashed password. Hashing belongs to
// internal/auth; storage never sees a plaintext password.
func SetPassword(ctx context.Context, db *sql.DB, id int64, passwordHash string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("set password for user %d: %w", id, err)
	}
	return checkAffected(res, fmt.Sprintf("set password for user %d", id))
}

func DeleteUser(ctx context.Context, db *sql.DB, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user %d: %w", id, err)
	}
	return checkAffected(res, fmt.Sprintf("delete user %d", id))
}

// EnsureAdmin creates the configured admin account if it is missing, and
// promotes it if it exists without the flag. An existing account's password is
// left alone so that a password changed in the UI is not reset on restart.
func EnsureAdmin(ctx context.Context, db *sql.DB, username, passwordHash string) (*User, error) {
	u, err := UserByUsername(ctx, db, username)
	switch {
	case errors.Is(err, ErrNotFound):
		return CreateUser(ctx, db, &User{
			Username:     username,
			DisplayName:  username,
			PasswordHash: passwordHash,
			IsAdmin:      true,
		})
	case err != nil:
		return nil, err
	case !u.IsAdmin:
		u.IsAdmin = true
		if err := UpdateUser(ctx, db, u); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func checkAffected(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: rows affected: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return nil
}

// The driver does not expose typed constraint errors, so the message is all we
// have to go on.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
