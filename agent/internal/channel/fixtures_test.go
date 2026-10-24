// The Go half of the shared envelope fixtures. The TypeScript half is
// packages/schema/src/channel-fixtures.test.ts and reads the same file, so a
// fixture only has to be written once and both sides are held to it.
//
// What this proves that a hand-written test cannot: the envelope really is
// defined once. The schema compiled here is the embedded copy, the structs are
// generated from the same file, and both are checked against the same cases
// the TypeScript side checks. A generator whose output drifted from the schema
// fails here rather than at the first message a browser cannot read.

package channel_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/zssvaidar/kpv/agent/internal/channel"
)

// sharedFixtures is the one set of fixtures, outside the Go module because it
// belongs to neither language.
const sharedFixtures = "../../../schema/fixtures/channel-messages.json"

// schemaID is the $id the envelope schema declares, used as its compilation
// URL.
const schemaID = "https://github.com/zssvaidar/kpv/schema/channel.schema.json"

type fixtureFile struct {
	Valid []struct {
		Name    string          `json:"name"`
		Why     string          `json:"why"`
		Message json.RawMessage `json:"message"`
	} `json:"valid"`
	Invalid []struct {
		Name    string          `json:"name"`
		Why     string          `json:"why"`
		Message json.RawMessage `json:"message"`
	} `json:"invalid"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedFixtures))
	if err != nil {
		t.Fatalf("read the shared envelope fixtures: %v", err)
	}
	var loaded fixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared envelope fixtures: %v", err)
	}
	if len(loaded.Valid) == 0 || len(loaded.Invalid) == 0 {
		t.Fatalf("the fixtures have %d valid and %d invalid cases, want some of each", len(loaded.Valid), len(loaded.Invalid))
	}
	return loaded
}

// compiled is the embedded schema, compiled. The embedded copy is what the
// agent ships, so it is what is tested; `pnpm run generate:check` is what
// keeps it the same bytes as schema/channel.schema.json.
func compiled(t *testing.T) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(channel.SchemaJSON()))
	if err != nil {
		t.Fatalf("decode the embedded envelope schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		t.Fatalf("add the embedded envelope schema: %v", err)
	}
	schema, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatalf("compile the embedded envelope schema: %v", err)
	}
	return schema
}

func decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode a fixture: %v", err)
	}
	return document
}

// A valid fixture passes the schema, parses into the generated struct, and is
// still valid after a round trip through it. The last part is the one that
// catches a generated struct that cannot carry what the schema allows: a
// payload that loses its nesting, a field that marshals under another name.
func TestValidFixturesValidateAndRoundTripThroughTheGeneratedStruct(t *testing.T) {
	t.Parallel()
	schema := compiled(t)
	for _, fixture := range loadFixtures(t).Valid {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(decode(t, fixture.Message)); err != nil {
				t.Fatalf("%s should be valid (%s): %v", fixture.Name, fixture.Why, err)
			}

			var message channel.ChannelMessage
			if err := json.Unmarshal(fixture.Message, &message); err != nil {
				t.Fatalf("%s does not parse into the generated struct: %v", fixture.Name, err)
			}
			if message.Kind == "" || message.PipelineID == "" {
				t.Fatalf("%s parsed into an envelope with no kind or no pipeline id: %+v", fixture.Name, message)
			}

			again, err := json.Marshal(message)
			if err != nil {
				t.Fatalf("%s does not marshal back: %v", fixture.Name, err)
			}
			if err := schema.Validate(decode(t, again)); err != nil {
				t.Fatalf("%s is not valid after a round trip through the generated struct (%s): %v", fixture.Name, again, err)
			}
		})
	}
}

// An invalid fixture is refused by the schema, which is the contract the
// TypeScript side is held to as well.
func TestInvalidFixturesAreRefusedByTheSchema(t *testing.T) {
	t.Parallel()
	schema := compiled(t)
	for _, fixture := range loadFixtures(t).Invalid {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(decode(t, fixture.Message)); err == nil {
				t.Fatalf("%s should be invalid (%s), and the schema accepted it", fixture.Name, fixture.Why)
			}
		})
	}
}

// The generated struct refuses what it can. It is not the schema and does not
// claim to be -- it says nothing about a property the envelope does not have,
// for one -- so this asserts the overlap and names what is outside it.
func TestTheGeneratedStructRefusesWhatItCan(t *testing.T) {
	t.Parallel()
	// What the generated unmarshaller is expected to refuse: a required
	// property that is missing, a kind outside the enum, a pipeline id that
	// is not an identifier. Everything else in the invalid fixtures is the
	// schema's to refuse.
	refused := map[string]bool{
		"no-kind":                               true,
		"a-kind-that-does-not-exist":            true,
		"no-pipeline-id":                        true,
		"a-pipeline-id-no-pipeline-could-have":  true,
		"an-empty-pipeline-id":                  true,
		"no-payload":                            true,
		"a-payload-that-is-not-an-object":       true,
		"a-property-the-envelope-does-not-have": false,
	}
	for _, fixture := range loadFixtures(t).Invalid {
		want, known := refused[fixture.Name]
		if !known {
			t.Errorf("the fixture %s is not listed here, so nobody has said whether the generated struct refuses it", fixture.Name)
			continue
		}
		var message channel.ChannelMessage
		err := json.Unmarshal(fixture.Message, &message)
		if want && err == nil {
			t.Errorf("the generated struct accepted %s (%s)", fixture.Name, fixture.Why)
		}
		if !want && err != nil {
			t.Errorf("the generated struct refused %s, which this test says is the schema's job alone; update the list: %v", fixture.Name, err)
		}
	}
}
