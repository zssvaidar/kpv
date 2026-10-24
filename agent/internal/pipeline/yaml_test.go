// The YAML-to-JSON bridge, and the two properties the YAML library was chosen
// for. Those two are pinned as tests because they are the difference between
// rejecting a bad pipeline file and quietly reading the wrong pipeline, and
// because a library swap would otherwise lose them without a word.

package pipeline_test

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"gopkg.in/yaml.v3"
)

func TestTheYAMLLibraryRejectsADuplicateKeyToo(t *testing.T) {
	// Measured, not assumed. On this document sigs.k8s.io/yaml's YAMLToJSON
	// returns {"id":"b"} and no error, resolving the duplicate to the last
	// occurrence; gopkg.in/yaml.v3 refuses to decode it. That is why this
	// package parses with yaml.v3, and this test fails if that stops being
	// true, even though the bridge rejects the duplicate itself.
	const document = "id: a\nlanguage: python\nid: b\n"

	var into map[string]any
	err := yaml.Unmarshal([]byte(document), &into)
	if err == nil {
		t.Fatalf("the YAML library resolved a duplicate key to %v instead of failing", into)
	}
	if !strings.Contains(err.Error(), "already defined") {
		t.Errorf("the library's duplicate-key error reads %q", err)
	}
}

func TestTheYAMLLibraryTellsNoDocumentApartFromNoContent(t *testing.T) {
	// The other measured property: Unmarshal reads a file with no document in
	// it as a nil value and no error, which is indistinguishable from a
	// document that is explicitly null, and reads as an empty pipeline. A
	// Decoder says io.EOF instead, so the bridge uses a Decoder.
	for _, document := range []string{"", "# a pipeline, one day\n", "\n  \n"} {
		var permissive any
		if err := yaml.Unmarshal([]byte(document), &permissive); err != nil || permissive != nil {
			t.Errorf("Unmarshal(%q) = %v, %v; the premise of this test no longer holds",
				document, permissive, err)
		}

		var strict any
		err := yaml.NewDecoder(strings.NewReader(document)).Decode(&strict)
		if !errors.Is(err, io.EOF) {
			t.Errorf("Decode(%q) = %v, want io.EOF", document, err)
		}
	}
}

func TestTheBridgeKeepsValuesAndTheirTypes(t *testing.T) {
	// Every scalar here would read back as something else if the bridge let
	// YAML resolve it loosely: "1" as a number, "true" as a boolean, "null" as
	// nothing, "1.0" as a float.
	const document = `{"one":"1","octal":"0755","all":"all","yes":"yes","on":"on",` +
		`"nullish":"null","blank":"","truthy":"true","dotted":"1.0","colon":"a: b",` +
		`"hash":"a # b","int":1,"big":9007199254740993,"float":1.5,` +
		`"bool":true,"none":null,"list":[1,"2",false],"map":{},"empty":[]}`

	asYAML, err := pipeline.JSONToYAML([]byte(document))
	if err != nil {
		t.Fatalf("JSONToYAML: %v", err)
	}
	if !strings.Contains(string(asYAML), `one: "1"`) {
		t.Errorf("a string that reads as a number was written unquoted:\n%s", asYAML)
	}

	back, err := pipeline.YAMLToJSON(asYAML)
	if err != nil {
		t.Fatalf("YAMLToJSON: %v", err)
	}

	var want, got any
	decode := func(data []byte, into *any) {
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.UseNumber()
		if err := decoder.Decode(into); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
	}
	decode([]byte(document), &want)
	decode(back, &got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the bridge changed the document\n got %s\nwant %s", back, document)
	}

	// Exponent notation is the one thing that does not survive verbatim: the
	// value is kept, the spelling is normalised, which is what JSON numbers
	// are allowed to do.
	normalised, err := pipeline.YAMLToJSON([]byte("exponent: 1e3\n"))
	if err != nil {
		t.Fatalf("YAMLToJSON: %v", err)
	}
	if want := `{"exponent":1000}`; string(normalised) != want {
		t.Errorf("YAMLToJSON produced %s, want %s", normalised, want)
	}
}

func TestJSONToYAMLKeepsTheOrderOfTheKeys(t *testing.T) {
	// Key order is what makes a written file reproducible, so it comes from
	// the document and not from a map.
	const document = `{"zeta":1,"alpha":2,"middle":{"second":1,"first":2},"omega":3}`
	asYAML, err := pipeline.JSONToYAML([]byte(document))
	if err != nil {
		t.Fatalf("JSONToYAML: %v", err)
	}
	want := "zeta: 1\nalpha: 2\nmiddle:\n  second: 1\n  first: 2\nomega: 3\n"
	if string(asYAML) != want {
		t.Errorf("JSONToYAML wrote\n%s\nwant\n%s", asYAML, want)
	}
}

func TestTheBridgeExpandsAnAlias(t *testing.T) {
	const document = "id: &name shared\nlanguage: python\nalias: *name\n"
	encoded, err := pipeline.YAMLToJSON([]byte(document))
	if err != nil {
		t.Fatalf("YAMLToJSON: %v", err)
	}
	if want := `{"alias":"shared","id":"shared","language":"python"}`; string(encoded) != want {
		t.Errorf("YAMLToJSON produced %s, want %s", encoded, want)
	}
}

func TestTheBridgeRejectsWhatJSONCannotHold(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
		kind     pipeline.ErrorKind
		message  string
	}{
		{
			name:     "a timestamp",
			document: "when: 2026-10-24\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "no JSON equivalent",
		},
		{
			name:     "binary data",
			document: "blob: !!binary aGk=\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "no JSON equivalent",
		},
		{
			name:     "infinity",
			document: "ratio: .inf\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "no JSON equivalent",
		},
		{
			name:     "not a number",
			document: "ratio: .nan\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "no JSON equivalent",
		},
		{
			name:     "an integer key",
			document: "7: seven\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "is not a string",
		},
		{
			name:     "a sequence as a key",
			document: "? [a, b]\n: value\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "is not a string",
		},
		{
			name:     "a merge key, which would smuggle in fields",
			document: "base: &base\n  id: a\ncopy:\n  <<: *base\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "is not a string",
		},
		{
			name:     "an alias that points at itself",
			document: "loop: &self\n  inner: *self\n",
			kind:     pipeline.KindUnsupportedValue,
			message:  "nested more than",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := pipeline.YAMLToJSON([]byte(testCase.document))
			if err == nil {
				t.Fatalf("the bridge accepted it and produced %s", encoded)
			}
			var fault *pipeline.FileError
			if !errors.As(err, &fault) {
				t.Fatalf("the failure is %T, not a *pipeline.FileError: %v", err, err)
			}
			if fault.Kind != testCase.kind {
				t.Errorf("failed as %q, want %q: %v", fault.Kind, testCase.kind, fault)
			}
			if !strings.Contains(fault.Message, testCase.message) {
				t.Errorf("the message %q does not mention %q", fault.Message, testCase.message)
			}
			if fault.Line == 0 {
				t.Errorf("the failure has no line: %v", fault)
			}
		})
	}
}
