package dav

import (
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/steveljko/edav/internal/dav/internal"
	"github.com/steveljko/edav/internal/storage"
)

// syncTokenPrefix namespaces the token so a value from another server, or from
// an older token format, is rejected rather than silently misread as a
// position in this collection's sequence.
const syncTokenPrefix = "urn:edav:sync:"

// errInvalidSyncToken marks a token this server did not issue, or one pointing
// past the collection's current sequence. RFC 6578 3.2 wants a
// DAV:valid-sync-token precondition failure, which tells the client to discard
// its state and synchronise from scratch.
type errInvalidSyncToken struct{ token string }

func (e *errInvalidSyncToken) Error() string {
	return fmt.Sprintf("dav: unusable sync token %q", e.token)
}

func encodeSyncToken(seq int64) string {
	return syncTokenPrefix + strconv.FormatInt(seq, 10)
}

// decodeSyncToken reads a token back into a sequence number. An empty token is
// an initial synchronisation and starts at zero.
func decodeSyncToken(token string, current int64) (int64, error) {
	if token == "" {
		return 0, nil
	}

	digits, ok := strings.CutPrefix(token, syncTokenPrefix)
	if !ok {
		return 0, &errInvalidSyncToken{token: token}
	}
	seq, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || seq < 0 {
		return 0, &errInvalidSyncToken{token: token}
	}
	// A token ahead of the collection means it came from a different database,
	// or the collection was rebuilt underneath the client.
	if seq > current {
		return 0, &errInvalidSyncToken{token: token}
	}
	return seq, nil
}

// syncChanges splits a collection's changes since a token into the members to
// fetch and the paths to drop. A change is reported as a deletion when the
// object is no longer present, which also covers an object created and removed
// again between two synchronisations.
type syncChanges struct {
	Updated []*storage.Object
	Deleted []string
	Token   string
}

// collectSync resolves a token against a collection and gathers what changed.
func collectSync(ctx context.Context, db *sql.DB, c *storage.Collection, token string, limit int) (*syncChanges, error) {
	since, err := decodeSyncToken(token, c.SyncSeq)
	if err != nil {
		return nil, err
	}

	changes, current, err := storage.ChangesSince(ctx, db, c.ID, since)
	if err != nil {
		return nil, err
	}

	out := &syncChanges{Token: encodeSyncToken(current)}
	for _, ch := range changes {
		if limit > 0 && len(out.Updated)+len(out.Deleted) >= limit {
			break
		}

		obj, err := storage.ObjectByURI(ctx, db, c.ID, ch.URI)
		if errors.Is(err, storage.ErrNotFound) {
			out.Deleted = append(out.Deleted, ch.URI)
			continue
		}
		if err != nil {
			return nil, err
		}
		out.Updated = append(out.Updated, obj)
	}
	return out, nil
}

// syncTokenError renders an unusable token as the precondition failure RFC 6578
// defines, so the client knows to start over rather than treating it as a
// transient error and retrying the same token forever.
func syncTokenError(err error) error {
	var invalid *errInvalidSyncToken
	if !errors.As(err, &invalid) {
		return err
	}
	return &internal.HTTPError{
		Code: http.StatusForbidden,
		Err: &internal.Error{
			Raw: []internal.RawXMLValue{
				*internal.NewRawXMLElement(xml.Name{Space: internal.Namespace, Local: "valid-sync-token"}, nil, nil),
			},
		},
	}
}
