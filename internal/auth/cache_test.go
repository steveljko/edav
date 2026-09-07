package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/steveljko/edav/internal/storage"
)

func TestCredentialCacheRemembersAVerifiedPassword(t *testing.T) {
	c := newCredentialCache(time.Minute)
	now := time.Now()

	if c.verified("alice", "secret", "$hash", now) {
		t.Error("an empty cache reported a verification")
	}

	c.remember("alice", "secret", "$hash", now)
	if !c.verified("alice", "secret", "$hash", now) {
		t.Error("a remembered credential was not found")
	}
}

func TestCredentialCacheDistinguishesCredentials(t *testing.T) {
	c := newCredentialCache(time.Minute)
	now := time.Now()
	c.remember("alice", "secret", "$hash", now)

	tests := []struct {
		name                           string
		username, password, storedHash string
	}{
		{"another password", "alice", "wrong", "$hash"},
		{"another user", "bob", "secret", "$hash"},
		// The stored hash is part of the entry, so a password change
		// invalidates it without the cache being told.
		{"the password was changed", "alice", "secret", "$newhash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if c.verified(tt.username, tt.password, tt.storedHash, now) {
				t.Error("the cache matched a credential it should not have")
			}
		})
	}
}

func TestCredentialCacheExpires(t *testing.T) {
	c := newCredentialCache(time.Minute)
	now := time.Now()
	c.remember("alice", "secret", "$hash", now)

	if !c.verified("alice", "secret", "$hash", now.Add(59*time.Second)) {
		t.Error("the entry expired early")
	}
	if c.verified("alice", "secret", "$hash", now.Add(time.Minute)) {
		t.Error("the entry outlived its TTL")
	}
	if c.len() != 0 {
		t.Error("an expired entry was not dropped on lookup")
	}
}

func TestCredentialCacheStaysBounded(t *testing.T) {
	c := newCredentialCache(time.Hour)
	now := time.Now()

	for i := range maxCredentialEntries * 2 {
		c.remember("user", string(rune(i)), "$hash", now)
		if c.len() > maxCredentialEntries {
			t.Fatalf("cache grew to %d entries, past the %d bound", c.len(), maxCredentialEntries)
		}
	}
}

func TestCredentialCacheSweepsExpiredBeforeClearing(t *testing.T) {
	c := newCredentialCache(time.Minute)
	now := time.Now()

	for i := range maxCredentialEntries {
		c.remember("user", string(rune(i)), "$hash", now)
	}
	before := c.len()

	// Everything above is now stale, so the next write should sweep rather
	// than throw the whole cache away.
	later := now.Add(2 * time.Minute)
	c.remember("user", "fresh", "$hash", later)

	if c.len() >= before {
		t.Errorf("expired entries were not swept: %d entries", c.len())
	}
	if !c.verified("user", "fresh", "$hash", later) {
		t.Error("the new entry was lost")
	}
}

// Entry keys must not be usable outside the process that made them.
func TestCredentialCacheKeysAreProcessScoped(t *testing.T) {
	a, b := newCredentialCache(time.Minute), newCredentialCache(time.Minute)

	if a.id("alice", "secret", "$hash") == b.id("alice", "secret", "$hash") {
		t.Error("two caches derived the same key for one credential")
	}
	if id := a.id("alice", "secret", "$hash"); id == a.id("alice", "secret", "$other") {
		t.Error("the stored hash does not affect the key")
	} else if len(id) != 32 {
		t.Errorf("key length = %d, want a 32 byte digest", len(id))
	}
}

func davHarness(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()
	db := testDB(t)
	testUser(t, db, "alice", "hunter2hunter2")

	handler := RequireBasicAuth(db, "edav")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return db, handler
}

func request(t *testing.T, h http.Handler, username, password string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dav/", nil)
	req.SetBasicAuth(username, password)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// A repeated request is the common case, and it must still be authenticated
// rather than waved through on the strength of the username alone.
func TestBasicAuthCachesAcrossRequests(t *testing.T) {
	_, h := davHarness(t)

	for i := range 3 {
		if code := request(t, h, "alice", "hunter2hunter2"); code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i, code)
		}
	}
	if code := request(t, h, "alice", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("a wrong password after a cached success = %d, want 401", code)
	}
}

// The case that would lose people access to their own data, or leave it open
// after they revoked it.
func TestChangingAPasswordInvalidatesTheCache(t *testing.T) {
	db, h := davHarness(t)

	if code := request(t, h, "alice", "hunter2hunter2"); code != http.StatusOK {
		t.Fatalf("initial request = %d, want 200", code)
	}

	u, err := storage.UserByUsername(t.Context(), db, "alice")
	if err != nil {
		t.Fatalf("UserByUsername() = %v", err)
	}
	newHash, err := hashPasswordWith("a different password", testParams)
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}
	if err := storage.SetPassword(t.Context(), db, u.ID, newHash); err != nil {
		t.Fatalf("SetPassword() = %v", err)
	}

	if code := request(t, h, "alice", "hunter2hunter2"); code != http.StatusUnauthorized {
		t.Errorf("the old password still worked after a change: %d, want 401", code)
	}
	if code := request(t, h, "alice", "a different password"); code != http.StatusOK {
		t.Errorf("the new password was rejected: %d, want 200", code)
	}
}

// Caching must not make an unknown username cheaper than a known one, or the
// endpoint starts reporting which accounts exist.
func TestUnknownUsernameIsNotCached(t *testing.T) {
	db := testDB(t)
	testUser(t, db, "alice", "hunter2hunter2")
	cache := newCredentialCache(time.Minute)

	for range 3 {
		if _, err := authenticate(t.Context(), db, "mallory", "guess", cache); err == nil {
			t.Fatal("an unknown user authenticated")
		}
	}
	if cache.len() != 0 {
		t.Errorf("cache holds %d entries after failed logins, want 0", cache.len())
	}
}

func TestFailedPasswordIsNotCached(t *testing.T) {
	db := testDB(t)
	testUser(t, db, "alice", "hunter2hunter2")
	cache := newCredentialCache(time.Minute)

	if _, err := authenticate(t.Context(), db, "alice", "wrong", cache); err == nil {
		t.Fatal("a wrong password authenticated")
	}
	if cache.len() != 0 {
		t.Errorf("cache holds %d entries after a failed login, want 0", cache.len())
	}
}
