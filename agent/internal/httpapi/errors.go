package httpapi

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind says what went wrong while starting the HTTP API. It exists so a
// caller branches on a value rather than on the wording of a message, the same
// shape the pipeline package reports its failures in.
type ErrorKind string

const (
	// KindToken is a session token that could not be generated, which means
	// the system's cryptographic random source failed. There is no fallback:
	// a token from a weaker source is worse than no server at all.
	KindToken ErrorKind = "token"
	// KindBind is the loopback listener: the address could not be bound.
	KindBind ErrorKind = "bind"
	// KindOrigin is a configured allowed origin that is not an origin.
	KindOrigin ErrorKind = "origin"
	// KindShutdown is a server that did not stop cleanly.
	KindShutdown ErrorKind = "shutdown"
)

// ServerError is the one structured failure this package reports. Every field
// but Kind and Message is optional, so a failure carries as much as it knows:
// what was being done, which configured value was at fault, and the cause.
//
// It deliberately never carries the session token, nor anything a client
// presented, because these errors are the things that get logged.
type ServerError struct {
	// Op is what was being done: "new", "serve" or "shutdown".
	Op string
	// Kind says what went wrong.
	Kind ErrorKind
	// Location is a JSON Pointer into the options, naming the offending
	// field; "" is the options as a whole.
	Location string
	// Value is the offending configured value, when quoting it helps and it
	// came from the operator rather than from a client.
	Value string
	// Message is a human-readable explanation, not a stable identifier.
	Message string
	// Err is the underlying cause, when there is one.
	Err error
}

func (e *ServerError) Error() string {
	var out strings.Builder
	if e.Op != "" {
		out.WriteString(e.Op)
		out.WriteString(" ")
	}
	out.WriteString("agent http: ")
	out.WriteString(e.Message)
	if e.Location != "" {
		fmt.Fprintf(&out, " at %s", e.Location)
	}
	if e.Value != "" {
		fmt.Fprintf(&out, " (%q)", e.Value)
	}
	if e.Err != nil {
		out.WriteString(": ")
		out.WriteString(e.Err.Error())
	}
	return out.String()
}

// Unwrap exposes the underlying cause, so errors.Is and errors.As reach it.
func (e *ServerError) Unwrap() error {
	return e.Err
}

// kindOf reports the kind of err, or "" when err is not a *ServerError. It is
// what a test or a caller uses instead of matching on a message.
func kindOf(err error) ErrorKind {
	var fault *ServerError
	if errors.As(err, &fault) {
		return fault.Kind
	}
	return ""
}
