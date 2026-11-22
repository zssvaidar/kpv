package parser

import (
	"errors"
	"fmt"
)

// Kind is why a parse failed. The set is fixed, so a caller can switch on it.
type Kind string

// Every Kind an Error can have.
const (
	// KindSourceTooLarge is a source over MaxSource, refused before any
	// parser starts. It matches catalog's tooLarge.
	KindSourceTooLarge Kind = "sourceTooLarge"
	// KindInvalidSource is a source that is not UTF-8 or holds a NUL,
	// refused before any parser starts. It matches catalog's notText.
	KindInvalidSource Kind = "invalidSource"
	// KindUnavailable is a sidecar whose program is not installed.
	KindUnavailable Kind = "unavailable"
	// KindCrashed is a sidecar that exited non-zero, was killed by a signal
	// or could not start, or an in-process parser that returned an error.
	KindCrashed Kind = "crashed"
	// KindPanicked is an in-process parser that panicked.
	KindPanicked       Kind = "panicked"
	KindTimedOut       Kind = "timedOut"
	KindCanceled       Kind = "canceled"
	KindOutputTooLarge Kind = "outputTooLarge"
	// KindMalformed is output that breaks a rule of the contract; Problems
	// says which.
	KindMalformed   Kind = "malformed"
	KindUnsupported Kind = "unsupported"
)

var sentences = map[Kind]string{
	KindSourceTooLarge: "the source is larger than 1 MiB",
	KindInvalidSource:  "the source is not UTF-8 text without a NUL",
	KindUnavailable:    "the parser is not installed",
	KindCrashed:        "the parser failed",
	KindPanicked:       "the parser panicked",
	KindTimedOut:       "the parser ran past its time limit",
	KindCanceled:       "the parse was canceled",
	KindOutputTooLarge: "the parser wrote more output than it may",
	KindMalformed:      "the parser's output breaks the parser contract",
	KindUnsupported:    "running a parser process is not supported on this system",
}

// Problem is one way a parser's output breaks the contract: where, as a JSON
// pointer into the output ("" is the whole output), and a fixed sentence.
type Problem struct {
	Pointer string
	Message string
}

// Error is every error Parse returns.
type Error struct {
	Kind Kind
	// ExitCode is a crashed sidecar's non-zero exit code.
	ExitCode int
	// Signal is the signal a crashed sidecar was killed by.
	Signal string
	// Stderr is what was kept of a sidecar's stderr, an in-process parser's
	// error text, or a panic's value as text.
	Stderr          string
	StderrTruncated bool
	// Problems is set for KindMalformed, sorted by pointer and then message.
	Problems []Problem
	// Err is the underlying cause where there is one, for a log.
	Err error
}

func (e *Error) Error() string {
	sentence := sentences[e.Kind]
	switch {
	case e.Kind == KindMalformed && len(e.Problems) > 0:
		return fmt.Sprintf("parser: %s: %s %s", sentence, pointerOrRoot(e.Problems[0].Pointer), e.Problems[0].Message)
	case e.ExitCode != 0:
		return fmt.Sprintf("parser: %s (exit code %d)", sentence, e.ExitCode)
	case e.Signal != "":
		return fmt.Sprintf("parser: %s (%s)", sentence, e.Signal)
	}
	return "parser: " + sentence
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf answers the Kind of a parser error, or "" for any other error.
func KindOf(err error) Kind {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return ""
}

func pointerOrRoot(pointer string) string {
	if pointer == "" {
		return "/"
	}
	return pointer
}
