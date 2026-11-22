// The Go half of the shared source file endpoint fixtures (#87). The
// TypeScript half is packages/schema/src/source-fixtures.test.ts and reads the
// same file, so what the agent sends and takes and what the browser parses
// and sends are held to the same cases.

package httpapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// sourceFixtures is the one set of fixtures, outside the Go module.
	sourceFixtures = "../../../schema/fixtures/source-responses.json"
	// sourceSchemaCopy is the copy of schema/source.schema.json generation
	// writes beside the generated structs, for these tests only.
	sourceSchemaCopy = "source.schema.json"
	sourceSchemaID   = "https://github.com/zssvaidar/kpv/schema/source.schema.json"
)

type sourceFixtureFile struct {
	Files             directoryFixtureSet `json:"files"`
	Requests          directoryFixtureSet `json:"requests"`
	Written           directoryFixtureSet `json:"written"`
	Errors            directoryFixtureSet `json:"errors"`
	PairingViolations struct {
		Requests []directoryFixtureCase `json:"requests"`
		Errors   []directoryFixtureCase `json:"errors"`
	} `json:"pairingViolations"`
}

// sourceSchemaSet is every shape in the source schema, compiled.
type sourceSchemaSet struct {
	file, request, written, failure *jsonschema.Schema
}

func sourceSchemas(t *testing.T) sourceSchemaSet {
	t.Helper()
	raw, err := os.ReadFile(sourceSchemaCopy)
	if err != nil {
		t.Fatalf("read the source schema copy: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the source schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(sourceSchemaID, document); err != nil {
		t.Fatalf("add the source schema: %v", err)
	}
	compile := func(location string) *jsonschema.Schema {
		schema, err := compiler.Compile(sourceSchemaID + location)
		if err != nil {
			t.Fatalf("compile %s: %v", location, err)
		}
		return schema
	}
	return sourceSchemaSet{
		file:    compile(""),
		request: compile("#/$defs/sourceWriteRequest"),
		written: compile("#/$defs/sourceWritten"),
		failure: compile("#/$defs/sourceError"),
	}
}

func loadSourceFixtures(t *testing.T) sourceFixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sourceFixtures))
	if err != nil {
		t.Fatalf("read the shared source fixtures: %v", err)
	}
	var loaded sourceFixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared source fixtures: %v", err)
	}
	for name, set := range map[string]directoryFixtureSet{
		"files": loaded.Files, "requests": loaded.Requests, "written": loaded.Written, "errors": loaded.Errors,
	} {
		if len(set.Valid) == 0 || len(set.Invalid) == 0 {
			t.Fatalf("the %s fixtures have %d valid and %d invalid cases, want some of each", name, len(set.Valid), len(set.Invalid))
		}
	}
	if len(loaded.PairingViolations.Requests) == 0 || len(loaded.PairingViolations.Errors) == 0 {
		t.Fatal("there are no pairing violations, so the pairings are not tested")
	}
	return loaded
}

// sourcePairingsHold reports whether a request or an error keeps the pairings
// the schema states in words.
func sourcePairingsHold(value any) bool {
	switch typed := value.(type) {
	case *SourceWriteRequest:
		return (typed.ExpectedSha256 != nil) != (typed.ExpectedAbsent != nil)
	case *SourceError:
		return (typed.Error == SourceErrorKindRootUnavailable) == (typed.Reason != nil) &&
			(typed.Sha256 == nil || typed.Error == SourceErrorKindChanged)
	}
	return true
}

// A valid fixture passes the schema, parses into the generated struct, is
// still valid after a round trip and keeps the pairings; an invalid one is
// refused; a pairing violation passes the schema and breaks a pairing. A
// valid error's message is the handler table's.
func TestTheSourceFixturesHoldTheSchemaAndTheGeneratedStructs(t *testing.T) {
	t.Parallel()
	fixtures := loadSourceFixtures(t)
	schemas := sourceSchemas(t)
	for _, group := range []struct {
		name   string
		set    directoryFixtureSet
		schema *jsonschema.Schema
		into   func() any
	}{
		{"file", fixtures.Files, schemas.file, func() any { return &SourceFile{} }},
		{"request", fixtures.Requests, schemas.request, func() any { return &SourceWriteRequest{} }},
		{"written", fixtures.Written, schemas.written, func() any { return &SourceWritten{} }},
		{"error", fixtures.Errors, schemas.failure, func() any { return &SourceError{} }},
	} {
		for _, fixture := range group.set.Valid {
			t.Run(group.name+"/"+fixture.Name, func(t *testing.T) {
				if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err != nil {
					t.Fatalf("should be valid (%s): %v", fixture.Why, err)
				}
				into := group.into()
				if err := json.Unmarshal(fixture.Body, into); err != nil {
					t.Fatalf("does not parse into the generated struct: %v", err)
				}
				again, err := json.Marshal(into)
				if err != nil {
					t.Fatalf("does not marshal back: %v", err)
				}
				if err := group.schema.Validate(decodeDocument(t, again)); err != nil {
					t.Fatalf("not valid after a round trip (%s): %v", again, err)
				}
				if !sourcePairingsHold(into) {
					t.Errorf("breaks a pairing the schema states: %s", fixture.Body)
				}
				if failure, ok := into.(*SourceError); ok && sourceFailures[failure.Error].message != failure.Message {
					t.Errorf("the %s message is not the handler table's: %q", failure.Error, failure.Message)
				}
			})
		}
		for _, fixture := range group.set.Invalid {
			if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err == nil {
				t.Errorf("%s %s should be invalid (%s), and the schema accepted it", group.name, fixture.Name, fixture.Why)
			}
		}
	}
	for _, group := range []struct {
		cases  []directoryFixtureCase
		schema *jsonschema.Schema
		into   func() any
	}{
		{fixtures.PairingViolations.Requests, schemas.request, func() any { return &SourceWriteRequest{} }},
		{fixtures.PairingViolations.Errors, schemas.failure, func() any { return &SourceError{} }},
	} {
		for _, fixture := range group.cases {
			if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err != nil {
				t.Errorf("pairing violation %s should pass the schema: %v", fixture.Name, err)
			}
			into := group.into()
			if err := json.Unmarshal(fixture.Body, into); err != nil {
				t.Fatalf("%s does not parse: %v", fixture.Name, err)
			}
			if sourcePairingsHold(into) {
				t.Errorf("pairing violation %s keeps every pairing (%s)", fixture.Name, fixture.Why)
			}
		}
	}
}
