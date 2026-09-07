package storage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func seedContacts(t *testing.T, db *sql.DB, collectionID int64, names ...string) {
	t.Helper()
	for i, name := range names {
		if _, err := PutObject(context.Background(), db, &Object{
			CollectionID: collectionID,
			URI:          fmt.Sprintf("c%02d.vcf", i),
			Raw:          []byte(fmt.Sprintf("%sNOTE:%d\r\n", vcardRaw, i)),
			DisplayName:  name,
		}); err != nil {
			t.Fatalf("PutObject(%q) = %v", name, err)
		}
	}
}

func TestSearchObjectsPaginates(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	names := make([]string, 0, 25)
	for i := range 25 {
		names = append(names, fmt.Sprintf("Person %02d", i))
	}
	seedContacts(t, db, c.ID, names...)

	first, total, err := SearchObjects(ctx, db, c.ID, "", 10, 0)
	if err != nil {
		t.Fatalf("SearchObjects() = %v", err)
	}
	if total != 25 {
		t.Errorf("total = %d, want 25", total)
	}
	if len(first) != 10 {
		t.Fatalf("page = %d objects, want 10", len(first))
	}

	last, _, err := SearchObjects(ctx, db, c.ID, "", 10, 20)
	if err != nil {
		t.Fatalf("SearchObjects() = %v", err)
	}
	if len(last) != 5 {
		t.Errorf("last page = %d objects, want 5", len(last))
	}
	if first[0].DisplayName == last[0].DisplayName {
		t.Error("paging returned the same rows")
	}
}

func TestSearchObjectsMatchesNameAndURI(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)
	seedContacts(t, db, c.ID, "Ada Lovelace", "Grace Hopper", "Alan Turing")

	tests := []struct {
		query string
		want  int
	}{
		{"", 3},
		{"ada", 1},
		{"ADA", 1},
		{"lovelace", 1},
		{"c00", 1},
		{"nobody", 0},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got, total, err := SearchObjects(ctx, db, c.ID, tt.query, 50, 0)
			if err != nil {
				t.Fatalf("SearchObjects() = %v", err)
			}
			if total != tt.want || len(got) != tt.want {
				t.Errorf("matched %d (total %d), want %d", len(got), total, tt.want)
			}
		})
	}
}

// A percent sign in a search box is a character to look for, not a wildcard
// that quietly matches the whole collection.
func TestSearchObjectsTreatsWildcardsLiterally(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)
	seedContacts(t, db, c.ID, "Ada Lovelace", "100% Cotton", "under_score")

	tests := []struct {
		query string
		want  int
	}{
		{"%", 1},
		{"100%", 1},
		{"_", 1},
		{"under_score", 1},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			_, total, err := SearchObjects(ctx, db, c.ID, tt.query, 50, 0)
			if err != nil {
				t.Fatalf("SearchObjects() = %v", err)
			}
			if total != tt.want {
				t.Errorf("matched %d, want %d", total, tt.want)
			}
		})
	}
}

func TestSearchObjectsIsScopedToOneCollection(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)
	other := mustCreateCollection(t, db, c.OwnerID, "other")

	seedContacts(t, db, c.ID, "Ada Lovelace")
	seedContacts(t, db, other.ID, "Ada Lovelace")

	_, total, err := SearchObjects(ctx, db, c.ID, "ada", 50, 0)
	if err != nil {
		t.Fatalf("SearchObjects() = %v", err)
	}
	if total != 1 {
		t.Errorf("matched %d, want 1 from this collection only", total)
	}
}
