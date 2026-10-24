package channel

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind says what went wrong on the channel. It exists so a caller
// branches on a value rather than on the wording of a message, the same shape
// the pipeline and httpapi packages report their failures in.
type ErrorKind string

const (
	// KindUpgrade is a handshake that was not accepted. The HTTP boundary has
	// already answered the client by then, with its one uniform rejection, so
	// this kind exists for the agent's own log and not for a response.
	KindUpgrade ErrorKind = "upgrade"
	// KindClosed is a connection asked for after the hub has shut down.
	KindClosed ErrorKind = "closed"
)

// Error is the one structured failure this package reports. It is named for
// the package it is in, as url.Error is: channel.Error, not
// channel.ChannelError.
//
// It deliberately never carries anything a client presented, because these
// errors are the things that get logged: a reason in the agent's own words,
// the kind, and the cause from the library.
type Error struct {
	// Op is what was being done: "serve".
	Op string
	// Kind says what went wrong.
	Kind ErrorKind
	// Message is a human-readable explanation, not a stable identifier.
	Message string
	// Err is the underlying cause, when there is one.
	Err error
}

func (e *Error) Error() string {
	var out strings.Builder
	if e.Op != "" {
		out.WriteString(e.Op)
		out.WriteString(" ")
	}
	out.WriteString("agent channel: ")
	out.WriteString(e.Message)
	if e.Err != nil {
		fmt.Fprintf(&out, ": %s", e.Err.Error())
	}
	return out.String()
}

// Unwrap exposes the underlying cause, so errors.Is and errors.As reach it.
func (e *Error) Unwrap() error {
	return e.Err
}

// KindOf reports the kind of err, or "" when err is not a *Error. It is
// what a caller uses instead of matching on a message.
func KindOf(err error) ErrorKind {
	var fault *Error
	if errors.As(err, &fault) {
		return fault.Kind
	}
	return ""
}
