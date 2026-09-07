package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestThrottleAllowsUntilTheLimit(t *testing.T) {
	th := newThrottle(3, time.Minute)
	now := time.Now()

	for i := range 3 {
		if _, ok := th.allow("alice", now); !ok {
			t.Fatalf("attempt %d was refused before the limit", i+1)
		}
		th.fail("alice", now)
	}

	retry, ok := th.allow("alice", now)
	if ok {
		t.Fatal("the fourth attempt was allowed")
	}
	if retry <= 0 || retry > time.Minute {
		t.Errorf("retry after = %v, want a wait inside the window", retry)
	}
}

func TestThrottleWindowExpires(t *testing.T) {
	th := newThrottle(2, time.Minute)
	now := time.Now()

	th.fail("alice", now)
	th.fail("alice", now)
	if _, ok := th.allow("alice", now); ok {
		t.Fatal("attempts were allowed at the limit")
	}

	if _, ok := th.allow("alice", now.Add(59*time.Second)); ok {
		t.Error("the window ended early")
	}
	if _, ok := th.allow("alice", now.Add(time.Minute)); !ok {
		t.Error("the window did not end")
	}
}

// The window runs from the first failure, so a burst cannot keep extending its
// own punishment indefinitely.
func TestThrottleWindowDoesNotSlideOnFurtherFailures(t *testing.T) {
	th := newThrottle(2, time.Minute)
	now := time.Now()

	th.fail("alice", now)
	th.fail("alice", now.Add(30*time.Second))
	th.fail("alice", now.Add(45*time.Second))

	if _, ok := th.allow("alice", now.Add(time.Minute)); !ok {
		t.Error("later failures pushed the window out")
	}
}

// Someone who eventually remembers their password should not be left waiting.
func TestThrottleClearsOnSuccess(t *testing.T) {
	th := newThrottle(3, time.Minute)
	now := time.Now()

	th.fail("alice", now)
	th.fail("alice", now)
	th.succeed("alice")

	for range 3 {
		if _, ok := th.allow("alice", now); !ok {
			t.Fatal("the record was not cleared on success")
		}
		th.fail("alice", now)
	}
}

func TestThrottleIsPerName(t *testing.T) {
	th := newThrottle(2, time.Minute)
	now := time.Now()

	th.fail("alice", now)
	th.fail("alice", now)

	if _, ok := th.allow("alice", now); ok {
		t.Error("alice was not throttled")
	}
	if _, ok := th.allow("bob", now); !ok {
		t.Error("bob was throttled by alice's failures")
	}
}

// Names are chosen by whoever is guessing, so a flood of distinct ones must
// not grow the map without bound.
func TestThrottleStaysBounded(t *testing.T) {
	th := newThrottle(2, time.Minute)
	now := time.Now()

	for i := range maxThrottleKey * 2 {
		th.fail("user"+strconv.Itoa(i), now)
		if th.len() > maxThrottleKey {
			t.Fatalf("throttle grew to %d keys, past the %d bound", th.len(), maxThrottleKey)
		}
	}
}

// The bound on concurrent verifications is what keeps a burst of wrong
// credentials from allocating 20MB each, all at once.
func TestVerificationSlotsAreBounded(t *testing.T) {
	var running, peak int64
	var mu sync.Mutex
	var wg sync.WaitGroup

	for range maxConcurrentVerifications * 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = withVerificationSlot(context.Background(), func() error {
				n := atomic.AddInt64(&running, 1)
				mu.Lock()
				if n > peak {
					peak = n
				}
				mu.Unlock()
				time.Sleep(2 * time.Millisecond)
				atomic.AddInt64(&running, -1)
				return nil
			})
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if peak > maxConcurrentVerifications {
		t.Errorf("%d verifications ran at once, past the %d bound", peak, maxConcurrentVerifications)
	}
	if peak < 2 {
		t.Errorf("peak concurrency %d suggests the work was serialised entirely", peak)
	}
}

// A caller who has gone away should not spend 17ms on an answer nobody reads.
func TestVerificationSlotHonoursCancellation(t *testing.T) {
	// Fill every slot.
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range maxConcurrentVerifications {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = withVerificationSlot(context.Background(), func() error {
				<-release
				return nil
			})
		}()
	}

	// Give the fillers a moment to take their slots.
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var ran bool
	err := withVerificationSlot(ctx, func() error {
		ran = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ran {
		t.Error("the work ran for a cancelled caller")
	}

	close(release)
	wg.Wait()
}

func TestBasicAuthThrottlesRepeatedFailures(t *testing.T) {
	_, h := davHarness(t)

	for i := range maxFailures {
		if code := request(t, h, "alice", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, code)
		}
	}

	code := request(t, h, "alice", "wrong")
	if code != http.StatusTooManyRequests {
		t.Fatalf("attempt past the limit = %d, want 429", code)
	}
	// Even the right password waits: the limit is on the name, not on whether
	// this particular guess happens to be correct.
	if code := request(t, h, "alice", "hunter2hunter2"); code != http.StatusTooManyRequests {
		t.Errorf("a correct password during a throttle = %d, want 429", code)
	}
}

// An unknown name throttles exactly like a known one, so the response does not
// report which accounts exist.
func TestThrottlingDoesNotRevealWhichAccountsExist(t *testing.T) {
	_, h := davHarness(t)

	for range maxFailures {
		request(t, h, "mallory", "guess")
	}
	if code := request(t, h, "mallory", "guess"); code != http.StatusTooManyRequests {
		t.Errorf("an unknown name past the limit = %d, want 429 like a known one", code)
	}
}

func TestThrottleSendsRetryAfter(t *testing.T) {
	db := testDB(t)
	testUser(t, db, "alice", "hunter2hunter2")

	h := RequireBasicAuth(db, "edav")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for range maxFailures {
		request(t, h, "alice", "wrong")
	}

	req := newBasicRequest("alice", "wrong")
	rec := recordResponse(h, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}

	retry := rec.Header().Get("Retry-After")
	if retry == "" {
		t.Fatal("no Retry-After header")
	}
	seconds, err := strconv.Atoi(retry)
	if err != nil || seconds < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", retry)
	}
}

// A successful sync must not be throttled by an earlier typo.
func TestSuccessClearsTheThrottle(t *testing.T) {
	_, h := davHarness(t)

	for range maxFailures - 1 {
		request(t, h, "alice", "wrong")
	}
	if code := request(t, h, "alice", "hunter2hunter2"); code != http.StatusOK {
		t.Fatalf("a correct password below the limit = %d, want 200", code)
	}

	for i := range maxFailures - 1 {
		if code := request(t, h, "alice", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d after a success = %d, want 401", i+1, code)
		}
	}
}
