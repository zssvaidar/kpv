// The Go half of the shared connection endpoint fixtures (#24). The
// TypeScript half is packages/schema/src/connection-fixtures.test.ts and
// reads the same file, so what the agent answers and what the browser parses
// are held to the same cases. The records and their combination rules are
// internal/connections/fixtures_test.go's.

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
	connectionsFixtures   = "../../../schema/fixtures/connections.json"
	connectionsSchemaCopy = "connection.schema.json"
	connectionsSchemaID   = "https://github.com/zssvaidar/kpv/schema/connection.schema.json"
)

type connectionsFixtureFile struct {
	Responses struct {
		Lists   directoryFixtureSet `json:"lists"`
		Names   directoryFixtureSet `json:"names"`
		Updates directoryFixtureSet `json:"updates"`
		Deletes directoryFixtureSet `json:"deletes"`
		Errors  directoryFixtureSet `json:"errors"`
	} `json:"responses"`
}

type connectionsSchemaSet struct {
	list, names, summary, update, remove, failure *jsonschema.Schema
}

func connectionsSchemas(t *testing.T) connectionsSchemaSet {
	t.Helper()
	raw, err := os.ReadFile(connectionsSchemaCopy)
	if err != nil {
		t.Fatalf("read the connection schema copy: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the connection schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(connectionsSchemaID, document); err != nil {
		t.Fatalf("add the connection schema: %v", err)
	}
	compile := func(location string) *jsonschema.Schema {
		schema, err := compiler.Compile(connectionsSchemaID + location)
		if err != nil {
			t.Fatalf("compile %s: %v", location, err)
		}
		return schema
	}
	return connectionsSchemaSet{
		list:    compile(""),
		names:   compile("#/$defs/connectionNameList"),
		summary: compile("#/$defs/connectionSummary"),
		update:  compile("#/$defs/connectionUpdate"),
		remove:  compile("#/$defs/connectionDelete"),
		failure: compile("#/$defs/connectionsError"),
	}
}

func TestTheConnectionFixturesHoldTheSchemaAndTheGeneratedStructs(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Clean(connectionsFixtures))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures connectionsFixtureFile
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	schemas := connectionsSchemas(t)
	responses := fixtures.Responses
	for _, group := range []struct {
		name   string
		set    directoryFixtureSet
		schema *jsonschema.Schema
		into   func() any
	}{
		{"list", responses.Lists, schemas.list, func() any { return &ConnectionList{} }},
		{"names", responses.Names, schemas.names, func() any { return &ConnectionNameList{} }},
		{"update", responses.Updates, schemas.update, func() any { return &map[string]any{} }},
		{"delete", responses.Deletes, schemas.remove, func() any { return &ConnectionDelete{} }},
		{"error", responses.Errors, schemas.failure, func() any { return &ConnectionsError{} }},
	} {
		if len(group.set.Valid) == 0 || len(group.set.Invalid) == 0 {
			t.Fatalf("the %s fixtures need valid and invalid cases", group.name)
		}
		for _, fixture := range group.set.Valid {
			if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err != nil {
				t.Errorf("%s/%s should be valid (%s): %v", group.name, fixture.Name, fixture.Why, err)
				continue
			}
			into := group.into()
			if err := json.Unmarshal(fixture.Body, into); err != nil {
				t.Errorf("%s/%s does not parse into the generated struct: %v", group.name, fixture.Name, err)
				continue
			}
			again, err := json.Marshal(into)
			if err != nil {
				t.Fatal(err)
			}
			if err := group.schema.Validate(decodeDocument(t, again)); err != nil {
				t.Errorf("%s/%s is not valid after a round trip: %v", group.name, fixture.Name, err)
			}
		}
		for _, fixture := range group.set.Invalid {
			if err := group.schema.Validate(decodeDocument(t, fixture.Body)); err == nil {
				t.Errorf("%s/%s should be invalid (%s)", group.name, fixture.Name, fixture.Why)
			}
		}
	}
}
