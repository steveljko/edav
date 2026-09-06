package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

const vcardRaw = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"UID:abc-123\r\n" +
	"FN:Ada Lovelace\r\n" +
	"X-ABLabel:my label\r\n" +
	"X-APPLE-STRUCTURED-LOCATION;VALUE=URI:geo:1,2\r\n" +
	"END:VCARD\r\n"

func mustCreateCollection(t *testing.T, db *sql.DB, ownerID int64, uri string) *Collection {
	t.Helper()
	c, err := CreateCollection(context.Background(), db, &Collection{
		OwnerID:     ownerID,
		Type:        CollectionAddressBook,
		URI:         uri,
		DisplayName: uri,
	})
	if err != nil {
		t.Fatalf("CreateCollection(%q) = %v", uri, err)
	}
	return c
}

func fixture(t *testing.T) (*sql.DB, *Collection) {
	t.Helper()
	db := testDB(t)
	u := mustCreateUser(t, db, "alice")
	return db, mustCreateCollection(t, db, u.ID, "contacts")
}

func TestPutObjectStoresBytesVerbatim(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	raw := []byte(vcardRaw)
	if _, err := PutObject(ctx, db, &Object{
		CollectionID: c.ID,
		URI:          "ada.vcf",
		Raw:          raw,
		UID:          "abc-123",
	}); err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	got, err := ObjectByURI(ctx, db, c.ID, "ada.vcf")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if !bytes.Equal(got.Raw, raw) {
		t.Errorf("stored bytes differ from input:\n in: %q\nout: %q", raw, got.Raw)
	}
	if got.ETag != ETagFor(raw) {
		t.Errorf("ETag = %q, want %q", got.ETag, ETagFor(raw))
	}
}

func TestETagChangesOnlyWithBytes(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	first, err := PutObject(ctx, db, &Object{CollectionID: c.ID, URI: "ada.vcf", Raw: []byte(vcardRaw)})
	if err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	same, err := PutObject(ctx, db, &Object{CollectionID: c.ID, URI: "ada.vcf", Raw: []byte(vcardRaw)})
	if err != nil {
		t.Fatalf("PutObject() rewrite = %v", err)
	}
	if same.ETag != first.ETag {
		t.Errorf("ETag changed on an identical rewrite: %q then %q", first.ETag, same.ETag)
	}

	changed, err := PutObject(ctx, db, &Object{
		CollectionID: c.ID, URI: "ada.vcf",
		Raw: []byte(vcardRaw + "NOTE:hello\r\n"),
	})
	if err != nil {
		t.Fatalf("PutObject() modified = %v", err)
	}
	if changed.ETag == first.ETag {
		t.Error("ETag did not change when the bytes did")
	}
}

func TestCTagBumpsOnMemberChangeOnly(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	reload := func() *Collection {
		t.Helper()
		got, err := CollectionByID(ctx, db, c.ID)
		if err != nil {
			t.Fatalf("CollectionByID() = %v", err)
		}
		return got
	}

	initial := reload()

	if _, err := PutObject(ctx, db, &Object{CollectionID: c.ID, URI: "ada.vcf", Raw: []byte(vcardRaw)}); err != nil {
		t.Fatalf("PutObject() = %v", err)
	}
	afterCreate := reload()
	if afterCreate.CTag == initial.CTag {
		t.Error("ctag did not change when an object was added")
	}

	if _, err := PutObject(ctx, db, &Object{CollectionID: c.ID, URI: "ada.vcf", Raw: []byte(vcardRaw)}); err != nil {
		t.Fatalf("PutObject() rewrite = %v", err)
	}
	if got := reload(); got.CTag != afterCreate.CTag {
		t.Errorf("ctag changed on an identical rewrite: %q then %q", afterCreate.CTag, got.CTag)
	}

	if err := DeleteObject(ctx, db, c.ID, "ada.vcf"); err != nil {
		t.Fatalf("DeleteObject() = %v", err)
	}
	if got := reload(); got.CTag == afterCreate.CTag {
		t.Error("ctag did not change when an object was deleted")
	}
}

func TestUpdateCollectionLeavesCTagAlone(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	c.DisplayName = "Renamed"
	c.Color = "#ff0000"
	if err := UpdateCollection(ctx, db, c); err != nil {
		t.Fatalf("UpdateCollection() = %v", err)
	}

	got, err := CollectionByID(ctx, db, c.ID)
	if err != nil {
		t.Fatalf("CollectionByID() = %v", err)
	}
	if got.CTag != c.CTag {
		t.Errorf("ctag changed on a property edit: %q then %q", c.CTag, got.CTag)
	}
	if got.DisplayName != "Renamed" || got.Color != "#ff0000" {
		t.Errorf("UpdateCollection() = %+v", got)
	}
}

