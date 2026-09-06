package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

func put(t *testing.T, db *sql.DB, collectionID int64, uri, body string) {
	t.Helper()
	if _, err := PutObject(context.Background(), db, &Object{
		CollectionID: collectionID, URI: uri, Raw: []byte(body),
	}); err != nil {
		t.Fatalf("PutObject(%q) = %v", uri, err)
	}
}

func changeMap(t *testing.T, changes []Change) map[string]ChangeType {
	t.Helper()
	out := make(map[string]ChangeType, len(changes))
	for _, ch := range changes {
		out[ch.URI] = ch.Type
	}
	return out
}

func TestChangesSinceInitialSyncSeesEverything(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	put(t, db, c.ID, "a.vcf", vcardRaw+"NOTE:a\r\n")
	put(t, db, c.ID, "b.vcf", vcardRaw+"NOTE:b\r\n")

	changes, token, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if token != 2 {
		t.Errorf("token = %d, want 2", token)
	}

	got := changeMap(t, changes)
	if len(got) != 2 || got["a.vcf"] != ChangeCreated || got["b.vcf"] != ChangeCreated {
		t.Errorf("changes = %v, want both created", got)
	}
}

func TestChangesSinceReportsOnlyWhatFollowsTheToken(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	put(t, db, c.ID, "a.vcf", vcardRaw+"NOTE:a\r\n")
	_, token, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}

	put(t, db, c.ID, "b.vcf", vcardRaw+"NOTE:b\r\n")

	changes, newToken, err := ChangesSince(ctx, db, c.ID, token)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if newToken <= token {
		t.Errorf("token did not advance: %d then %d", token, newToken)
	}
	if len(changes) != 1 || changes[0].URI != "b.vcf" {
		t.Errorf("changes = %+v, want only b.vcf", changes)
	}
}

// The case that loses data if it is wrong: a client synced before a deletion
// must be told about the deletion, or it keeps an object the server dropped.
func TestChangesSinceReportsDeletionsAfterTheToken(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	put(t, db, c.ID, "a.vcf", vcardRaw+"NOTE:a\r\n")
	put(t, db, c.ID, "b.vcf", vcardRaw+"NOTE:b\r\n")

	_, token, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}

	if err := DeleteObject(ctx, db, c.ID, "a.vcf"); err != nil {
		t.Fatalf("DeleteObject() = %v", err)
	}

	changes, _, err := ChangesSince(ctx, db, c.ID, token)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if len(changes) != 1 || changes[0].URI != "a.vcf" || changes[0].Type != ChangeDeleted {
		t.Errorf("changes = %+v, want a.vcf deleted", changes)
	}
}

// An initial sync taken after a deletion must not mention the deleted object at
// all: the client never had it.
func TestChangesSinceInitialSyncStillListsPastDeletions(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	put(t, db, c.ID, "a.vcf", vcardRaw+"NOTE:a\r\n")
	if err := DeleteObject(ctx, db, c.ID, "a.vcf"); err != nil {
		t.Fatalf("DeleteObject() = %v", err)
	}

	changes, _, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if len(changes) != 1 || changes[0].Type != ChangeDeleted {
		t.Fatalf("changes = %+v, want a single deletion", changes)
	}
}

func TestChangesSinceCollapsesRepeatedEditsOfOneObject(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	_, token, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}

	for i := 0; i < 4; i++ {
		put(t, db, c.ID, "a.vcf", fmt.Sprintf("%sNOTE:edit %d\r\n", vcardRaw, i))
	}

	changes, _, err := ChangesSince(ctx, db, c.ID, token)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if len(changes) != 1 || changes[0].URI != "a.vcf" {
		t.Errorf("changes = %+v, want one entry for a.vcf", changes)
	}
}

func TestChangesSinceIgnoresRewritesThatChangeNothing(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)
	put(t, db, c.ID, "a.vcf", vcardRaw)

	_, token, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}

	put(t, db, c.ID, "a.vcf", vcardRaw)

	changes, newToken, err := ChangesSince(ctx, db, c.ID, token)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %+v, want none for an identical rewrite", changes)
	}
	if newToken != token {
		t.Errorf("token = %d, want it unchanged at %d", newToken, token)
	}
}

func TestChangesSinceIsScopedToOneCollection(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)
	other := mustCreateCollection(t, db, c.OwnerID, "other")

	put(t, db, c.ID, "a.vcf", vcardRaw+"NOTE:a\r\n")
	put(t, db, other.ID, "b.vcf", vcardRaw+"NOTE:b\r\n")

	changes, _, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if len(changes) != 1 || changes[0].URI != "a.vcf" {
		t.Errorf("changes = %+v, want only this collection's", changes)
	}
}

// Concurrent writers must each get their own sequence number. If two shared
// one, a client syncing between them would never be told about the second.
func TestConcurrentWritesDoNotShareASequence(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := PutObject(ctx, db, &Object{
				CollectionID: c.ID,
				URI:          fmt.Sprintf("obj-%02d.vcf", i),
				Raw:          []byte(fmt.Sprintf("%sNOTE:%d\r\n", vcardRaw, i)),
			}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent PutObject: %v", err)
	}

	changes, token, err := ChangesSince(ctx, db, c.ID, 0)
	if err != nil {
		t.Fatalf("ChangesSince() = %v", err)
	}
	if len(changes) != writers {
		t.Fatalf("changes = %d, want %d", len(changes), writers)
	}
	if token != int64(writers) {
		t.Errorf("token = %d, want %d", token, writers)
	}

	seen := make(map[int64]string, writers)
	for _, ch := range changes {
		if prev, dup := seen[ch.Seq]; dup {
			t.Errorf("sequence %d used by both %s and %s", ch.Seq, prev, ch.URI)
		}
		seen[ch.Seq] = ch.URI
	}
}

// Walking the log one token at a time must visit every change exactly once,
// which is what a client does across a run of syncs.
func TestSequentialSyncsSeeEveryChangeOnce(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	var token int64
	seen := make(map[string]int)

	for i := 0; i < 5; i++ {
		put(t, db, c.ID, fmt.Sprintf("obj-%d.vcf", i), fmt.Sprintf("%sNOTE:%d\r\n", vcardRaw, i))

		changes, next, err := ChangesSince(ctx, db, c.ID, token)
		if err != nil {
			t.Fatalf("ChangesSince() = %v", err)
		}
		if len(changes) != 1 {
			t.Fatalf("round %d: changes = %+v, want exactly one", i, changes)
		}
		seen[changes[0].URI]++
		token = next
	}

	if len(seen) != 5 {
		t.Errorf("saw %d distinct objects, want 5", len(seen))
	}
	for uri, n := range seen {
		if n != 1 {
			t.Errorf("%s reported %d times, want once", uri, n)
		}
	}
}
