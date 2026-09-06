// Package dav wires the storage layer to the CardDAV and CalDAV protocol
// handlers.
package dav

import (
	"fmt"
	"strings"
)

// ResourceKind identifies what a request path points at.
type ResourceKind int

const (
	KindUnknown ResourceKind = iota
	KindRoot
	KindPrincipal
	KindAddressBookHome
	KindAddressBook
	KindAddressObject
)

const (
	principalsSegment   = "principals"
	addressBooksSegment = "addressbooks"
)

// Resource is a parsed request path.
type Resource struct {
	Kind       ResourceKind
	User       string
	Collection string
	Object     string
}

// Paths maps between storage identifiers and request paths. Paths are handled
// decoded: the XML layer escapes them on the way out and unescapes on the way
// in.
type Paths struct {
	// Prefix is the DAV root, without a trailing slash, for example "/dav".
	Prefix string
}

func (p Paths) Root() string {
	return p.Prefix + "/"
}

func (p Paths) Principal(user string) string {
	return p.Prefix + "/" + principalsSegment + "/" + user + "/"
}

func (p Paths) AddressBookHome(user string) string {
	return p.Prefix + "/" + addressBooksSegment + "/" + user + "/"
}

func (p Paths) AddressBook(user, collection string) string {
	return p.AddressBookHome(user) + collection + "/"
}

func (p Paths) AddressObject(user, collection, object string) string {
	return p.AddressBook(user, collection) + object
}

// Parse resolves a request path against the prefix. Trailing slashes are not
// significant: clients are inconsistent about sending them, and a collection
// requested without one is still that collection.
func (p Paths) Parse(path string) (Resource, error) {
	rest, ok := strings.CutPrefix(path, p.Prefix)
	if !ok {
		return Resource{}, fmt.Errorf("dav: path %q is outside %q", path, p.Prefix)
	}

	segments := splitPath(rest)
	if len(segments) == 0 {
		return Resource{Kind: KindRoot}, nil
	}

	switch segments[0] {
	case principalsSegment:
		if len(segments) == 2 {
			return Resource{Kind: KindPrincipal, User: segments[1]}, nil
		}
	case addressBooksSegment:
		switch len(segments) {
		case 2:
			return Resource{Kind: KindAddressBookHome, User: segments[1]}, nil
		case 3:
			return Resource{Kind: KindAddressBook, User: segments[1], Collection: segments[2]}, nil
		case 4:
			return Resource{
				Kind:       KindAddressObject,
				User:       segments[1],
				Collection: segments[2],
				Object:     segments[3],
			}, nil
		}
	}
	return Resource{}, fmt.Errorf("dav: cannot resolve path %q", path)
}

func splitPath(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
