package storage

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCollectionValidatesInput(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")

	tests := []struct {
		name string
		c    Collection
	}{
		{"unknown type", Collection{OwnerID: u.ID, Type: "notes", URI: "x"}},
		{"empty type", Collection{OwnerID: u.ID, URI: "x"}},
		{"empty uri", Collection{OwnerID: u.ID, Type: CollectionCalendar}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CreateCollection(ctx, db, &tt.c); err == nil {
				t.Error("CreateCollection() = nil, want an error")
			}
		})
	}
}

func TestCreateCollectionRejectsDuplicateURIPerOwner(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	alice := mustCreateUser(t, db, "alice")
	bob := mustCreateUser(t, db, "bob")

	mustCreateCollection(t, db, alice.ID, "contacts")

	if _, err := CreateCollection(ctx, db, &Collection{
		OwnerID: alice.ID, Type: CollectionCalendar, URI: "contacts",
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("CreateCollection() duplicate uri = %v, want ErrConflict", err)
	}

	// The same slug under a different owner is a different collection.
	if _, err := CreateCollection(ctx, db, &Collection{
		OwnerID: bob.ID, Type: CollectionAddressBook, URI: "contacts",
	}); err != nil {
		t.Errorf("CreateCollection() for another owner = %v, want nil", err)
	}
}

func TestCollectionLookup(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	alice := mustCreateUser(t, db, "alice")
	bob := mustCreateUser(t, db, "bob")
	c := mustCreateCollection(t, db, alice.ID, "contacts")

	got, err := CollectionByURI(ctx, db, alice.ID, "contacts")
	if err != nil {
		t.Fatalf("CollectionByURI() = %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("CollectionByURI() id = %d, want %d", got.ID, c.ID)
	}

	// Another user's slug must not resolve.
	if _, err := CollectionByURI(ctx, db, bob.ID, "contacts"); !errors.Is(err, ErrNotFound) {
		t.Errorf("CollectionByURI() for a non-owner = %v, want ErrNotFound", err)
	}
	if _, err := CollectionByID(ctx, db, 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("CollectionByID() = %v, want ErrNotFound", err)
	}
}

func TestListCollectionsFiltersByType(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")

	for _, spec := range []struct {
		uri string
		typ CollectionType
	}{
		{"work", CollectionCalendar},
		{"home", CollectionCalendar},
		{"contacts", CollectionAddressBook},
	} {
		if _, err := CreateCollection(ctx, db, &Collection{
			OwnerID: u.ID, Type: spec.typ, URI: spec.uri,
		}); err != nil {
			t.Fatalf("CreateCollection(%q) = %v", spec.uri, err)
		}
	}

	tests := []struct {
		typ  CollectionType
		want []string
	}{
		{"", []string{"contacts", "home", "work"}},
		{CollectionCalendar, []string{"home", "work"}},
		{CollectionAddressBook, []string{"contacts"}},
	}

	for _, tt := range tests {
		got, err := ListCollections(ctx, db, u.ID, tt.typ)
		if err != nil {
			t.Fatalf("ListCollections(%q) = %v", tt.typ, err)
		}
		if len(got) != len(tt.want) {
			t.Fatalf("ListCollections(%q) returned %d, want %d", tt.typ, len(got), len(tt.want))
		}
		for i, c := range got {
			if c.URI != tt.want[i] {
				t.Errorf("ListCollections(%q)[%d] = %q, want %q", tt.typ, i, c.URI, tt.want[i])
			}
		}
	}
}

func TestDeleteCollection(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	c := mustCreateCollection(t, db, u.ID, "contacts")

	if err := DeleteCollection(ctx, db, c.ID); err != nil {
		t.Fatalf("DeleteCollection() = %v", err)
	}
	if err := DeleteCollection(ctx, db, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteCollection() twice = %v, want ErrNotFound", err)
	}
}

func TestCollectionsCascadeOnUserDelete(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	mustCreateCollection(t, db, u.ID, "contacts")

	if err := DeleteUser(ctx, db, u.ID); err != nil {
		t.Fatalf("DeleteUser() = %v", err)
	}

	got, err := ListCollections(ctx, db, u.ID, "")
	if err != nil {
		t.Fatalf("ListCollections() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("collections = %d after owner delete, want 0", len(got))
	}
}
