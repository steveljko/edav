package dav

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/emersion/go-vcard"

	"github.com/steveljko/edav/internal/auth"
	"github.com/steveljko/edav/internal/dav/carddav"
	"github.com/steveljko/edav/internal/dav/internal"
	"github.com/steveljko/edav/internal/storage"
)

// CardDAVBackend implements carddav.Backend on top of the SQLite storage layer.
// Every method resolves the caller from the request context, so a user can only
// reach their own collections.
type CardDAVBackend struct {
	DB    *sql.DB
	Paths Paths
}

var _ carddav.Backend = (*CardDAVBackend)(nil)

func (b *CardDAVBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return "", err
	}
	return b.Paths.Principal(u.Username), nil
}

func (b *CardDAVBackend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return "", err
	}
	return b.Paths.AddressBookHome(u.Username), nil
}

func (b *CardDAVBackend) ListAddressBooks(ctx context.Context) ([]carddav.AddressBook, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return nil, err
	}

	collections, err := storage.ListCollections(ctx, b.DB, u.ID, storage.CollectionAddressBook)
	if err != nil {
		return nil, err
	}

	books := make([]carddav.AddressBook, 0, len(collections))
	for _, c := range collections {
		books = append(books, b.addressBook(u.Username, c))
	}
	return books, nil
}

func (b *CardDAVBackend) GetAddressBook(ctx context.Context, path string) (*carddav.AddressBook, error) {
	u, c, err := b.resolveAddressBook(ctx, path)
	if err != nil {
		return nil, err
	}
	book := b.addressBook(u.Username, c)
	return &book, nil
}

func (b *CardDAVBackend) CreateAddressBook(ctx context.Context, ab *carddav.AddressBook) error {
	u, res, err := b.resolve(ctx, ab.Path)
	if err != nil {
		return err
	}
	if res.Kind != KindAddressBook {
		return internal.HTTPErrorf(http.StatusForbidden, "carddav: %q is not an address book path", ab.Path)
	}

	_, err = storage.CreateCollection(ctx, b.DB, &storage.Collection{
		OwnerID:     u.ID,
		Type:        storage.CollectionAddressBook,
		URI:         res.Collection,
		DisplayName: ab.Name,
		Description: ab.Description,
	})
	if errors.Is(err, storage.ErrConflict) {
		return internal.HTTPErrorf(http.StatusMethodNotAllowed, "carddav: %q already exists", ab.Path)
	}
	return err
}

func (b *CardDAVBackend) DeleteAddressBook(ctx context.Context, path string) error {
	_, c, err := b.resolveAddressBook(ctx, path)
	if err != nil {
		return err
	}
	return storage.DeleteCollection(ctx, b.DB, c.ID)
}

func (b *CardDAVBackend) GetAddressObject(ctx context.Context, path string, req *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if res.Kind != KindAddressObject {
		return nil, internal.HTTPErrorf(http.StatusNotFound, "carddav: %q is not an address object", path)
	}

	c, err := b.collection(ctx, u.ID, res.Collection)
	if err != nil {
		return nil, err
	}
	obj, err := storage.ObjectByURI(ctx, b.DB, c.ID, res.Object)
	if err != nil {
		return nil, notFoundIfMissing(err, path)
	}

	ao, err := b.addressObject(u.Username, c, obj, req, false)
	if err != nil {
		return nil, err
	}
	return &ao, nil
}

func (b *CardDAVBackend) ListAddressObjects(ctx context.Context, path string, req *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	return b.listAddressObjects(ctx, path, req, false)
}

func (b *CardDAVBackend) listAddressObjects(ctx context.Context, path string, req *carddav.AddressDataRequest, withCard bool) ([]carddav.AddressObject, error) {
	u, c, err := b.resolveAddressBook(ctx, path)
	if err != nil {
		return nil, err
	}

	objects, err := storage.ListObjects(ctx, b.DB, c.ID)
	if err != nil {
		return nil, err
	}

	aos := make([]carddav.AddressObject, 0, len(objects))
	for _, obj := range objects {
		ao, err := b.addressObject(u.Username, c, obj, req, withCard)
		if err != nil {
			return nil, err
		}
		aos = append(aos, ao)
	}
	return aos, nil
}

