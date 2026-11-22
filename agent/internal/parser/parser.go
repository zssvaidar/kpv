// Package parser is the harness every source parser runs in (#37). A parser
// reads one source file's bytes and reports, as contract JSON
// (schema/parser.schema.json), the pipeline stages it recognized, the regions
// it could not, and its warnings. It runs either as a sidecar, a process from
// internal/process's fixed table that reads the source on stdin and writes the
// result to stdout (#38, #39), or in-process, a function that returns the same
// bytes (#40). Both hand their output to one validation function, so no rule
// depends on which transport produced it, and output that breaks any rule is
// refused whole: a caller gets a *Result or an error, never part of one.
//
// The harness takes bytes and never a path: the caller read them through the
// path guard (catalog.ReadSource), and this package imports no agent package
// but internal/process, nor os or path/filepath (source_test.go).
//
// The structs in models_gen.go are generated from schema/parser.schema.json,
// and so is the copy of that schema in this directory, which exists because a
// Go module cannot embed a file from outside itself. Run `pnpm run generate`
// after changing the schema.
package parser

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/process"
)

//go:embed parser.schema.json
var schemaJSON []byte

// SchemaJSON returns the authoritative contract schema, verbatim.
func SchemaJSON() []byte {
	return bytes.Clone(schemaJSON)
}

// MaxSource is the largest source a parser is given: 1 MiB, the same limit
// catalog.ReadSource reads a source file within (catalog.MaxFileSize).
const MaxSource = 1 << 20

// The defaults Options falls back to.
const (
	DefaultTimeout   = 10 * time.Second
	DefaultMaxOutput = 8 << 20
)

// Options bound one parse, the same way for both transports.
type Options struct {
	// Timeout is how long a parse may take. Zero or less is DefaultTimeout.
	Timeout time.Duration
	// MaxOutput is the most output a parser may produce, in bytes. More is
	// outputTooLarge. Zero or less is DefaultMaxOutput.
	MaxOutput int
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxOutput <= 0 {
		o.MaxOutput = DefaultMaxOutput
	}
	return o
}

// Result is a parser's validated answer: the generated contract type, with
// its stages and unrecognized regions sorted by start. A stage's
// configuration holds its numbers as json.Number, so none loses precision.
type Result = ParserResult

// InProcessFunc is an in-process parser. It is handed its own copy of the
// source and returns contract JSON bytes, exactly what a sidecar would write
// to stdout.
type InProcessFunc func(ctx context.Context, source []byte) ([]byte, error)

// Parser runs one parser over a source.
type Parser struct {
	options   Options
	sidecar   *process.Program
	inProcess InProcessFunc
}

// Sidecar is a parser that runs program, writing the source to its stdin and
// reading the result from its stdout.
func Sidecar(program process.Program, options Options) *Parser {
	return &Parser{options: options.withDefaults(), sidecar: &program}
}

// InProcess is a parser that calls parse in this process.
func InProcess(parse InProcessFunc, options Options) *Parser {
	return &Parser{options: options.withDefaults(), inProcess: parse}
}

// Parse checks source, runs the parser over it and validates what it answers.
// On any error the Result is nil.
func (p *Parser) Parse(ctx context.Context, source []byte) (*Result, error) {
	if len(source) > MaxSource {
		return nil, &Error{Kind: KindSourceTooLarge}
	}
	if !utf8.Valid(source) || bytes.IndexByte(source, 0) >= 0 {
		return nil, &Error{Kind: KindInvalidSource}
	}
	if p == nil || (p.sidecar == nil && p.inProcess == nil) {
		return nil, &Error{Kind: KindUnavailable}
	}

	ctx, cancel := context.WithTimeout(ctx, p.options.Timeout)
	defer cancel()
	var output []byte
	var failed *Error
	if p.sidecar != nil {
		output, failed = runSidecar(ctx, *p.sidecar, source)
	} else {
		output, failed = runInProcess(ctx, p.inProcess, source)
	}
	if failed != nil {
		return nil, failed
	}
	if len(output) > p.options.MaxOutput {
		return nil, &Error{Kind: KindOutputTooLarge}
	}
	result, problems := Validate(source, output)
	if len(problems) > 0 {
		return nil, &Error{Kind: KindMalformed, Problems: problems}
	}
	return result, nil
}

// runSidecar runs the program and maps how it ended onto this package's
// kinds. Only a child that exited 0 has its stdout looked at, so a valid
// result from a child that then failed is never validated or returned, and
// stderr from a child that exited 0 is dropped.
func runSidecar(ctx context.Context, program process.Program, source []byte) ([]byte, *Error) {
	output, err := process.Run(ctx, program, source)
	if err == nil {
		return output.Stdout, nil
	}
	var ran *process.Error
	if !errors.As(err, &ran) {
		return nil, &Error{Kind: KindCrashed, Err: err}
	}
	mapped := &Error{Stderr: string(ran.Stderr), StderrTruncated: ran.StderrTruncated, Err: err}
	switch ran.Kind {
	case process.KindNotFound, process.KindInvalidProgram:
		return nil, &Error{Kind: KindUnavailable, Err: err}
	case process.KindExited:
		mapped.Kind = KindCrashed
		mapped.ExitCode = ran.ExitCode
	case process.KindSignaled:
		mapped.Kind = KindCrashed
		mapped.Signal = ran.Signal
	case process.KindStartFailed:
		mapped.Kind = KindCrashed
	case process.KindTimedOut:
		mapped.Kind = KindTimedOut
	case process.KindCanceled:
		mapped.Kind = KindCanceled
	case process.KindOutputTooLarge:
		mapped.Kind = KindOutputTooLarge
	case process.KindUnsupported:
		mapped.Kind = KindUnsupported
	default:
		mapped.Kind = KindCrashed
	}
	return nil, mapped
}

// outcome is what an in-process parser's goroutine hands back.
type outcome struct {
	output   []byte
	err      error
	panicked bool
	value    string
}

// runInProcess calls parse on a goroutine with its own copy of the source and
// waits for it or for the context, whichever is first. A parser that ignores
// its context is answered timedOut at the deadline; its goroutine runs on
// until parse returns, because a goroutine cannot be killed, and what it
// returns then is dropped.
func runInProcess(ctx context.Context, parse InProcessFunc, source []byte) ([]byte, *Error) {
	own := append(make([]byte, 0, len(source)), source...)
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				done <- outcome{panicked: true, value: fmt.Sprint(value)}
			}
		}()
		output, err := parse(ctx, own)
		done <- outcome{output: output, err: err}
	}()
	select {
	case result := <-done:
		switch {
		case result.panicked:
			return nil, &Error{Kind: KindPanicked, Stderr: result.value}
		case result.err != nil:
			return nil, &Error{Kind: KindCrashed, Stderr: result.err.Error(), Err: result.err}
		}
		return result.output, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &Error{Kind: KindTimedOut}
		}
		return nil, &Error{Kind: KindCanceled}
	}
}
