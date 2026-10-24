package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
)

// tokenBytes is how much entropy a session token carries before encoding: 256
// bits. It is far more than guessing resistance needs on a loopback socket,
// and it is cheap, so there is no reason to spend less.
const tokenBytes = 32

// redacted is what a Token renders as anywhere a value gets formatted. The
// token is the only secret the agent has, so the type refuses to print itself:
// a stray %v, a slog attribute or a JSON response body cannot leak it by
// accident, only a deliberate call to Secret can.
const redacted = "Token(redacted)"

// Token is the session token the agent mints at startup. Holding it is the
// whole authorization story for the local API, so it is a type and not a
// string: it hides its own value when formatted, and the only comparison it
// offers is a constant-time one.
//
// The zero Token matches nothing.
type Token struct {
	// secret is the encoded token, the value a client has to present.
	secret string
	// digest is sha256(secret), kept so the comparison runs over two
	// fixed-size values. Comparing digests rather than the tokens themselves
	// is what keeps the length of the presented value out of the result: a
	// one-byte guess and a one-megabyte guess take the same path through the
	// same 32-byte compare.
	digest [sha256.Size]byte
}

// NewToken mints a fresh session token from crypto/rand, the operating
// system's cryptographic random source. There is no seeding and no fallback,
// so a token is either unpredictable or the error is returned and the agent
// does not start.
func NewToken() (Token, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, &ServerError{
			Op: "new", Kind: KindToken,
			Message: "a session token could not be generated from the system random source",
			Err:     err,
		}
	}
	// Base64url, unpadded: the token travels in a URL and in a header, and
	// this keeps it free of anything either of those has to escape.
	secret := base64.RawURLEncoding.EncodeToString(raw)
	return Token{secret: secret, digest: sha256.Sum256([]byte(secret))}, nil
}

// Secret returns the token value a client has to present. Every call site is a
// place the secret could escape, so there are exactly two: the startup line
// that tells the operator the URL, and the tests.
func (t Token) Secret() string {
	return t.secret
}

// matches reports whether presented is this token.
//
// The comparison is crypto/subtle's constant-time one over two SHA-256
// digests, so neither the position of the first differing byte nor the length
// of presented changes how long the answer takes. A plain == would leak both,
// and a length check in front of the compare would leak the second, which is
// why there is no length check here.
func (t Token) matches(presented string) bool {
	if t.secret == "" {
		// An unset token authorizes nothing. This branches on the server's own
		// configuration, which is the same for every request, so it tells a
		// client nothing it did not already know.
		return false
	}
	presentedDigest := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(t.digest[:], presentedDigest[:]) == 1
}

// String hides the token. It is what makes fmt verbs safe on a Token.
func (t Token) String() string {
	return redacted
}

// LogValue hides the token from log/slog, which asks for this before it asks
// for String.
func (t Token) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// MarshalText hides the token from encoding/json and anything else that
// marshals text, so a Token that ends up in a response body carries no secret.
func (t Token) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}