func (b *CardDAVBackend) QueryAddressObjects(ctx context.Context, path string, query *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	var req *carddav.AddressDataRequest
	if query != nil {
		req = &query.DataRequest
	}

	aos, err := b.listAddressObjects(ctx, path, req, true)
	if err != nil {
		return nil, err
	}
	return carddav.Filter(query, aos)
}

func (b *CardDAVBackend) PutAddressObject(ctx context.Context, path string, raw []byte, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if res.Kind != KindAddressObject {
		return nil, internal.HTTPErrorf(http.StatusForbidden, "carddav: %q is not an address object path", path)
	}

	c, err := b.collection(ctx, u.ID, res.Collection)
	if err != nil {
		return nil, err
	}

	existing, err := storage.ObjectByURI(ctx, b.DB, c.ID, res.Object)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if err := checkPreconditions(opts, existing); err != nil {
		return nil, err
	}

	uid, fn, err := cardIndex(raw)
	if err != nil {
		return nil, internal.HTTPErrorf(http.StatusBadRequest, "carddav: %v", err)
	}

	obj, err := storage.PutObject(ctx, b.DB, &storage.Object{
		CollectionID:  c.ID,
		URI:           res.Object,
		Raw:           raw,
		UID:           uid,
		ComponentType: "VCARD",
		DisplayName:   fn,
	})
	if err != nil {
		return nil, err
	}

	ao, err := b.addressObject(u.Username, c, obj, nil, false)
	if err != nil {
		return nil, err
	}
	return &ao, nil
}

func (b *CardDAVBackend) DeleteAddressObject(ctx context.Context, path string) error {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return err
	}
	if res.Kind != KindAddressObject {
		return internal.HTTPErrorf(http.StatusForbidden, "carddav: %q is not an address object path", path)
	}

	c, err := b.collection(ctx, u.ID, res.Collection)
	if err != nil {
		return err
	}
	return notFoundIfMissing(storage.DeleteObject(ctx, b.DB, c.ID, res.Object), path)
}

// ResourceTypeAtPath implements carddav.ResourceTypeResolver, so principals and
// address book home sets are told apart by their layout rather than by how many
// path segments they happen to have.
func (b *CardDAVBackend) ResourceTypeAtPath(reqPath string) (carddav.ResourceType, bool) {
	res, err := b.Paths.Parse(reqPath)
	if err != nil {
		return 0, false
	}

	switch res.Kind {
	case KindRoot:
		return carddav.ResourceTypeRoot, true
	case KindPrincipal:
		return carddav.ResourceTypeUserPrincipal, true
	case KindAddressBookHome:
		return carddav.ResourceTypeAddressBookHomeSet, true
	case KindAddressBook:
		return carddav.ResourceTypeAddressBook, true
	case KindAddressObject:
		return carddav.ResourceTypeAddressObject, true
	default:
		return 0, false
	}
}

// SyncCollection answers a sync-collection REPORT with everything that changed
// after the client's token, including the members that have since been removed.
func (b *CardDAVBackend) SyncCollection(ctx context.Context, path string, query *carddav.SyncQuery) (*carddav.SyncResponse, error) {
	u, c, err := b.resolveAddressBook(ctx, path)
	if err != nil {
		return nil, err
	}

	var token string
	var limit int
	var req *carddav.AddressDataRequest
	if query != nil {
		token, limit = query.SyncToken, query.Limit
		req = &query.DataRequest
	}

	changes, err := collectSync(ctx, b.DB, c, token, limit)
	if err != nil {
		return nil, syncTokenError(err)
	}

	resp := &carddav.SyncResponse{SyncToken: changes.Token}
	for _, obj := range changes.Updated {
		ao, err := b.addressObject(u.Username, c, obj, req, false)
		if err != nil {
			return nil, err
		}
		resp.Updated = append(resp.Updated, ao)
	}
	for _, uri := range changes.Deleted {
		resp.Deleted = append(resp.Deleted, b.Paths.AddressObject(u.Username, c.URI, uri))
	}
	return resp, nil
}

