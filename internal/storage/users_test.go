package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func mustCreateUser(t *testing.T, db *sql.DB, username string) *User {
	t.Helper()
	u, err := CreateUser(context.Background(), db, &User{
		Username:     username,
		DisplayName:  username,
		Email:        username + "@example.com",
		PasswordHash: "$argon2id$fake",
	})
	if err != nil {
		t.Fatalf("CreateUser(%q) = %v", username, err)
	}
	return u
}

func TestCreateAndReadUser(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	created := mustCreateUser(t, db, "alice")
	if created.ID == 0 {
		t.Error("CreateUser() returned id 0")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("CreateUser() left timestamps zero")
	}
	if created.IsAdmin {
		t.Error("CreateUser() defaulted IsAdmin to true")
	}

	byID, err := UserByID(ctx, db, created.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if byID.Username != "alice" || byID.Email != "alice@example.com" {
		t.Errorf("UserByID() = %+v", byID)
	}

	byName, err := UserByUsername(ctx, db, "ALICE")
	if err != nil {
		t.Fatalf("UserByUsername(ALICE) = %v, want case-insensitive match", err)
	}
	if byName.ID != created.ID {
		t.Errorf("UserByUsername() id = %d, want %d", byName.ID, created.ID)
	}
}

func TestUserNotFound(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if _, err := UserByID(ctx, db, 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserByID() = %v, want ErrNotFound", err)
	}
	if _, err := UserByUsername(ctx, db, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserByUsername() = %v, want ErrNotFound", err)
	}
	if err := DeleteUser(ctx, db, 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteUser() = %v, want ErrNotFound", err)
	}
	if err := SetPassword(ctx, db, 404, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetPassword() = %v, want ErrNotFound", err)
	}
}

func TestCreateUserRejectsDuplicateUsername(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	mustCreateUser(t, db, "alice")

	for _, username := range []string{"alice", "ALICE"} {
		_, err := CreateUser(ctx, db, &User{Username: username, PasswordHash: "x"})
		if !errors.Is(err, ErrConflict) {
			t.Errorf("CreateUser(%q) = %v, want ErrConflict", username, err)
		}
	}
}

func TestUpdateUser(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")

	u.DisplayName = "Alice Liddell"
	u.Email = "alice@wonderland.test"
	u.IsAdmin = true
	if err := UpdateUser(ctx, db, u); err != nil {
		t.Fatalf("UpdateUser() = %v", err)
	}

	got, err := UserByID(ctx, db, u.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if got.DisplayName != "Alice Liddell" || got.Email != "alice@wonderland.test" || !got.IsAdmin {
		t.Errorf("UserByID() = %+v after update", got)
	}
	if got.PasswordHash != u.PasswordHash {
		t.Error("UpdateUser() changed the password hash")
	}
}

func TestUpdateUserRejectsDuplicateUsername(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	mustCreateUser(t, db, "alice")
	bob := mustCreateUser(t, db, "bob")

	bob.Username = "alice"
	if err := UpdateUser(ctx, db, bob); !errors.Is(err, ErrConflict) {
		t.Errorf("UpdateUser() = %v, want ErrConflict", err)
	}
}

func TestSetPassword(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")

	if err := SetPassword(ctx, db, u.ID, "$argon2id$new"); err != nil {
		t.Fatalf("SetPassword() = %v", err)
	}

	got, err := UserByID(ctx, db, u.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if got.PasswordHash != "$argon2id$new" {
		t.Errorf("PasswordHash = %q, want %q", got.PasswordHash, "$argon2id$new")
	}
}

func TestListUsersIsSorted(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	for _, name := range []string{"carol", "alice", "Bob"} {
		mustCreateUser(t, db, name)
	}

	users, err := ListUsers(ctx, db)
	if err != nil {
		t.Fatalf("ListUsers() = %v", err)
	}
	want := []string{"alice", "Bob", "carol"}
	if len(users) != len(want) {
		t.Fatalf("ListUsers() returned %d users, want %d", len(users), len(want))
	}
	for i, u := range users {
		if u.Username != want[i] {
			t.Errorf("ListUsers()[%d] = %q, want %q", i, u.Username, want[i])
		}
	}
}

func TestEnsureAdmin(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	created, err := EnsureAdmin(ctx, db, "admin", "$argon2id$first")
	if err != nil {
		t.Fatalf("EnsureAdmin() = %v", err)
	}
	if !created.IsAdmin {
		t.Error("EnsureAdmin() created a non-admin user")
	}

	again, err := EnsureAdmin(ctx, db, "admin", "$argon2id$second")
	if err != nil {
		t.Fatalf("EnsureAdmin() second call = %v", err)
	}
	if again.ID != created.ID {
		t.Errorf("EnsureAdmin() created a second account: id %d then %d", created.ID, again.ID)
	}
	if again.PasswordHash != "$argon2id$first" {
		t.Error("EnsureAdmin() overwrote the stored password of an existing account")
	}
}

func TestEnsureAdminPromotesExistingUser(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")

	if _, err := EnsureAdmin(ctx, db, "alice", "$argon2id$ignored"); err != nil {
		t.Fatalf("EnsureAdmin() = %v", err)
	}

	got, err := UserByID(ctx, db, u.ID)
	if err != nil {
		t.Fatalf("UserByID() = %v", err)
	}
	if !got.IsAdmin {
		t.Error("EnsureAdmin() did not promote the existing user")
	}
	if got.PasswordHash != u.PasswordHash {
		t.Error("EnsureAdmin() overwrote the existing user's password")
	}
}
