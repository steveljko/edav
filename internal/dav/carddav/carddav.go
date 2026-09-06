// Package carddav provides a client and server CardDAV implementation.
//
// CardDAV is defined in RFC 6352.
package carddav

import (
	"time"

	"github.com/emersion/go-vcard"
	"github.com/steveljko/edav/internal/dav/internal"
	"github.com/steveljko/edav/internal/dav/webdav"
)

var CapabilityAddressBook = webdav.Capability("addressbook")

func NewAddressBookHomeSet(path string) webdav.BackendSuppliedHomeSet {
	return &addressbookHomeSet{Href: internal.Href{Path: path}}
}

type AddressDataType struct {
	ContentType string
	Version     string
}

type AddressBook struct {
	Path                 string
	Name                 string
	Description          string
	MaxResourceSize      int64
	SupportedAddressData []AddressDataType
	// SyncToken is the collection's current position in its own change
	// sequence, reported so a client can start syncing from now.
	SyncToken string
}

func (ab *AddressBook) SupportsAddressData(contentType, version string) bool {
	if len(ab.SupportedAddressData) == 0 {
		return contentType == "text/vcard" && version == "3.0"
	}
	for _, t := range ab.SupportedAddressData {
		if t.ContentType == contentType && t.Version == version {
			return true
		}
	}
	return false
}

type AddressBookQuery struct {
	DataRequest AddressDataRequest

	PropFilters []PropFilter
	FilterTest  FilterTest // defaults to FilterAnyOf

	Limit int // <= 0 means unlimited
}

type AddressDataRequest struct {
	Props   []string
	AllProp bool
}

type PropFilter struct {
	Name string
	Test FilterTest // defaults to FilterAnyOf

	// if IsNotDefined is set, TextMatches and Params need to be unset
	IsNotDefined bool
	TextMatches  []TextMatch
	Params       []ParamFilter
}

type ParamFilter struct {
	Name string

	// if IsNotDefined is set, TextMatch needs to be unset
	IsNotDefined bool
	TextMatch    *TextMatch
}

type TextMatch struct {
	Text            string
	NegateCondition bool
	MatchType       MatchType // defaults to MatchContains
}

type FilterTest string

const (
	FilterAnyOf FilterTest = "anyof"
	FilterAllOf FilterTest = "allof"
)

type MatchType string

const (
	MatchEquals     MatchType = "equals"
	MatchContains   MatchType = "contains"
	MatchStartsWith MatchType = "starts-with"
	MatchEndsWith   MatchType = "ends-with"
)

type AddressBookMultiGet struct {
	Paths       []string
	DataRequest AddressDataRequest
}

type AddressObject struct {
	Path          string
	ModTime       time.Time
	ContentLength int64
	ETag          string

	// Raw is the vCard exactly as the client stored it, and is what GET and an
	// unrestricted address-data response return. Backends must set it.
	Raw []byte

	// Card is a parsed view of Raw, needed only to evaluate filters and
	// property restrictions. Backends may leave it nil when the request needs
	// neither. A response built from a reduced Card carries no Raw, since it no
	// longer represents the stored bytes.
	Card vcard.Card
}

// MaxResourceSize caps the size of a single vCard accepted by PUT. RFC 6352
// leaves the limit to the server; this is generous for a contact card while
// keeping a malicious client from streaming an unbounded body into memory.
const MaxResourceSize = 1 << 20

// SyncQuery is the query struct represents a sync-collection request
type SyncQuery struct {
	DataRequest AddressDataRequest
	SyncToken   string
	Limit       int // <= 0 means unlimited
}

// SyncResponse contains the returned sync-token for next time
type SyncResponse struct {
	SyncToken string
	Updated   []AddressObject
	Deleted   []string
}
