//go:build unix

package parser_test

// The two fake parsers. The fake sidecar is this test binary run again
// through process.ForTest with sidecarMarker as its first argument; TestMain
// hands such a run to fakeSidecar before any test flag is parsed. The fake
// in-process parser is fakeInProcess. Both are told what to do by the same
// behaviours, so a test can ask each transport for the same thing.

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/parser"
	"github.com/zssvaidar/kpv/agent/internal/process"
)

const sidecarMarker = "kpv-fake-sidecar"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == sidecarMarker {
		os.Exit(fakeSidecar(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// theSource is what the fakes are written against. é is the two bytes at
// offsets 5 and 6, so 5..7 covers it and 5..6 or 6..7 splits it.
//
//	src("é")\nsink()
//	0123456789...
var theSource = []byte("src(\"é\")\nsink()")

// The ranges of theSource the valid answer reports.
const validOutput = `{"contract":1,"status":"parsed",
"stages":[
  {"type":"kafkaDestination","range":{"start":10,"end":16},"configuration":{"topic":"out"}},
  {"type":"kafkaSource","range":{"start":0,"end":9},"configuration":{"topic":"é"}}
],
"unrecognized":[{"range":{"start":9,"end":10},"reason":"a newline"}],
"warnings":[{"message":"the topic is not ASCII","range":{"start":5,"end":7}}]}
`

// behaviour is one thing a fake can be told to do.
type behaviour struct {
	// output is written to stdout, or returned.
	output string
	// stderr is written to stderr, or is the returned error's text.
	stderr string
	// exitCode is the sidecar's exit code; non-zero is an error returned
	// alongside output in-process.
	exitCode int
	// signal makes the sidecar kill itself with SIGKILL.
	signal bool
	// hang ignores the context and never answers.
	hang bool
	// flood is that many bytes of output, a valid result padded with spaces.
	flood int
	// panics is in-process only.
	panics bool
}

var behaviours = map[string]behaviour{
	"valid":              {output: validOutput},
	"malformed":          {output: `{"contract":1,"status":"parsed"`},
	"overlapping":        {output: `{"contract":1,"status":"parsed","stages":[{"type":"filter","range":{"start":0,"end":9},"configuration":{}},{"type":"filter","range":{"start":8,"end":16},"configuration":{}}],"unrecognized":[],"warnings":[]}`},
	"out-of-bounds":      {output: `{"contract":1,"status":"parsed","stages":[{"type":"filter","range":{"start":10,"end":17},"configuration":{}}],"unrecognized":[],"warnings":[]}`},
	"splits-a-character": {output: `{"contract":1,"status":"parsed","stages":[{"type":"filter","range":{"start":0,"end":6},"configuration":{}}],"unrecognized":[],"warnings":[]}`},
	"crash":              {stderr: "Traceback: the parser broke", exitCode: 2},
	"valid-then-fail":    {output: validOutput, stderr: "failed after writing", exitCode: 1},
	"valid-with-stderr":  {output: validOutput, stderr: "chatter on stderr"},
	"signal":             {stderr: "about to be killed", signal: true},
	"hang":               {hang: true},
	"too-much-output":    {flood: 4096},
	"panic":              {panics: true},
}

func (b behaviour) flooded() []byte {
	return append([]byte(validOutput), bytes.Repeat([]byte(" "), b.flood)...)
}

// fakeSidecar is the sidecar side of a behaviour, or, with "emit" and a hex
// argument, writes exactly those bytes.
func fakeSidecar(args []string) int {
	_, _ = io.ReadAll(os.Stdin)
	if args[0] == "emit" {
		output, _ := hex.DecodeString(args[1])
		_, _ = os.Stdout.Write(output)
		return 0
	}
	b, known := behaviours[args[0]]
	if !known {
		return 99
	}
	if b.flood > 0 {
		_, _ = os.Stdout.Write(b.flooded())
		return 0
	}
	_, _ = os.Stdout.WriteString(b.output)
	_, _ = os.Stderr.WriteString(b.stderr)
	if b.signal {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	if b.hang {
		time.Sleep(time.Hour)
	}
	return b.exitCode
}

// sidecarProgram runs the fake sidecar with args.
func sidecarProgram(t *testing.T, args ...string) process.Program {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	return process.ForTest(self, append([]string{sidecarMarker}, args...), nil, process.Limits{Timeout: time.Minute, MaxStdout: 64 << 20})
}

// fakeInProcess is the in-process side of a behaviour. A hang ignores its
// context and ends only when the test does, so no goroutine outlives it.
func fakeInProcess(t *testing.T, name string) parser.InProcessFunc {
	t.Helper()
	b, known := behaviours[name]
	if !known {
		t.Fatalf("no behaviour %s", name)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return func(_ context.Context, _ []byte) ([]byte, error) {
		switch {
		case b.panics:
			panic("the parser exploded")
		case b.hang:
			<-release
			return []byte(validOutput), nil
		case b.flood > 0:
			return b.flooded(), nil
		case b.signal:
			return nil, errors.New(b.stderr)
		case b.exitCode != 0:
			return []byte(b.output), errors.New(b.stderr + " (exit " + strconv.Itoa(b.exitCode) + ")")
		}
		return []byte(b.output), nil
	}
}

// emitting is an in-process parser that returns exactly output.
func emitting(output []byte) parser.InProcessFunc {
	return func(context.Context, []byte) ([]byte, error) { return output, nil }
}

// bothTransports answers a sidecar and an in-process parser for the same
// behaviour.
func bothTransports(t *testing.T, name string, options parser.Options) map[string]*parser.Parser {
	t.Helper()
	return map[string]*parser.Parser{
		"sidecar":    parser.Sidecar(sidecarProgram(t, name), options),
		"in-process": parser.InProcess(fakeInProcess(t, name), options),
	}
}
