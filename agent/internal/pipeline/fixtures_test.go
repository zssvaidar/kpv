// The Go half of the shared fixtures. The TypeScript half is
// packages/schema/src/fixtures.test.ts and reads the same file, so a fixture
// only has to be written once and both sides are held to it.

package pipeline_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

// sharedFixtures is the one set of fixtures, outside the Go module because it
// belongs to neither language.
const sharedFixtures = "../../../schema/fixtures/pipelines.json"

type expectedError struct {
	InstanceLocation string `json:"instanceLocation"`
	KeywordLocation  string `json:"keywordLocation"`
}

type fixtureFile struct {
	Valid []struct {
		Name     string          `json:"name"`
		Why      string          `json:"why"`
		Pipeline json.RawMessage `json:"pipeline"`
	} `json:"valid"`
	Invalid []struct {
		Name     string          `json:"name"`
		Why      string          `json:"why"`
		Errors   []expectedError `json:"errors"`
		Pipeline json.RawMessage `json:"pipeline"`
	} `json:"invalid"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(sharedFixtures))
	if err != nil {
		t.Fatalf("read shared fixtures: %v", err)
	}
	var loaded fixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode shared fixtures: %v", err)
	}
	if len(loaded.Valid) == 0 || len(loaded.Invalid) == 0 {
		t.Fatalf("shared fixtures have %d valid and %d invalid cases, want some of each",
			len(loaded.Valid), len(loaded.Invalid))
	}
	return loaded
}

func decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return document
}

func TestValidFixturesValidateAndParse(t *testing.T) {
	for _, fixture := range loadFixtures(t).Valid {
		t.Run(fixture.Name, func(t *testing.T) {
			problems, err := pipeline.Validate(decode(t, fixture.Pipeline))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(problems) > 0 {
				t.Fatalf("%s should be valid (%s), got %v", fixture.Name, fixture.Why, problems)
			}

			parsed, steps, err := pipeline.Parse(fixture.Pipeline)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if parsed.ID == "" {
				t.Error("parsed pipeline has no id")
			}
			if len(steps) != len(parsed.ProcessingSteps) {
				t.Errorf("decoded %d steps, want %d", len(steps), len(parsed.ProcessingSteps))
			}
			for index, step := range steps {
				if step.StepID() == "" {
					t.Errorf("step %d has no id", index)
				}
				if step.StepType() == "" {
					t.Errorf("step %d has no type", index)
				}
			}
		})
	}
}

func TestInvalidFixturesFailAtTheSchemaPathTheyName(t *testing.T) {
	for _, fixture := range loadFixtures(t).Invalid {
		t.Run(fixture.Name, func(t *testing.T) {
			problems, err := pipeline.Validate(decode(t, fixture.Pipeline))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(problems) == 0 {
				t.Fatalf("%s should be invalid (%s), got no errors", fixture.Name, fixture.Why)
			}
			if len(fixture.Errors) == 0 {
				t.Fatal("an invalid fixture has to name the failures it expects")
			}

			for _, want := range fixture.Errors {
				if !reports(problems, want) {
					t.Errorf("no failure at instance %q, schema %q; got %v",
						want.InstanceLocation, want.KeywordLocation, problems)
				}
			}

			if _, _, err := pipeline.Parse(fixture.Pipeline); err == nil {
				t.Error("Parse accepted an invalid pipeline")
			}
		})
	}
}

func reports(problems pipeline.ValidationErrors, want expectedError) bool {
	for _, problem := range problems {
		if problem.InstanceLocation == want.InstanceLocation &&
			problem.KeywordLocation == want.KeywordLocation {
			return true
		}
	}
	return false
}
