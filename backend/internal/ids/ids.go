// Package ids generates the random identifiers used throughout Filet.
package ids

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
)

var crockford = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// New returns a 128-bit random id encoded as 26 lowercase Crockford base32
// characters ("id26").
func New() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return crockford.EncodeToString(b[:])
}

// RandomBytes returns n bytes from crypto/rand.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// B64 encodes bytes as unpadded base64url.
func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// FromB64 decodes unpadded base64url.
func FromB64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
