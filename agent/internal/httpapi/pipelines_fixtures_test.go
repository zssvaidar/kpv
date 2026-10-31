// The Go half of the shared pipeline endpoint fixtures (#48). The TypeScript
// half is packages/schema/src/pipelines-fixtures.test.ts and reads the same
// file, so what the agent sends and takes and what the browser parses and
// sends are held to the same cases.

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
	// pipelinesFixtures is the one set of fixtures, outside the Go module.
	pipelinesFixtures = "../../../schema/fixtures/pipelines-responses.json"
	// pipelinesSchemaCopy is the copy of schema/pipelines.schema.json
	// generation writes beside the generated structs, for these tests only.
	pipelinesSchemaCopy = "pipelines.schema.json"
	pipelinesSchemaID   = "https://github.com/zssvaidar/kpv/schema/pipelines.schema.json"
)

type pipelinesFixtureFile struct {
	Lists     directoryFixtureSet `json:"lists"`
	Documents directoryFixtureSet `json:"documents"`
	Requests  directoryFixtureSet `json:"requests"`
	Errors    directoryFixtureSet `json:"errors"`
}

// pipelinesSchemaSet is every shape in the pipelines schema, compiled.
type pipelinesSchemaSet struct {
	list, document, request, failure *jsonschema.Schema
}

func pipelinesSchemas(t *testing.T) pipelinesSchemaSet {
	t.Helper()
	raw, err := os.ReadFile(pipelinesSchemaCopy)
	if err != nil {
		t.Fatalf("read the pipelines schema copy: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the pipelines schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(pipelinesSchemaID, document); err != nil {
		t.Fatalf("add the pipelines schema: %v", err)
	}
	compile := func(location string) *jsonschema.Schema {
		schema, err := compiler.Compile(pipelinesSchemaID + location)
		if err != nil {
			t.Fatalf("compile %s: %v", location, err)
		}
		return schema
	}
	return pipelinesSchemaSet{
		list:     compile(""),
		document: compile("#/$defs/pipelineDocument"),
		request:  compile("#/$defs/createPipelineRequest"),
		failure:  compile("#/$defs/pipelinesError"),
	}
}

func loadPipelinesFixtures(t *testing.T) pipelinesFixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(pipelinesFixtures))
	if err != nil {
		t.Fatalf("read the shared pipelines fixtures: %v", err)
	}
	var loaded pipelinesFixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared pipelines fixtures: %v", err)
	}
	for name, set := range map[string]directoryFixtureSet{
		"lists": loaded.Lists, "documents": loaded.Documents, "requests": loaded.Requests, "errors": loaded.Errors,
	} {
		if len(set.Valid) == 0 || len(set.Invalid) == 0 {
			t.Fatalf("the %s fixtures have %d valid and %d invalid cases, want some of each", name, len(set.Valid), len(set.Invalid))
		}
	}
	return loaded
}

// A valid fixture passes the schema, parses into the generated struct and is
// still valid after a round trip through it; an invalid one is refused. The
// pairings the schema states in words are held here too.
func TestThePipelinesFixturesHoldTheSchemaAndTheGeneratedStructs(t *testing.T) {
	t.Parallel()
	fixtures := loadPipelinesFixtures(t)
	schemas := pipelinesSchemas(t)
	for _, group := range []struct {
		name   string
		set    directoryFixtureSet
		schema *jsonschema.Schema
		into   func() any
	}{
		{"list", fixtures.Lists, schemas.list, func() any { return &PipelineList{} }},
		{"document", fixtures.Documents, schemas.document, func() any { return &PipelineDocument{} }},
		{"request", fixtures.Requests, schemas.request, func() any { return &CreatePipelineRequest{} }},
		{"error", fixtures.Errors, schemas.failure, func() any { return &PipelinesError{} }},
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
				assertPipelinesPairings(t, into)
			})
		}
		for _, fixture := range group.set.Invalid {
			if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err == nil {
				t.Errorf("%s %s should be invalid (%s), and the schema accepted it", group.name, fixture.Name, fixture.Why)
			}
		}
	}
}

// assertPipelinesPairings holds an answer to the pairings the schema states in
// its descriptions.
func assertPipelinesPairings(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case *PipelineList:
		for _, root := range typed.Roots {
			if root.Available == (root.Reason != nil) {
				t.Errorf("root %s is available=%v with reason %v", root.Path, root.Available, root.Reason)
			}
			if !root.Available && len(root.Pipelines) > 0 {
				t.Errorf("unavailable root %s lists %d pipelines", root.Path, len(root.Pipelines))
			}
			for _, entry := range root.Pipelines {
				if entry.Root != root.Path {
					t.Errorf("an entry of %s names root %s", root.Path, entry.Root)
				}
				if entry.Readable != (entry.Unreadable == nil) || entry.Readable != (entry.ID != nil) || entry.Readable != (entry.Language != nil) {
					t.Errorf("entry %v is readable=%v with id %v, language %v, unreadable %v", entry.Directory, entry.Readable, entry.ID, entry.Language, entry.Unreadable)
				}
				if !entry.Readable && entry.Name != nil {
					t.Errorf("unreadable entry %v has a name", entry.Directory)
				}
			}
		}
	case *PipelinesError:
		if (typed.Error == PipelinesErrorKindRootUnavailable) != (typed.Reason != nil) {
			t.Errorf("a %s error has reason %v", typed.Error, typed.Reason)
		}
		if (typed.Error == PipelinesErrorKindUnreadable) != (typed.Unreadable != nil) {
			t.Errorf("a %s error has unreadable %v", typed.Error, typed.Unreadable)
		}
		if typed.Problems != nil && typed.Error != PipelinesErrorKindInvalid {
			t.Errorf("a %s error has problems", typed.Error)
		}
	}
}
