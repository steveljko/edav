package dav

import "testing"

var testPaths = Paths{Prefix: "/dav"}

func TestPathsBuild(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"root", testPaths.Root(), "/dav/"},
		{"principal", testPaths.Principal("alice"), "/dav/principals/alice/"},
		{"home set", testPaths.AddressBookHome("alice"), "/dav/addressbooks/alice/"},
		{"address book", testPaths.AddressBook("alice", "contacts"), "/dav/addressbooks/alice/contacts/"},
		{"object", testPaths.AddressObject("alice", "contacts", "ada.vcf"), "/dav/addressbooks/alice/contacts/ada.vcf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("= %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestPathsParse(t *testing.T) {
	tests := []struct {
		path string
		want Resource
	}{
		{"/dav", Resource{Kind: KindRoot}},
		{"/dav/", Resource{Kind: KindRoot}},
		{"/dav/principals/alice/", Resource{Kind: KindPrincipal, User: "alice"}},
		{"/dav/principals/alice", Resource{Kind: KindPrincipal, User: "alice"}},
		{"/dav/addressbooks/alice/", Resource{Kind: KindAddressBookHome, User: "alice"}},
		{"/dav/addressbooks/alice", Resource{Kind: KindAddressBookHome, User: "alice"}},
		{
			"/dav/addressbooks/alice/contacts/",
			Resource{Kind: KindAddressBook, User: "alice", Collection: "contacts"},
		},
		{
			"/dav/addressbooks/alice/contacts",
			Resource{Kind: KindAddressBook, User: "alice", Collection: "contacts"},
		},
		{
			"/dav/addressbooks/alice/contacts/ada.vcf",
			Resource{Kind: KindAddressObject, User: "alice", Collection: "contacts", Object: "ada.vcf"},
		},
		{
			// Apple clients use the UID as the filename, colons and all.
			"/dav/addressbooks/alice/contacts/urn:uuid:4fbe8971-0bc3.vcf",
			Resource{Kind: KindAddressObject, User: "alice", Collection: "contacts", Object: "urn:uuid:4fbe8971-0bc3.vcf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := testPaths.Parse(tt.path)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.path, got, tt.want)
			}
		})
	}
}

func TestPathsParseRejects(t *testing.T) {
	for _, path := range []string{
		"/other/addressbooks/alice/contacts/",
		"/dav/calendars/alice/work/",
		"/dav/principals/",
		"/dav/principals/alice/extra/",
		"/dav/addressbooks/",
		"/dav/addressbooks/alice/contacts/ada.vcf/extra",
	} {
		t.Run(path, func(t *testing.T) {
			if got, err := testPaths.Parse(path); err == nil {
				t.Errorf("Parse(%q) = %+v, want an error", path, got)
			}
		})
	}
}

func TestPathsRoundTrip(t *testing.T) {
	object := testPaths.AddressObject("alice", "contacts", "ada.vcf")
	res, err := testPaths.Parse(object)
	if err != nil {
		t.Fatalf("Parse(%q) = %v", object, err)
	}
	if res.User != "alice" || res.Collection != "contacts" || res.Object != "ada.vcf" {
		t.Errorf("Parse(%q) = %+v", object, res)
	}
}
