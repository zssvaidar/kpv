// The Go half of the shared directory-browsing fixtures. The TypeScript half
// is packages/schema/src/directory-fixtures.test.ts and reads the same file, so
// the answer the agent sends and the answer the browser will parse are held to
// the same cases.

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
	// directoryFixtures is the one set of fixtures, outside the Go module
	// because it belongs to neither language.
	directoryFixtures = "../../../schema/fixtures/directory-responses.json"
	// directorySchemaCopy is the copy of schema/directory.schema.json that
	// generation writes beside the generated structs. Nothing in the agent
	// embeds it, because this package may not import embed; it is here for
	// these tests, and `pnpm run generate:check` keeps it the same bytes.
	directorySchemaCopy = "directory.schema.json"
	// directorySchemaID is the $id the schema declares.
	directorySchemaID = "https://github.com/zssvaidar/kpv/schema/directory.schema.json"
)

type directoryFixtureCase struct {
	Name string          `json:"name"`
	Why  string          `json:"why"`
	Body json.RawMessage `json:"body"`
}

type directoryFixtureSet struct {
	Valid   []directoryFixtureCase `json:"valid"`
	Invalid []directoryFixtureCase `json:"invalid"`
}

type directoryFixtureFile struct {
	Listings directoryFixtureSet `json:"listings"`
	Errors   directoryFixtureSet `json:"errors"`
}

func loadDirectoryFixtures(t *testing.T) directoryFixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(directoryFixtures))
	if err != nil {
		t.Fatalf("read the shared directory fixtures: %v", err)
	}
	var loaded directoryFixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared directory fixtures: %v", err)
	}
	for name, set := range map[string]directoryFixtureSet{"listings": loaded.Listings, "errors": loaded.Errors} {
		if len(set.Valid) == 0 || len(set.Invalid) == 0 {
			t.Fatalf("the %s fixtures have %d valid and %d invalid cases, want some of each", name, len(set.Valid), len(set.Invalid))
		}
	}
	return loaded
}

// directorySchemas compiles the listing, which is the schema's root, and the
// error, which is one of its definitions.
func directorySchemas(t *testing.T) (listing, failure *jsonschema.Schema) {
	t.Helper()
	raw, err := os.ReadFile(directorySchemaCopy)
	if err != nil {
		t.Fatalf("read the directory schema copy: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the directory schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(directorySchemaID, document); err != nil {
		t.Fatalf("add the directory schema: %v", err)
	}
	listing, err = compiler.Compile(directorySchemaID)
	if err != nil {
		t.Fatalf("compile the listing schema: %v", err)
	}
	failure, err = compiler.Compile(directorySchemaID + "#/$defs/directoryError")
	if err != nil {
		t.Fatalf("compile the error schema: %v", err)
	}
	return listing, failure
}

func decodeDocument(t *testing.T, raw []byte) any {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return document
}

// A valid fixture passes the schema, parses into the generated struct, and is
// still valid after a round trip through it, which is what catches a struct
// that cannot carry what the schema allows. An invalid one is refused.
func TestTheDirectoryFixturesHoldTheSchemaAndTheGeneratedStructs(t *testing.T) {
	t.Parallel()
	fixtures := loadDirectoryFixtures(t)
	listingSchema, errorSchema := directorySchemas(t)

	roundTrip := func(t *testing.T, raw []byte, into any) []byte {
		t.Helper()
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("does not parse into the generated struct: %v", err)
		}
		again, err := json.Marshal(into)
		if err != nil {
			t.Fatalf("does not marshal back: %v", err)
		}
		return again
	}

	for _, fixture := range fixtures.Listings.Valid {
		t.Run("listing/"+fixture.Name, func(t *testing.T) {
			if err := listingSchema.Validate(decodeDocument(t, fixture.Body)); err != nil {
				t.Fatalf("should be valid (%s): %v", fixture.Why, err)
			}
			again := roundTrip(t, fixture.Body, &DirectoryListing{})
			if err := listingSchema.Validate(decodeDocument(t, again)); err != nil {
				t.Fatalf("not valid after a round trip through the generated struct (%s): %v", again, err)
			}
		})
	}
	for _, fixture := range fixtures.Errors.Valid {
		t.Run("error/"+fixture.Name, func(t *testing.T) {
			if err := errorSchema.Validate(decodeDocument(t, fixture.Body)); err != nil {
				t.Fatalf("should be valid (%s): %v", fixture.Why, err)
			}
			again := roundTrip(t, fixture.Body, &DirectoryError{})
			if err := errorSchema.Validate(decodeDocument(t, again)); err != nil {
				t.Fatalf("not valid after a round trip through the generated struct (%s): %v", again, err)
			}
		})
	}
	for _, fixture := range fixtures.Listings.Invalid {
		if err := listingSchema.Validate(decodeDocument(t, fixture.Body)); err == nil {
			t.Errorf("listing %s should be invalid (%s), and the schema accepted it", fixture.Name, fixture.Why)
		}
	}
	for _, fixture := range fixtures.Errors.Invalid {
		if err := errorSchema.Validate(decodeDocument(t, fixture.Body)); err == nil {
			t.Errorf("error %s should be invalid (%s), and the schema accepted it", fixture.Name, fixture.Why)
		}
	}
}

// Every message the endpoint can answer with is one the schema accepts, so a
// fixed sentence cannot drift out of the contract.
func TestEveryDirectoryErrorTheEndpointSendsIsValid(t *testing.T) {
	t.Parallel()
	_, errorSchema := directorySchemas(t)
	for kind, failure := range directoryFailures {
		body, err := json.Marshal(DirectoryError{Error: kind, Message: failure.message})
		if err != nil {
			t.Fatalf("marshal the %s error: %v", kind, err)
		}
		if err := errorSchema.Validate(decodeDocument(t, body)); err != nil {
			t.Errorf("the %s error is not valid: %v", kind, err)
		}
	}
	if len(directoryFailures) != 6 {
		t.Errorf("there are %d error kinds with an answer, want the schema's 6", len(directoryFailures))
	}
}
