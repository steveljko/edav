// Package auth provides password hashing, HTTP Basic authentication for DAV
// clients, and cookie sessions with CSRF protection for the admin UI.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrMismatch is returned by VerifyPassword when the password does not match.
var ErrMismatch = errors.New("auth: password does not match")

// ErrInvalidHash is returned when an encoded hash cannot be parsed, or encodes
// parameters outside the accepted range.
var ErrInvalidHash = errors.New("auth: malformed password hash")

// Params are the Argon2id cost parameters. Defaults follow the OWASP minimum
// recommendation for argon2id (19 MiB, t=2, p=1).
type Params struct {
	Memory      uint32 // KiB
	Time        uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams are used by HashPassword. They are recorded inside each encoded
// hash, so raising them later does not invalidate existing passwords.
var DefaultParams = Params{
	Memory:      19 * 1024,
	Time:        2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// Bounds on decoded parameters. A stored hash is attacker-influenced if the
// database is ever compromised, and argon2 will happily allocate whatever
// memory the encoded string asks for.
const (
	maxMemory      = 1 << 21 // 2 GiB in KiB
	maxTime        = 16
	maxParallelism = 16
	maxSaltLength  = 64
	maxKeyLength   = 64
)

// HashPassword returns an Argon2id hash in PHC string format:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
func HashPassword(password string) (string, error) {
	return hashPasswordWith(password, DefaultParams)
}

func hashPasswordWith(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, p.KeyLength)
	b64 := base64.RawStdEncoding

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches encoded. It returns
// ErrMismatch when the password is wrong and ErrInvalidHash when encoded cannot
// be parsed; callers must distinguish the two, since a malformed hash is an
// operational problem rather than a failed login.
func VerifyPassword(encoded, password string) error {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}

	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, p.KeyLength)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	var p Params

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return p, nil, nil, fmt.Errorf("%w: expected 5 fields, got %d", ErrInvalidHash, len(parts)-1)
	}
	if parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, fmt.Errorf("%w: unreadable version %q", ErrInvalidHash, parts[2])
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Parallelism); err != nil {
		return p, nil, nil, fmt.Errorf("%w: unreadable parameters %q", ErrInvalidHash, parts[3])
	}
	if p.Memory == 0 || p.Memory > maxMemory ||
		p.Time == 0 || p.Time > maxTime ||
		p.Parallelism == 0 || p.Parallelism > maxParallelism {
		return p, nil, nil, fmt.Errorf("%w: parameters out of range (%s)", ErrInvalidHash, parts[3])
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("%w: undecodable salt: %v", ErrInvalidHash, err)
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("%w: undecodable key: %v", ErrInvalidHash, err)
	}
	if len(salt) == 0 || len(salt) > maxSaltLength || len(key) == 0 || len(key) > maxKeyLength {
		return p, nil, nil, fmt.Errorf("%w: salt or key length out of range", ErrInvalidHash)
	}

	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	return p, salt, key, nil
}
