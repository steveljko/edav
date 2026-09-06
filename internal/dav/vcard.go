package dav

import (
	"bytes"
	"fmt"

	"github.com/emersion/go-vcard"
)

// cardIndex extracts the values indexed on write: the UID that identifies the
// contact across collections, and FN as its human-readable label. The parsed
// card is discarded; only the client's bytes are stored.
func cardIndex(raw []byte) (uid, fn string, err error) {
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return "", "", fmt.Errorf("parse vCard: %w", err)
	}

	uid = card.Value(vcard.FieldUID)
	fn = card.Value(vcard.FieldFormattedName)
	if uid == "" {
		// RFC 6352 §6.3.2 does not require UID, but without one nothing links
		// a contact across collections, and clients that rely on it resync.
		return "", fn, fmt.Errorf("vCard has no UID")
	}
	return uid, fn, nil
}
