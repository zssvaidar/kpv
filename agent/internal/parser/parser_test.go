//go:build unix

package parser_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/parser"
	"github.com/zssvaidar/kpv/agent/internal/process"
)

// refused asserts a parse failed with kind and no Result.
func refused(t *testing.T, result *parser.Result, err error, kind parser.Kind) parser.Error {
	t.Helper()
	if result != nil {
		t.Fatalf("a refused parse returned a result: %+v", result)
	}
	var typed *parser.Error
	if !errors.As(err, &typed) {
		t.Fatalf("got %v (%T), want a *parser.Error of kind %s", err, err, kind)
	}
	if typed.Kind != kind || parser.KindOf(err) != kind {
		t.Fatalf("got kind %s (%v), want %s", typed.Kind, err, kind)
	}
	return *typed
}

func TestAValidResultIsAnsweredSortedByStart(t *testing.T) {
	t.Parallel()
	for transport, p := range bothTransports(t, "valid", parser.Options{}) {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			result, err := p.Parse(context.Background(), theSource)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if result.Contract != 1 || result.Status != parser.ParserStatusParsed {
				t.Fatalf("got %+v", result)
			}
			if len(result.Stages) != 2 || result.Stages[0].Type != parser.ParserStageTypeKafkaSource || result.Stages[1].Type != parser.ParserStageTypeKafkaDestination {
				t.Fatalf("stages are not sorted by start: %+v", result.Stages)
			}
			// The ranges around é count bytes: the source stage ends after the
			// closing parenthesis at 8, and the warning covers é's two bytes.
			if result.Stages[0].Range != (parser.ParserRange{Start: 0, End: 9}) {
				t.Fatalf("source stage range %+v", result.Stages[0].Range)
			}
			if got := string(theSource[result.Warnings[0].Range.Start:result.Warnings[0].Range.End]); got != "é" {
				t.Fatalf("the warning covers %q, want é", got)
			}
			if result.Stages[0].Configuration["topic"] != "é" {
				t.Fatalf("configuration %+v", result.Stages[0].Configuration)
			}
			if len(result.Unrecognized) != 1 || result.Unrecognized[0].Reason == nil || *result.Unrecognized[0].Reason != "a newline" {
				t.Fatalf("unrecognized %+v", result.Unrecognized)
			}
		})
	}
}