func (b *CardDAVBackend) addressBook(username string, c *storage.Collection) carddav.AddressBook {
	return carddav.AddressBook{
		Path:            b.Paths.AddressBook(username, c.URI),
		Name:            c.DisplayName,
		Description:     c.Description,
		MaxResourceSize: carddav.MaxResourceSize,
		SyncToken:       encodeSyncToken(c.SyncSeq),
	}
}

// addressObject builds the protocol view of a stored object. The parsed card is
// attached only when something needs it -- a property restriction, or a query
// whose filters are evaluated against it -- since the common full-body case can
// answer from the stored bytes without parsing at all.
func (b *CardDAVBackend) addressObject(username string, c *storage.Collection, obj *storage.Object, req *carddav.AddressDataRequest, withCard bool) (carddav.AddressObject, error) {
	ao := carddav.AddressObject{
		Path:          b.Paths.AddressObject(username, c.URI, obj.URI),
		ModTime:       obj.UpdatedAt,
		ContentLength: int64(len(obj.Raw)),
		ETag:          obj.ETag,
		Raw:           obj.Raw,
	}

	if withCard || (req != nil && (req.AllProp || len(req.Props) > 0)) {
		card, err := vcard.NewDecoder(bytes.NewReader(obj.Raw)).Decode()
		if err != nil {
			return carddav.AddressObject{}, fmt.Errorf("dav: parse stored card %q: %w", obj.URI, err)
		}
		ao.Card = card
	}
	return ao, nil
}

func (b *CardDAVBackend) resolve(ctx context.Context, path string) (*storage.User, Resource, error) {
	u, err := userFrom(ctx)
	if err != nil {
		return nil, Resource{}, err
	}

	res, err := b.Paths.Parse(path)
	if err != nil {
		return nil, Resource{}, internal.HTTPErrorf(http.StatusNotFound, "%v", err)
	}
	// Every principal owns exactly its own tree; anything else is another
	// user's data and must not be reachable.
	if res.User != "" && !strings.EqualFold(res.User, u.Username) {
		return nil, Resource{}, internal.HTTPErrorf(http.StatusForbidden, "carddav: %q belongs to another user", path)
	}
	return u, res, nil
}

func (b *CardDAVBackend) resolveAddressBook(ctx context.Context, path string) (*storage.User, *storage.Collection, error) {
	u, res, err := b.resolve(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	if res.Kind != KindAddressBook {
		return nil, nil, internal.HTTPErrorf(http.StatusNotFound, "carddav: %q is not an address book", path)
	}

	c, err := b.collection(ctx, u.ID, res.Collection)
	if err != nil {
		return nil, nil, err
	}
	return u, c, nil
}

func (b *CardDAVBackend) collection(ctx context.Context, ownerID int64, uri string) (*storage.Collection, error) {
	c, err := storage.CollectionByURI(ctx, b.DB, ownerID, uri)
	if err != nil {
		return nil, notFoundIfMissing(err, uri)
	}
	if c.Type != storage.CollectionAddressBook {
		return nil, internal.HTTPErrorf(http.StatusNotFound, "carddav: %q is not an address book", uri)
	}
	return c, nil
}

func checkPreconditions(opts *carddav.PutAddressObjectOptions, existing *storage.Object) error {
	if opts == nil {
		return nil
	}

	if opts.IfNoneMatch.IsSet() {
		if existing != nil {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "carddav: resource already exists")
		}
		return nil
	}

	if opts.IfMatch.IsSet() {
		if existing == nil {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "carddav: resource does not exist")
		}
		ok, err := opts.IfMatch.MatchETag(existing.ETag)
		if err != nil {
			return internal.HTTPErrorf(http.StatusBadRequest, "carddav: malformed If-Match: %v", err)
		}
		if !ok {
			return internal.HTTPErrorf(http.StatusPreconditionFailed, "carddav: ETag does not match")
		}
	}
	return nil
}

func userFrom(ctx context.Context) (*storage.User, error) {
	u, ok := auth.UserFrom(ctx)
	if !ok {
		return nil, internal.HTTPErrorf(http.StatusUnauthorized, "carddav: no authenticated user")
	}
	return u, nil
}

func notFoundIfMissing(err error, path string) error {
	if errors.Is(err, storage.ErrNotFound) {
		return internal.HTTPErrorf(http.StatusNotFound, "carddav: %q not found", path)
	}
	return err
}
