package auth

import (
	"context"
	"sync"
	"time"
)

// Failure limits for one account name. Generous enough that a person mistyping
// a password never meets them, tight enough that guessing is not worth
// attempting.
const (
	maxFailures    = 8
	failureWindow  = 15 * time.Minute
	maxThrottleKey = 4096
)

// maxConcurrentVerifications bounds how many Argon2id verifications run at
// once. Each allocates 20MB, so without a bound a few dozen simultaneous
// requests carrying wrong credentials exhaust memory whether or not the
// credentials are ever valid. Requests wait for a slot rather than for
// memory the machine does not have.
const maxConcurrentVerifications = 4

var verificationSlots = make(chan struct{}, maxConcurrentVerifications)

// withVerificationSlot runs fn while holding one of the verification slots. A
// caller whose request is already cancelled gives up its place rather than
// spending 17ms on an answer nobody will read.
func withVerificationSlot(ctx context.Context, fn func() error) error {
	select {
	case verificationSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-verificationSlots }()

	return fn()
}

// throttle counts recent failed attempts per account name and refuses further
// ones for a while.
//
// It deliberately keys on the submitted name rather than the client address.
// Behind a reverse proxy every client shares one address, so an address-keyed
// limit would let one misconfigured client lock out the household; and the
// address a proxy reports is only as trustworthy as the proxy's own headers.
// Limiting by address belongs in the proxy, which knows the real one.
type throttle struct {
	mu       sync.Mutex
	failures map[string]*failureRecord
	max      int
	window   time.Duration
}

type failureRecord struct {
	count int
	first time.Time
}

func newThrottle(max int, window time.Duration) *throttle {
	return &throttle{
		failures: make(map[string]*failureRecord),
		max:      max,
		window:   window,
	}
}

// allow reports whether another attempt may be made for this name, and how
// long to wait when it may not.
func (t *throttle) allow(key string, now time.Time) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	record, ok := t.failures[key]
	if !ok {
		return 0, true
	}
	if now.Sub(record.first) >= t.window {
		delete(t.failures, key)
		return 0, true
	}
	if record.count < t.max {
		return 0, true
	}
	return record.first.Add(t.window).Sub(now), false
}

// fail records an unsuccessful attempt. The window runs from the first
// failure, so a burst does not keep extending its own punishment.
func (t *throttle) fail(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	record, ok := t.failures[key]
	if !ok || now.Sub(record.first) >= t.window {
		if len(t.failures) >= maxThrottleKey {
			t.sweepLocked(now)
		}
		t.failures[key] = &failureRecord{count: 1, first: now}
		return
	}
	record.count++
}

// succeed clears the record, so someone who eventually remembers their
// password is not left waiting out a window.
func (t *throttle) succeed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, key)
}

func (t *throttle) sweepLocked(now time.Time) {
	for key, record := range t.failures {
		if now.Sub(record.first) >= t.window {
			delete(t.failures, key)
		}
	}
	// Names are attacker-chosen, so a flood of distinct ones must not grow the
	// map without bound. Dropping the oldest records only forgives attempts
	// that were already being made too slowly to matter.
	if len(t.failures) >= maxThrottleKey {
		t.failures = make(map[string]*failureRecord)
	}
}

func (t *throttle) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.failures)
}

// LoginThrottle limits failed sign-in attempts for one account name. The DAV
// endpoint keeps its own; a login form needs one too, and the two are separate
// so that guessing at one does not consume the other's budget.
type LoginThrottle struct{ t *throttle }

func NewLoginThrottle() *LoginThrottle {
	return &LoginThrottle{t: newThrottle(maxFailures, failureWindow)}
}

// Allow reports whether an attempt may be made, and how long to wait when it
// may not.
func (l *LoginThrottle) Allow(username string, now time.Time) (time.Duration, bool) {
	return l.t.allow(username, now)
}

func (l *LoginThrottle) Fail(username string, now time.Time) { l.t.fail(username, now) }
func (l *LoginThrottle) Succeed(username string)             { l.t.succeed(username) }