// Every rule the harness adds to the schema, each broken once, plus every
// invalid shared fixture: each transport answers the same kind and the same
// problems, because both hand their bytes to the same function.
func TestEveryMalformedOutputIsRefusedTheSameWayByBothTransports(t *testing.T) {
	t.Parallel()
	type malformed struct {
		name   string
		output string
		want   []parser.Problem
	}
	result := func(stages, unrecognized, warnings string) string {
		return `{"contract":1,"status":"parsed","stages":[` + stages + `],"unrecognized":[` + unrecognized + `],"warnings":[` + warnings + `]}`
	}
	stage := func(start, end int) string {
		return fmt.Sprintf(`{"type":"filter","range":{"start":%d,"end":%d},"configuration":{}}`, start, end)
	}
	region := func(start, end int) string { return fmt.Sprintf(`{"range":{"start":%d,"end":%d}}`, start, end) }
	p := func(pointer, message string) parser.Problem {
		return parser.Problem{Pointer: pointer, Message: message}
	}
	cases := []malformed{
		{"empty", "", []parser.Problem{p("", "The output is not one JSON document.")}},
		{"not JSON", "not json", []parser.Problem{p("", "The output is not one JSON document.")}},
		{"cut short", `{"contract":1`, []parser.Problem{p("", "The output is not one JSON document.")}},
		{"two documents", result("", "", "") + result("", "", ""), []parser.Problem{p("", "The output holds something after its JSON document.")}},
		{"trailing text", result("", "", "") + " ok", []parser.Problem{p("", "The output holds something after its JSON document.")}},
		{"not UTF-8", "{\"contract\":1,\"x\":\"\xff\"}", []parser.Problem{p("", "The output is not UTF-8.")}},
		{"a key twice", `{"contract":1,"contract":1,"status":"parsed","stages":[],"unrecognized":[],"warnings":[]}`, []parser.Problem{p("/contract", "This key appears more than once in its object.")}},
		{"a nested key twice", result(`{"type":"filter","range":{"start":0,"end":1,"end":2},"configuration":{}}`, "", ""), []parser.Problem{p("/stages/0/range/end", "This key appears more than once in its object.")}},
		{"past the end", result(stage(10, 17), "", ""), []parser.Problem{p("/stages/0/range", "The range ends past the end of the source.")}},
		{"huge offset", result("", `{"range":{"start":0,"end":99999999999999999999999}}`, ""), []parser.Problem{p("/unrecognized/0/range", "The range ends past the end of the source.")}},
		{"empty range", result(stage(3, 3), "", ""), []parser.Problem{p("/stages/0/range", "The range is empty or ends before it starts.")}},
		{"reversed range", result("", region(9, 2), ""), []parser.Problem{p("/unrecognized/0/range", "The range is empty or ends before it starts.")}},
		{"ends inside é", result(stage(0, 6), "", ""), []parser.Problem{p("/stages/0/range", "The range ends inside a UTF-8 character.")}},
		{"starts inside é", result(stage(6, 9), "", ""), []parser.Problem{p("/stages/0/range", "The range starts inside a UTF-8 character.")}},
		{"warning splits é", result("", "", `{"message":"m","range":{"start":5,"end":6}}`), []parser.Problem{p("/warnings/0/range", "The range ends inside a UTF-8 character.")}},
		{"warning past the end", result("", "", `{"message":"m","range":{"start":0,"end":40}}`), []parser.Problem{p("/warnings/0/range", "The range ends past the end of the source.")}},
		{"stages overlap", result(stage(0, 9)+","+stage(8, 16), "", ""), []parser.Problem{p("/stages/1/range", "This range overlaps another stage or unrecognized region.")}},
		{"a stage inside another", result(stage(0, 16)+","+stage(10, 12), "", ""), []parser.Problem{p("/stages/1/range", "This range overlaps another stage or unrecognized region.")}},
		{"a region overlaps a stage", result(stage(0, 9), region(5, 10), ""), []parser.Problem{p("/unrecognized/0/range", "This range overlaps another stage or unrecognized region.")}},
		{"regions overlap", result("", region(0, 9)+","+region(0, 9), ""), []parser.Problem{p("/unrecognized/1/range", "This range overlaps another stage or unrecognized region.")}},
		{"unparseable with a stage", `{"contract":1,"status":"unparseable","stages":[` + stage(0, 9) + `],"unrecognized":[],"warnings":[{"message":"bad"}]}`, []parser.Problem{p("/stages", "An unparseable result has stages.")}},
		{"unparseable without a warning", `{"contract":1,"status":"unparseable","stages":[],"unrecognized":[],"warnings":[]}`, []parser.Problem{p("/warnings", "An unparseable result has no warning.")}},
		{"contract 2", `{"contract":2,"status":"parsed","stages":[],"unrecognized":[],"warnings":[]}`, []parser.Problem{p("/contract", "This value is not one the contract allows.")}},
		{"missing stages", `{"contract":1,"status":"parsed","unrecognized":[],"warnings":[]}`, []parser.Problem{p("/stages", "This required property is missing.")}},
		{"extra property", `{"contract":1,"status":"parsed","stages":[],"unrecognized":[],"warnings":[],"x":1}`, []parser.Problem{p("/x", "This property is not part of the contract.")}},
	}
	for _, fixture := range loadFixtures(t).Invalid {
		cases = append(cases, malformed{name: "fixture " + fixture.Name, output: string(fixture.Result)})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			output := []byte(c.output)
			sidecar := parser.Sidecar(sidecarProgram(t, "emit", hex.EncodeToString(output)), parser.Options{})
			inProcess := parser.InProcess(emitting(output), parser.Options{})
			var answers [][]parser.Problem
			for _, p := range []*parser.Parser{sidecar, inProcess} {
				result, err := p.Parse(context.Background(), theSource)
				answers = append(answers, refused(t, result, err, parser.KindMalformed).Problems)
			}
			if !reflect.DeepEqual(answers[0], answers[1]) {
				t.Fatalf("the transports disagree:\n  sidecar    %v\n  in-process %v", answers[0], answers[1])
			}
			if len(answers[0]) == 0 {
				t.Fatal("a malformed output was refused with no problem")
			}
			if c.want != nil && !reflect.DeepEqual(answers[0], c.want) {
				t.Fatalf("problems %v, want %v", answers[0], c.want)
			}
			direct, problems := parser.Validate(theSource, output)
			if direct != nil || !reflect.DeepEqual(problems, answers[0]) {
				t.Fatalf("Validate answers %v, the transports %v", problems, answers[0])
			}
		})
	}
}

