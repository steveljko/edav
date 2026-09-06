# Forked WebDAV/CardDAV protocol layer

`webdav/`, `internal/`, `carddav/` and `caldav/` are forked from
[github.com/emersion/go-webdav](https://github.com/emersion/go-webdav) v0.7.0
(MIT, see `LICENSE`).

## Why

Upstream's CardDAV and CalDAV handlers cannot preserve a client's bytes, which
this project requires. Taking CardDAV as the example:

- `PUT` decodes the request body into a `vcard.Card` and passes only the parsed
  card to the backend. The raw bytes are consumed and discarded.
- `GET` and the `address-data` property in PROPFIND and multiget REPORT
  re-serialize from that parsed card.

`go-vcard`'s encoder alphabetizes properties, upper-cases property names
(`X-ABLabel` becomes `X-ABLABEL`, which Apple clients match case-sensitively)
and re-escapes commas in values (`geo:1,2` becomes `geo:1\,2`, once more on each
round trip). A card would therefore come back different from how it went in, and
its ETag would change on every read.

There is no hook to avoid this: `AddressObject` carries only a `vcard.Card`
(`CalendarObject` only an `*ical.Calendar`), and the XML machinery lives in
`go-webdav/internal`, which an external module cannot import. Forking was the
only way to keep the raw bytes.

## Changes from upstream

- Import paths rewritten to this module.
- `client.go` and `fs_local.go` dropped; this is a server.
- `TestAddressBookDiscovery` dropped with the client it exercised.
- Struct literals keyed and a `%q` verb applied to a `*Status` instead of the
  raw bytes corrected, so `go vet ./...` passes across the repository.
- The `carddav.Backend` and `caldav.Backend` contracts carry raw bytes instead
  of `vcard.Card` and `*ical.Calendar`; see the commits that introduce them.
- Resource classification is delegated to the backend through a
  `ResourceTypeResolver`, because upstream infers it from the number of path
  segments and so cannot tell a principal from a home set at the same depth.

## Updating

There is no automatic path back to upstream. To pull in a fix, diff the relevant
file against the corresponding upstream tag and apply it by hand.
