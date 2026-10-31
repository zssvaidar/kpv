// The Go half of the shared connection record fixtures (#24). The
// TypeScript half is packages/schema/src/connection-fixtures.test.ts and
// reads the same file, so the combination rules, implemented in rules.go and
// in connection-rules.ts, give the same answers.

package connections

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const sharedFixtures = "../../../schema/fixtures/connections.json"

type expectedProblem struct {
	InstanceLocation string `json:"instanceLocation"`
	KeywordLocation  string `json:"keywordLocation"`
	Rule             string `json:"rule"`
}

type fixtureFile struct {
	Records struct {
		Valid []struct {
			Name       string          `json:"name"`
			Connection json.RawMessage `json:"connection"`
		} `json:"valid"`
		Invalid []struct {
			Name       string            `json:"name"`
			Errors     []expectedProblem `json:"errors"`
			Connection json.RawMessage   `json:"connection"`
		} `json:"invalid"`
	} `json:"records"`
	Names struct {
		Valid   []string `json:"valid"`
		Invalid []string `json:"invalid"`
	} `json:"names"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedFixtures))
	if err != nil {
		t.Fatalf("read the shared connection fixtures: %v", err)
	}
	var loaded fixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared connection fixtures: %v", err)
	}
	if len(loaded.Records.Valid) == 0 || len(loaded.Records.Invalid) == 0 || len(loaded.Names.Valid) == 0 {
		t.Fatal("the shared connection fixtures are empty")
	}
	return loaded
}

func decodeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return value
}

func TestValidConnectionRecordsPassTheSchemaAndTheRules(t *testing.T) {
	t.Parallel()
	for _, fixture := range loadFixtures(t).Records.Valid {
		problems, err := ValidateRecord(decodeJSON(t, fixture.Connection))
		if err != nil {
			t.Fatal(err)
		}
		if len(problems) != 0 {
			t.Errorf("%s: %+v", fixture.Name, problems)
		}
	}
}

func TestInvalidConnectionRecordsFailWhereTheFixtureSays(t *testing.T) {
	t.Parallel()
	for _, fixture := range loadFixtures(t).Records.Invalid {
		problems, err := ValidateRecord(decodeJSON(t, fixture.Connection))
		if err != nil {
			t.Fatal(err)
		}
		if len(fixture.Errors) == 0 {
			t.Fatalf("%s names no failure", fixture.Name)
		}
		for _, want := range fixture.Errors {
			found := false
			for _, problem := range problems {
				if problem.Pointer == want.InstanceLocation &&
					(want.Rule == "" || problem.Rule == want.Rule) &&
					(want.KeywordLocation == "" || problem.Keyword == want.KeywordLocation) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no failure %+v in %+v", fixture.Name, want, problems)
			}
		}
	}
}

// The same names are valid in the pipeline schema's kafkaConnectionName and
// the connection schema's connectionName, which generation also holds to the
// same rule.
func TestANameIsValidInOneSchemaExactlyWhenItIsInTheOther(t *testing.T) {
	t.Parallel()
	compile := func(file, id, definition string) *jsonschema.Schema {
		raw, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource(id, decodeJSON(t, raw)); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile(id + "#/$defs/" + definition)
		if err != nil {
			t.Fatal(err)
		}
		return schema
	}
	inPipeline := compile("../pipeline/pipeline.schema.json", "https://github.com/zssvaidar/kpv/schema/pipeline.schema.json", "kafkaConnectionName")
	inConnection := compile("connection.schema.json", schemaID, "connectionName")
	fixtures := loadFixtures(t)
	for _, name := range fixtures.Names.Valid {
		if inPipeline.Validate(name) != nil || inConnection.Validate(name) != nil {
			t.Errorf("%q should be a valid name in both schemas", name)
		}
	}
	for _, name := range fixtures.Names.Invalid {
		if inPipeline.Validate(name) == nil || inConnection.Validate(name) == nil {
			t.Errorf("%q should be an invalid name in both schemas", name)
		}
	}
}

func TestARuleMessageNamesTheConflict(t *testing.T) {
	t.Parallel()
	problems, err := ValidateRecord(map[string]any{
		"name": "a", "bootstrapServers": []any{"b:1"}, "securityProtocol": "SSL", "saslMechanism": "PLAIN",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Rule != RuleSASLNotAllowed ||
		!bytes.Contains([]byte(problems[0].Message), []byte("SSL")) || !bytes.Contains([]byte(problems[0].Message), []byte("SASL")) {
		t.Fatalf("got %+v", problems)
	}
}

// No message about a field quotes it: a pattern or enum refusal of a
// certificate-sized string, or of a value that could be a typed password,
// says what is wrong without repeating it.
func TestASchemaMessageNeverQuotesTheValue(t *testing.T) {
	t.Parallel()
	const marker = "QUOTE-ME-NOT-7d1e"
	problems, err := ValidateRecord(map[string]any{
		"name": marker, "bootstrapServers": []any{marker}, "securityProtocol": marker, "saslMechanism": marker,
		marker: marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) == 0 {
		t.Fatal("expected problems")
	}
	for _, problem := range problems {
		if bytes.Contains([]byte(problem.Message), []byte(marker)) {
			t.Errorf("%s quotes the value: %s", problem.Pointer, problem.Message)
		}
	}
}