func TestTheNamedMalformedBehavioursAreRefusedByBothTransports(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"malformed", "overlapping", "out-of-bounds", "splits-a-character"} {
		for transport, p := range bothTransports(t, name, parser.Options{}) {
			t.Run(name+"/"+transport, func(t *testing.T) {
				t.Parallel()
				result, err := p.Parse(context.Background(), theSource)
				refused(t, result, err, parser.KindMalformed)
			})
		}
	}
}

func TestRangesThatOnlyTouchDoNotOverlap(t *testing.T) {
	t.Parallel()
	output := `{"contract":1,"status":"parsed","stages":[{"type":"filter","range":{"start":9,"end":16},"configuration":{}},{"type":"transform","range":{"start":0,"end":5},"configuration":{}}],"unrecognized":[{"range":{"start":7,"end":9}},{"range":{"start":5,"end":7},"reason":"é"}],"warnings":[{"message":"over everything","range":{"start":0,"end":16}}]}`
	result, problems := parser.Validate(theSource, []byte(output))
	if len(problems) != 0 {
		t.Fatalf("touching ranges were refused: %v", problems)
	}
	if result.Stages[0].Range.Start != 0 || result.Unrecognized[0].Range.Start != 5 || result.Unrecognized[1].Range.Start != 7 {
		t.Fatalf("not sorted by start: %+v %+v", result.Stages, result.Unrecognized)
	}
}

func TestAnEmptySourceWithNoRangesIsAccepted(t *testing.T) {
	t.Parallel()
	empty := `{"contract":1,"status":"parsed","stages":[],"unrecognized":[],"warnings":[{"message":"nothing here"}]}`
	result, err := parser.InProcess(emitting([]byte(empty)), parser.Options{}).Parse(context.Background(), []byte{})
	if err != nil || result == nil || len(result.Stages) != 0 {
		t.Fatalf("got %+v, %v", result, err)
	}
	withRange := `{"contract":1,"status":"parsed","stages":[],"unrecognized":[{"range":{"start":0,"end":1}}],"warnings":[]}`
	_, err = parser.InProcess(emitting([]byte(withRange)), parser.Options{}).Parse(context.Background(), nil)
	if parser.KindOf(err) != parser.KindMalformed {
		t.Fatalf("a range in an empty source: %v", err)
	}
}

func TestOffsetsWrittenAsOtherIntegerFormsAreAccepted(t *testing.T) {
	t.Parallel()
	output := `{"contract":1.0,"status":"parsed","stages":[{"type":"filter","range":{"start":0.0,"end":9e0},"configuration":{"n":12345678901234567890}}],"unrecognized":[],"warnings":[]}`
	result, problems := parser.Validate(theSource, []byte(output))
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if result.Stages[0].Range != (parser.ParserRange{Start: 0, End: 9}) {
		t.Fatalf("range %+v", result.Stages[0].Range)
	}
	if n, ok := result.Stages[0].Configuration["n"].(json.Number); !ok || n.String() != "12345678901234567890" {
		t.Fatalf("a large number lost precision: %#v", result.Stages[0].Configuration["n"])
	}
}