func TestChangeLogRecordsEveryTransition(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	put := func(uri, raw string) {
		t.Helper()
		if _, err := PutObject(ctx, db, &Object{CollectionID: c.ID, URI: uri, Raw: []byte(raw)}); err != nil {
			t.Fatalf("PutObject(%q) = %v", uri, err)
		}
	}

	put("a.vcf", vcardRaw)
	put("b.vcf", vcardRaw+"NOTE:b\r\n")
	put("a.vcf", vcardRaw+"NOTE:edited\r\n")
	if err := DeleteObject(ctx, db, c.ID, "b.vcf"); err != nil {
		t.Fatalf("DeleteObject() = %v", err)
	}

	rows, err := db.Query(
		`SELECT object_uri, change_type, seq FROM object_changes WHERE collection_id = ? ORDER BY seq`, c.ID)
	if err != nil {
		t.Fatalf("query changes: %v", err)
	}
	defer rows.Close()

	type change struct {
		uri, kind string
		seq       int64
	}
	var got []change
	for rows.Next() {
		var ch change
		if err := rows.Scan(&ch.uri, &ch.kind, &ch.seq); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, ch)
	}

	// One row per URI, carrying its latest transition.
	want := []change{{"a.vcf", "updated", 3}, {"b.vcf", "deleted", 4}}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("changes[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSyncSeqIsStrictlyIncreasing(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	var last int64
	for i, uri := range []string{"a.vcf", "b.vcf", "c.vcf", "d.vcf"} {
		if _, err := PutObject(ctx, db, &Object{
			CollectionID: c.ID, URI: uri, Raw: []byte(vcardRaw + "NOTE:" + uri + "\r\n"),
		}); err != nil {
			t.Fatalf("PutObject(%q) = %v", uri, err)
		}
		got, err := CollectionByID(ctx, db, c.ID)
		if err != nil {
			t.Fatalf("CollectionByID() = %v", err)
		}
		if got.SyncSeq <= last {
			t.Fatalf("write %d: sync_seq = %d, want > %d", i, got.SyncSeq, last)
		}
		if got.CTag != ctagFor(got.SyncSeq) {
			t.Errorf("ctag = %q, want %q", got.CTag, ctagFor(got.SyncSeq))
		}
		last = got.SyncSeq
	}
}

func TestPutObjectIndexColumns(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if _, err := PutObject(ctx, db, &Object{
		CollectionID:  c.ID,
		URI:           "event.ics",
		Raw:           []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"),
		UID:           "evt-1",
		ComponentType: "VEVENT",
		StartAt:       &start,
		EndAt:         &end,
		Recurring:     true,
	}); err != nil {
		t.Fatalf("PutObject() = %v", err)
	}

	got, err := ObjectByURI(ctx, db, c.ID, "event.ics")
	if err != nil {
		t.Fatalf("ObjectByURI() = %v", err)
	}
	if got.UID != "evt-1" || got.ComponentType != "VEVENT" || !got.Recurring {
		t.Errorf("index columns = %+v", got)
	}
	if got.StartAt == nil || !got.StartAt.Equal(start) {
		t.Errorf("StartAt = %v, want %v", got.StartAt, start)
	}
	if got.EndAt == nil || !got.EndAt.Equal(end) {
		t.Errorf("EndAt = %v, want %v", got.EndAt, end)
	}
}

func TestListAndDeleteObjects(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	for _, uri := range []string{"c.vcf", "a.vcf", "b.vcf"} {
		if _, err := PutObject(ctx, db, &Object{
			CollectionID: c.ID, URI: uri, Raw: []byte(vcardRaw + "NOTE:" + uri + "\r\n"),
		}); err != nil {
			t.Fatalf("PutObject(%q) = %v", uri, err)
		}
	}

	objects, err := ListObjects(ctx, db, c.ID)
	if err != nil {
		t.Fatalf("ListObjects() = %v", err)
	}
	want := []string{"a.vcf", "b.vcf", "c.vcf"}
	if len(objects) != len(want) {
		t.Fatalf("ListObjects() returned %d, want %d", len(objects), len(want))
	}
	for i, o := range objects {
		if o.URI != want[i] {
			t.Errorf("ListObjects()[%d] = %q, want %q", i, o.URI, want[i])
		}
	}

	if err := DeleteObject(ctx, db, c.ID, "a.vcf"); err != nil {
		t.Fatalf("DeleteObject() = %v", err)
	}
	if _, err := ObjectByURI(ctx, db, c.ID, "a.vcf"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ObjectByURI() after delete = %v, want ErrNotFound", err)
	}
	if err := DeleteObject(ctx, db, c.ID, "a.vcf"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteObject() twice = %v, want ErrNotFound", err)
	}
}

func TestObjectsCascadeOnCollectionDelete(t *testing.T) {
	ctx := context.Background()
	db, c := fixture(t)

	if _, err := PutObject(ctx, db, &Object{CollectionID: c.ID, URI: "ada.vcf", Raw: []byte(vcardRaw)}); err != nil {
		t.Fatalf("PutObject() = %v", err)
	}
	if err := DeleteCollection(ctx, db, c.ID); err != nil {
		t.Fatalf("DeleteCollection() = %v", err)
	}

	for _, table := range []string{"objects", "object_changes"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s = %d rows after collection delete, want 0", table, n)
		}
	}
}
