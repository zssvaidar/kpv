// The Go half of the shared project root fixtures (#63). The TypeScript half
// is packages/schema/src/roots-fixtures.test.ts and reads the same file, so
// what the agent sends and takes and what the browser parses and sends are
// held to the same cases.

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
	// rootsFixtures is the one set of fixtures, outside the Go module because
	// it belongs to neither language.
	rootsFixtures = "../../../schema/fixtures/roots-responses.json"
	// rootsSchemaCopy is the copy of schema/roots.schema.json generation
	// writes beside the generated structs, for these tests only: this package
	// may not import embed.
	rootsSchemaCopy = "roots.schema.json"
	rootsSchemaID   = "https://github.com/zssvaidar/kpv/schema/roots.schema.json"
)

type rootsFixtureFile struct {
	Lists      directoryFixtureSet `json:"lists"`
	Requests   directoryFixtureSet `json:"requests"`
	Authorized directoryFixtureSet `json:"authorized"`
	Errors     directoryFixtureSet `json:"errors"`
}

// rootsSchemaSet is every shape in the roots schema, compiled.
type rootsSchemaSet struct {
	list, request, authorized, failure *jsonschema.Schema
}

func rootsSchemas(t *testing.T) rootsSchemaSet {
	t.Helper()
	raw, err := os.ReadFile(rootsSchemaCopy)
	if err != nil {
		t.Fatalf("read the roots schema copy: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the roots schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(rootsSchemaID, document); err != nil {
		t.Fatalf("add the roots schema: %v", err)
	}
	compile := func(location string) *jsonschema.Schema {
		schema, err := compiler.Compile(rootsSchemaID + location)
		if err != nil {
			t.Fatalf("compile %s: %v", location, err)
		}
		return schema
	}
	return rootsSchemaSet{
		list:       compile(""),
		request:    compile("#/$defs/rootRequest"),
		authorized: compile("#/$defs/authorizedRoot"),
		failure:    compile("#/$defs/rootsError"),
	}
}

func loadRootsFixtures(t *testing.T) rootsFixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(rootsFixtures))
	if err != nil {
		t.Fatalf("read the shared roots fixtures: %v", err)
	}
	var loaded rootsFixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared roots fixtures: %v", err)
	}
	for name, set := range map[string]directoryFixtureSet{
		"lists": loaded.Lists, "requests": loaded.Requests, "authorized": loaded.Authorized, "errors": loaded.Errors,
	} {
		if len(set.Valid) == 0 || len(set.Invalid) == 0 {
			t.Fatalf("the %s fixtures have %d valid and %d invalid cases, want some of each", name, len(set.Valid), len(set.Invalid))
		}
	}
	return loaded
}

// A valid fixture passes the schema, parses into the generated struct and is
// still valid after a round trip through it; an invalid one is refused. The
// two pairings the schema states in words -- a reason exactly when a root is
// unavailable, a root exactly when the error is overlaps -- are held here.
func TestTheRootsFixturesHoldTheSchemaAndTheGeneratedStructs(t *testing.T) {
	t.Parallel()
	fixtures := loadRootsFixtures(t)
	schemas := rootsSchemas(t)
	for _, group := range []struct {
		name   string
		set    directoryFixtureSet
		schema *jsonschema.Schema
		into   func() any
	}{
		{"list", fixtures.Lists, schemas.list, func() any { return &RootList{} }},
		{"request", fixtures.Requests, schemas.request, func() any { return &RootRequest{} }},
		{"authorized", fixtures.Authorized, schemas.authorized, func() any { return &AuthorizedRoot{} }},
		{"error", fixtures.Errors, schemas.failure, func() any { return &RootsError{} }},
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
				assertRootsPairings(t, into)
			})
		}
		for _, fixture := range group.set.Invalid {
			if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err == nil {
				t.Errorf("%s %s should be invalid (%s), and the schema accepted it", group.name, fixture.Name, fixture.Why)
			}
		}
	}
}

// assertRootsPairings holds an answer to the two pairings the schema states
// in its descriptions.
func assertRootsPairings(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case *RootList:
		for _, root := range typed.Roots {
			if root.Available == (root.Reason != nil) {
				t.Errorf("root %s is available=%v with reason %v", root.Path, root.Available, root.Reason)
			}
		}
	case *RootsError:
		if (typed.Error == RootsErrorKindOverlaps) != (typed.Root != nil) {
			t.Errorf("a %s error has root %v", typed.Error, typed.Root)
		}
	}
}

// Every message the endpoints can answer with is one the schema accepts.
func TestEveryRootsErrorTheEndpointsSendIsValid(t *testing.T) {
	t.Parallel()
	schemas := rootsSchemas(t)
	root := "/home/user/code/app"
	for kind, failure := range rootsFailures {
		answer := RootsError{Error: kind, Message: failure.message}
		if kind == RootsErrorKindOverlaps {
			answer.Root = &root
		}
		body, err := json.Marshal(answer)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := schemas.failure.Validate(decodeDocument(t, body)); err != nil {
			t.Errorf("the %s error is not valid: %v", kind, err)
		}
	}
	if len(rootsFailures) != len(enumValues_RootsErrorKind) {
		t.Errorf("there are %d error kinds with an answer, want the schema's %d", len(rootsFailures), len(enumValues_RootsErrorKind))
	}
}
