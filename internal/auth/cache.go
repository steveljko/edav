package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

// DefaultCredentialTTL is how long a verified password stays cached. Short
// enough that a password change takes effect promptly on its own, though a
// change also invalidates the entry outright.
const DefaultCredentialTTL = 5 * time.Minute

// maxCredentialEntries bounds the cache. One entry per credential in active
// use is a handful even for a large household, so reaching this means
// something is cycling credentials and the cache is not helping anyway.
const maxCredentialEntries = 1024

// credentialCache remembers that a password was verified, so that Basic
// authentication does not repeat the work on every request.
//
// Argon2id is deliberately expensive: a verification costs about 17ms and
// allocates 20MB. That is right for a login and wrong for a protocol where a
// single sync makes twenty requests, each carrying the same credentials.
type credentialCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration

	// key makes an entry's identity meaningless outside this process, so the
	// cache is not a table of password digests an attacker could carry away.
	keyOnce sync.Once
	key     []byte
}

func newCredentialCache(ttl time.Duration) *credentialCache {
	if ttl <= 0 {
		ttl = DefaultCredentialTTL
	}
	return &credentialCache{entries: make(map[string]time.Time), ttl: ttl}
}

// id derives the entry key. The stored hash is part of it, so changing a
// password invalidates every cached entry for that account without the cache
// needing to be told.
func (c *credentialCache) id(username, password, storedHash string) string {
	c.keyOnce.Do(func() {
		c.key = make([]byte, 32)
		if _, err := rand.Read(c.key); err != nil {
			panic("auth: read credential cache key: " + err.Error())
		}
	})

	mac := hmac.New(sha256.New, c.key)
	for _, part := range []string{username, password, storedHash} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return string(mac.Sum(nil))
}

// verified reports whether this exact credential was checked recently.
func (c *credentialCache) verified(username, password, storedHash string, now time.Time) bool {
	id := c.id(username, password, storedHash)

	c.mu.Lock()
	defer c.mu.Unlock()

	expires, ok := c.entries[id]
	if !ok {
		return false
	}
	if !now.Before(expires) {
		delete(c.entries, id)
		return false
	}
	return true
}

// remember records a successful verification. Only successes are cached:
// caching a failure would let a stale rejection outlive the password change
// that fixed it.
func (c *credentialCache) remember(username, password, storedHash string, now time.Time) {
	id := c.id(username, password, storedHash)

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= maxCredentialEntries {
		c.sweepLocked(now)
		if len(c.entries) >= maxCredentialEntries {
			// Everything still live: start over rather than grow without
			// bound. The cost is that active sessions pay for one more
			// verification.
			c.entries = make(map[string]time.Time)
		}
	}
	c.entries[id] = now.Add(c.ttl)
}

func (c *credentialCache) sweepLocked(now time.Time) {
	for id, expires := range c.entries {
		if !now.Before(expires) {
			delete(c.entries, id)
		}
	}
}

func (c *credentialCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
