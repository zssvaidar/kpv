package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const canary = "kpv-canary-Pa55w0rd-$HOME-\"q\"-é"

// holder reaches a Secret every way a struct can: exported, unexported,
// embedded, behind a pointer, in a slice and in a map.
type holder struct {
	Exported Secret
	hidden   Secret
	Pointer  *Secret
	List     []Secret
	Map      map[string]Secret
	Entry    Entry
	entry    Entry
}

type embedding struct {
	Name string
	Secret
}

type credentialError struct{ value Secret }

func (e *credentialError) Error() string { return fmt.Sprintf("bad credential %v", e.value) }

func newHolder() holder {
	value := NewSecret(canary)
	return holder{
		Exported: value, hidden: value, Pointer: &value,
		List: []Secret{value}, Map: map[string]Secret{"k": value},
		Entry: Entry{Key: "K", Value: value, Line: 1}, entry: Entry{Key: "K", Value: value, Line: 1},
	}
}

func TestASecretRevealsItsValueOnlyThroughReveal(t *testing.T) {
	t.Parallel()
	if NewSecret(canary).Reveal() != canary {
		t.Fatal("Reveal does not return the value")
	}
	if (Secret{}).Reveal() != "" {
		t.Fatal("the zero Secret is not empty")
	}
}

func TestNoFmtVerbPrintsTheValue(t *testing.T) {
	t.Parallel()
	value := NewSecret(canary)
	h := newHolder()
	wrapped := fmt.Errorf("connect: %w", &credentialError{value: value})
	subjects := []any{value, &value, h, &h, embedding{Name: "n", Secret: value}, wrapped, []any{value}, h.Entry}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T", "%p", "%10.3v"} {
		for i, subject := range subjects {
			out := fmt.Sprintf(verb, subject)
			if strings.Contains(out, canary) || strings.Contains(out, "Pa55w0rd") {
				t.Errorf("%s of subject %d printed the value: %s", verb, i, out)
			}
		}
	}
	for _, out := range []string{fmt.Sprint(value), fmt.Sprintln(h), value.String(), value.GoString(), wrapped.Error()} {
		if strings.Contains(out, "Pa55w0rd") {
			t.Errorf("printed the value: %s", out)
		}
	}
	if got := fmt.Sprintf("%v", value); got != Redacted {
		t.Errorf("%%v = %q, want the placeholder", got)
	}
	if !strings.Contains(wrapped.Error(), Redacted) {
		t.Errorf("a wrapped error does not show the placeholder: %s", wrapped)
	}
	var target *credentialError
	if !errors.As(wrapped, &target) {
		t.Fatal("the wrapped error does not unwrap")
	}
}

func TestSlogPrintsThePlaceholder(t *testing.T) {
	t.Parallel()
	value := NewSecret(canary)
	h := newHolder()
	var out bytes.Buffer
	for _, handler := range []slog.Handler{slog.NewTextHandler(&out, nil), slog.NewJSONHandler(&out, nil)} {
		logger := slog.New(handler)
		logger.Info("value", "secret", value, "holder", h, "pointer", &value,
			"error", fmt.Errorf("x: %w", &credentialError{value: value}), slog.Any("any", value))
		logger.Info(fmt.Sprintf("message %v", value))
		logger.With("secret", value).WithGroup("g").Info("grouped", "entry", h.Entry)
	}
	if strings.Contains(out.String(), "Pa55w0rd") {
		t.Fatalf("a log line holds the value:\n%s", out.String())
	}
	if !strings.Contains(out.String(), Redacted) {
		t.Fatalf("no log line holds the placeholder:\n%s", out.String())
	}
}

func TestJSONMarshalsThePlaceholder(t *testing.T) {
	t.Parallel()
	value := NewSecret(canary)
	type response struct {
		Password Secret            `json:"password"`
		Embedded embedding         `json:"embedded"`
		Entry    Entry             `json:"entry"`
		List     []Secret          `json:"list"`
		Map      map[string]Secret `json:"map"`
		Pointer  *Secret           `json:"pointer"`
	}
	out, err := json.Marshal(response{
		Password: value, Embedded: embedding{Name: "n", Secret: value}, Entry: Entry{Key: "K", Value: value},
		List: []Secret{value}, Map: map[string]Secret{"k": value}, Pointer: &value,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "Pa55w0rd") {
		t.Fatalf("the JSON holds the value: %s", out)
	}
	if !strings.Contains(string(out), `"password":"`+Redacted+`"`) {
		t.Fatalf("the JSON does not hold the placeholder: %s", out)
	}
	embedded, err := json.Marshal(embedding{Name: "n", Secret: value})
	if err != nil {
		t.Fatal(err)
	}
	if string(embedded) != `"`+Redacted+`"` {
		t.Fatalf("a struct embedding a Secret marshals as %s", embedded)
	}
}

// A panic carrying a Secret, an error holding one and a struct holding one
// prints the runtime's own message and a stack trace. The child process is
// this test binary; the value must appear nowhere in what it prints.
func TestAPanicAndItsStackTraceDoNotPrintTheValue(t *testing.T) {
	t.Parallel()
	for _, what := range []string{"secret", "error", "holder"} {
		command := exec.Command(os.Args[0], "-test.run=^TestPanicChild$", "-test.v")
		command.Env = append(os.Environ(), "KPV_SECRETS_PANIC="+what, "GOTRACEBACK=all")
		out, _ := command.CombinedOutput()
		if !strings.Contains(string(out), "panic") {
			t.Fatalf("%s: the child did not panic:\n%s", what, out)
		}
		if strings.Contains(string(out), "Pa55w0rd") {
			t.Fatalf("%s: the panic output holds the value:\n%s", what, out)
		}
	}
}

//go:noinline
func panicWith(value Secret, h holder, what string) {
	switch what {
	case "secret":
		panic(value)
	case "error":
		panic(fmt.Errorf("x: %w", &credentialError{value: value}))
	default:
		panic(h)
	}
}

func TestPanicChild(t *testing.T) {
	what := os.Getenv("KPV_SECRETS_PANIC")
	if what == "" {
		t.Skip("run by TestAPanicAndItsStackTraceDoNotPrintTheValue")
	}
	panicWith(NewSecret(canary), newHolder(), what)
}