// Before anything starts: a source the harness refuses never reaches a
// parser, not even one that is not installed.
func TestABadSourceIsRefusedBeforeAnyParserStarts(t *testing.T) {
	t.Parallel()
	if parser.MaxSource != catalog.MaxFileSize {
		t.Fatalf("MaxSource is %d, catalog reads sources within %d", parser.MaxSource, catalog.MaxFileSize)
	}
	called := false
	inProcess := parser.InProcess(func(context.Context, []byte) ([]byte, error) {
		called = true
		return nil, nil
	}, parser.Options{})
	missing := parser.Sidecar(process.ForTest("kpv-no-such-parser", nil, nil, process.Limits{Timeout: time.Second, MaxStdout: 1}), parser.Options{})
	cases := []struct {
		source []byte
		kind   parser.Kind
	}{
		{bytes.Repeat([]byte("a"), parser.MaxSource+1), parser.KindSourceTooLarge},
		{[]byte("a\x00b"), parser.KindInvalidSource},
		{[]byte("caf\xe9"), parser.KindInvalidSource},
		{[]byte("é")[:1], parser.KindInvalidSource},
	}
	for _, c := range cases {
		for _, p := range []*parser.Parser{inProcess, missing} {
			result, err := p.Parse(context.Background(), c.source)
			refused(t, result, err, c.kind)
		}
	}
	if called {
		t.Fatal("the in-process parser was called with a refused source")
	}
	// Exactly 1 MiB is not too large.
	ok := `{"contract":1,"status":"parsed","stages":[],"unrecognized":[],"warnings":[]}`
	if _, err := parser.InProcess(emitting([]byte(ok)), parser.Options{}).Parse(context.Background(), bytes.Repeat([]byte("a"), parser.MaxSource)); err != nil {
		t.Fatalf("a source of exactly MaxSource: %v", err)
	}
}

func TestASidecarThatIsNotInstalledIsUnavailable(t *testing.T) {
	t.Parallel()
	missing := parser.Sidecar(process.ForTest("kpv-no-such-parser", nil, nil, process.Limits{Timeout: time.Second, MaxStdout: 1}), parser.Options{})
	result, err := missing.Parse(context.Background(), theSource)
	refused(t, result, err, parser.KindUnavailable)
	result, err = parser.Sidecar(process.Program{}, parser.Options{}).Parse(context.Background(), theSource)
	refused(t, result, err, parser.KindUnavailable)
}

func TestACrashKeepsWhatItSaid(t *testing.T) {
	t.Parallel()
	for transport, p := range bothTransports(t, "crash", parser.Options{}) {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			result, err := p.Parse(context.Background(), theSource)
			failed := refused(t, result, err, parser.KindCrashed)
			if !strings.Contains(failed.Stderr, "Traceback: the parser broke") {
				t.Fatalf("stderr %q", failed.Stderr)
			}
			if transport == "sidecar" && failed.ExitCode != 2 {
				t.Fatalf("exit code %d, want 2", failed.ExitCode)
			}
		})
	}
}

func TestASidecarKilledByASignalCrashed(t *testing.T) {
	t.Parallel()
	result, err := parser.Sidecar(sidecarProgram(t, "signal"), parser.Options{}).Parse(context.Background(), theSource)
	failed := refused(t, result, err, parser.KindCrashed)
	if failed.Signal == "" || failed.Stderr != "about to be killed" {
		t.Fatalf("signal %q, stderr %q", failed.Signal, failed.Stderr)
	}
}

// A valid result followed by a failure is a failure, and the result is never
// looked at.
func TestAValidResultWithAFailureIsCrashed(t *testing.T) {
	t.Parallel()
	for transport, p := range bothTransports(t, "valid-then-fail", parser.Options{}) {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			result, err := p.Parse(context.Background(), theSource)
			failed := refused(t, result, err, parser.KindCrashed)
			if !strings.Contains(failed.Stderr, "failed after writing") || strings.Contains(failed.Stderr, "kafkaSource") {
				t.Fatalf("stderr %q", failed.Stderr)
			}
		})
	}
}

func TestStderrFromASidecarThatSucceedsIsDropped(t *testing.T) {
	t.Parallel()
	result, err := parser.Sidecar(sidecarProgram(t, "valid-with-stderr"), parser.Options{}).Parse(context.Background(), theSource)
	if err != nil || result == nil || len(result.Stages) != 2 {
		t.Fatalf("got %+v, %v", result, err)
	}
}

func TestAPanicIsPanickedAndTheAgentRunsOn(t *testing.T) {
	t.Parallel()
	p := parser.InProcess(fakeInProcess(t, "panic"), parser.Options{})
	for range 3 {
		result, err := p.Parse(context.Background(), theSource)
		failed := refused(t, result, err, parser.KindPanicked)
		if failed.Stderr != "the parser exploded" {
			t.Fatalf("panic text %q", failed.Stderr)
		}
	}
}

func TestAParserPastItsTimeoutIsTimedOut(t *testing.T) {
	t.Parallel()
	for transport, p := range bothTransports(t, "hang", parser.Options{Timeout: 300 * time.Millisecond}) {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			started := time.Now()
			result, err := p.Parse(context.Background(), theSource)
			refused(t, result, err, parser.KindTimedOut)
			// The in-process fake ignores its context entirely; it is still
			// answered at the deadline.
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("answered after %v", elapsed)
			}
		})
	}
}

func TestACanceledParseIsCanceled(t *testing.T) {
	t.Parallel()
	for transport, p := range bothTransports(t, "hang", parser.Options{}) {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(300*time.Millisecond, cancel)
			result, err := p.Parse(ctx, theSource)
			refused(t, result, err, parser.KindCanceled)
		})
	}
}

func TestTooMuchOutputIsOutputTooLarge(t *testing.T) {
	t.Parallel()
	options := parser.Options{MaxOutput: len(validOutput) + 1024}
	for transport, p := range bothTransports(t, "too-much-output", options) {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			result, err := p.Parse(context.Background(), theSource)
			refused(t, result, err, parser.KindOutputTooLarge)
		})
	}
	// The same output under the default cap is valid: only the size refused
	// it.
	for transport, p := range bothTransports(t, "too-much-output", parser.Options{}) {
		if _, err := p.Parse(context.Background(), theSource); err != nil {
			t.Fatalf("%s under the default cap: %v", transport, err)
		}
	}
	if parser.DefaultMaxOutput != 8<<20 || parser.DefaultTimeout != 10*time.Second {
		t.Fatalf("the defaults are %d and %v", parser.DefaultMaxOutput, parser.DefaultTimeout)
	}
}

func TestTheInProcessParserGetsItsOwnCopy(t *testing.T) {
	t.Parallel()
	source := bytes.Clone(theSource)
	p := parser.InProcess(func(_ context.Context, given []byte) ([]byte, error) {
		for index := range given {
			given[index] = 'x'
		}
		return []byte(validOutput), nil
	}, parser.Options{})
	if _, err := p.Parse(context.Background(), source); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !bytes.Equal(source, theSource) {
		t.Fatalf("the parser changed the caller's bytes: %q", source)
	}
}

func TestParsesRunInParallel(t *testing.T) {
	t.Parallel()
	parsers := bothTransports(t, "valid", parser.Options{})
	var group sync.WaitGroup
	errs := make(chan error, 16)
	for index := range 16 {
		p := parsers["sidecar"]
		if index%2 == 0 {
			p = parsers["in-process"]
		}
		group.Add(1)
		go func() {
			defer group.Done()
			if result, err := p.Parse(context.Background(), theSource); err != nil || len(result.Stages) != 2 {
				errs <- fmt.Errorf("parse %d: %v", index, err)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
