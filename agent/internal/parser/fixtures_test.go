// The Go half of the shared parser contract fixtures (#37). The TypeScript
// half is packages/schema/src/parser-fixtures.test.ts and reads the same
// file, so what a parser may answer is held to the same cases on both sides.

package parser_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/zssvaidar/kpv/agent/internal/parser"
)

const sharedFixtures = "../../../schema/fixtures/parser-results.json"

type fixtureCase struct {
	Name   string          `json:"name"`
	Why    string          `json:"why"`
	Result json.RawMessage `json:"result"`
}

type fixtureFile struct {
	Valid   []fixtureCase `json:"valid"`
	Invalid []fixtureCase `json:"invalid"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedFixtures))
	if err != nil {
		t.Fatalf("read the shared parser fixtures: %v", err)
	}
	var loaded fixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared parser fixtures: %v", err)
	}
	if len(loaded.Valid) == 0 || len(loaded.Invalid) == 0 {
		t.Fatalf("the fixtures have %d valid and %d invalid cases, want some of each", len(loaded.Valid), len(loaded.Invalid))
	}
	return loaded
}

func compiled(t *testing.T) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(parser.SchemaJSON()))
	if err != nil {
		t.Fatalf("decode the embedded parser schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const id = "https://github.com/zssvaidar/kpv/schema/parser.schema.json"
	if err := compiler.AddResource(id, document); err != nil {
		t.Fatalf("add the embedded parser schema: %v", err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatalf("compile the embedded parser schema: %v", err)
	}
	return schema
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode a fixture: %v", err)
	}
	return document
}

// The source a valid fixture's ranges are checked against: long enough for
// every range in the fixtures, and ASCII so every offset is a boundary.
var fixtureSource = bytes.Repeat([]byte("a"), 200)

// A valid fixture passes the schema, round-trips through the generated
// struct, and passes the harness's own validation against a source its
// ranges fit, so the schema-level valid set is also a set of answers a
// parser may give.
func TestValidFixturesPassTheSchemaAndTheHarness(t *testing.T) {
	t.Parallel()
	schema := compiled(t)
	for _, fixture := range loadFixtures(t).Valid {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(decode(t, fixture.Result)); err != nil {
				t.Fatalf("%s should be valid (%s): %v", fixture.Name, fixture.Why, err)
			}
			var typed parser.ParserResult
			if err := json.Unmarshal(fixture.Result, &typed); err != nil {
				t.Fatalf("%s does not parse into the generated struct: %v", fixture.Name, err)
			}
			again, err := json.Marshal(typed)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := schema.Validate(decode(t, again)); err != nil {
				t.Fatalf("%s is not valid after a round trip through the generated struct (%s): %v", fixture.Name, again, err)
			}
			source := fixtureSource
			if fixture.Name == "empty" {
				source = nil
			}
			if _, problems := parser.Validate(source, fixture.Result); len(problems) != 0 {
				t.Fatalf("the harness refuses %s: %v", fixture.Name, problems)
			}
		})
	}
}

func TestInvalidFixturesFailTheSchemaAndTheHarness(t *testing.T) {
	t.Parallel()
	schema := compiled(t)
	for _, fixture := range loadFixtures(t).Invalid {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(decode(t, fixture.Result)); err == nil {
				t.Fatalf("%s should be invalid (%s)", fixture.Name, fixture.Why)
			}
			result, problems := parser.Validate(fixtureSource, fixture.Result)
			if result != nil || len(problems) == 0 {
				t.Fatalf("the harness accepts %s", fixture.Name)
			}
		})
	}
}
