package secrets

import (
	"fmt"
	"io"
	"log/slog"
)

// Redacted is what a Secret renders as anywhere a value gets formatted,
// logged or marshalled. It is fixed, so it says nothing about the value, not
// even its length.
const Redacted = "Secret(redacted)"

// Secret is a credential value: one entry of a .env file, or a stored
// credential (#24). It is a type and not a string so that it hides its own
// value everywhere a value can escape by accident.
//
// What is guaranteed, and how, following httpapi's Token (token.go there
// explains the paths in more detail):
//
//   - No fmt verb, no log/slog handler and no encoding/json marshal prints the
//     value, wherever a Secret is held. Format, String, GoString, LogValue and
//     MarshalText cover the ordinary paths.
//   - fmt cannot call any of those methods on a value it reaches through an
//     unexported field of another struct, and %p is handled before Format is
//     consulted; both print the fields raw. So the plaintext is not held in a
//     field at all: reveal is a closure, and the only thing a raw print can
//     show is a function address.
//   - An error that formats a Secret into its message, or holds one, prints
//     the placeholder for the same reasons.
//
// The one way to the value is Reveal, and the one caller of it in this package
// is the .env writer. A Secret is not comparable with ==, which is deliberate:
// compare Reveal() results where a comparison is really needed.
//
// The zero Secret holds the empty value.
type Secret struct {
	reveal func() string
}

// NewSecret wraps value. The caller should drop its own copy as soon as it
// can; nothing here can make Go forget a string.
func NewSecret(value string) Secret {
	return Secret{reveal: func() string { return value }}
}

// Reveal returns the value. Every call is a place the value could escape, so
// a caller writes it to its destination and nowhere else.
func (s Secret) Reveal() string {
	if s.reveal == nil {
		return ""
	}
	return s.reveal()
}

// Format renders the placeholder for every fmt verb, %#v and %x included.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, Redacted)
}

// String hides the value from anything that asks for a string, which includes
// the runtime printing a panic value.
func (s Secret) String() string { return Redacted }

// GoString hides the value from %#v callers that bypass Format.
func (s Secret) GoString() string { return Redacted }

// LogValue hides the value from log/slog, which asks for this first.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalText hides the value from encoding/json, encoding/xml and anything
// else that marshals text, so a Secret in a response body carries no value.
// There is deliberately no UnmarshalText: a Secret is made with NewSecret,
// where a reader of the code can see it happen.
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }
