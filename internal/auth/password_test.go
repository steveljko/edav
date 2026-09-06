package auth

import (
	"errors"
	"strings"
	"testing"
)

// Cheap parameters keep the table tests fast; correctness does not depend on
// the cost.
var testParams = Params{Memory: 64, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashPasswordFormat(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() = %v", err)
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("hash %q has %d fields, want 5", encoded, len(parts)-1)
	}
	if parts[1] != "argon2id" {
		t.Errorf("algorithm = %q, want argon2id", parts[1])
	}
	if parts[3] != "m=19456,t=2,p=1" {
		t.Errorf("parameters = %q, want m=19456,t=2,p=1", parts[3])
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a, err := hashPasswordWith("same password", testParams)
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}
	b, err := hashPasswordWith("same password", testParams)
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical, want distinct salts")
	}
}

func TestVerifyPassword(t *testing.T) {
	const password = "hunter2hunter2"
	encoded, err := hashPasswordWith(password, testParams)
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}

	tests := []struct {
		name    string
		attempt string
		want    error
	}{
		{"correct", password, nil},
		{"wrong", "hunter3hunter3", ErrMismatch},
		{"empty", "", ErrMismatch},
		{"case differs", "Hunter2Hunter2", ErrMismatch},
		{"prefix of correct", "hunter2", ErrMismatch},
		{"correct with trailing space", password + " ", ErrMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := VerifyPassword(encoded, tt.attempt); !errors.Is(err, tt.want) {
				t.Errorf("VerifyPassword(_, %q) = %v, want %v", tt.attempt, err, tt.want)
			}
		})
	}
}

func TestVerifyPasswordRoundTripsUnicodeAndLength(t *testing.T) {
	for _, password := range []string{
		"",
		" ",
		"pässwörd wíth ünicode 🔑",
		strings.Repeat("long", 256),
	} {
		encoded, err := hashPasswordWith(password, testParams)
		if err != nil {
			t.Fatalf("hashPasswordWith(%q) = %v", password, err)
		}
		if err := VerifyPassword(encoded, password); err != nil {
			t.Errorf("VerifyPassword(_, %q) = %v, want nil", password, err)
		}
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	valid, err := hashPasswordWith("hunter2hunter2", testParams)
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}
	parts := strings.Split(valid, "$")

	tests := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"plaintext", "hunter2hunter2"},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"too few fields", "$argon2id$v=19$m=64,t=1,p=1$" + parts[4]},
		{"too many fields", valid + "$extra"},
		{"missing leading dollar", strings.TrimPrefix(valid, "$")},
		{"unsupported algorithm", "$argon2i$" + strings.Join(parts[2:], "$")},
		{"unreadable version", "$argon2id$v=nineteen$" + strings.Join(parts[3:], "$")},
		{"unsupported version", "$argon2id$v=16$" + strings.Join(parts[3:], "$")},
		{"unreadable parameters", "$argon2id$v=19$m=lots,t=1,p=1$" + strings.Join(parts[4:], "$")},
		{"zero memory", "$argon2id$v=19$m=0,t=1,p=1$" + strings.Join(parts[4:], "$")},
		{"absurd memory", "$argon2id$v=19$m=99999999,t=1,p=1$" + strings.Join(parts[4:], "$")},
		{"zero time", "$argon2id$v=19$m=64,t=0,p=1$" + strings.Join(parts[4:], "$")},
		{"absurd time", "$argon2id$v=19$m=64,t=999,p=1$" + strings.Join(parts[4:], "$")},
		{"zero parallelism", "$argon2id$v=19$m=64,t=1,p=0$" + strings.Join(parts[4:], "$")},
		{"undecodable salt", "$argon2id$v=19$m=64,t=1,p=1$not!base64$" + parts[5]},
		{"undecodable key", "$argon2id$v=19$m=64,t=1,p=1$" + parts[4] + "$not!base64"},
		{"empty salt", "$argon2id$v=19$m=64,t=1,p=1$$" + parts[5]},
		{"empty key", "$argon2id$v=19$m=64,t=1,p=1$" + parts[4] + "$"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyPassword(tt.encoded, "hunter2hunter2")
			if !errors.Is(err, ErrInvalidHash) {
				t.Errorf("VerifyPassword(%q, _) = %v, want ErrInvalidHash", tt.encoded, err)
			}
			if errors.Is(err, ErrMismatch) {
				t.Errorf("VerifyPassword(%q, _) reported a mismatch, want a malformed-hash error", tt.encoded)
			}
		})
	}
}

func TestVerifyPasswordAcceptsRaisedParameters(t *testing.T) {
	encoded, err := hashPasswordWith("hunter2hunter2", Params{
		Memory: 128, Time: 3, Parallelism: 2, SaltLength: 32, KeyLength: 64,
	})
	if err != nil {
		t.Fatalf("hashPasswordWith() = %v", err)
	}
	if err := VerifyPassword(encoded, "hunter2hunter2"); err != nil {
		t.Errorf("VerifyPassword() = %v, want nil", err)
	}
}
