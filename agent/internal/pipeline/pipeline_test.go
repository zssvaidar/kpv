package pipeline_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

// authoritativeSchema is the one JSON Schema. The copy this package embeds is
// generated from it, because a Go module cannot embed a file outside itself.
const authoritativeSchema = "../../../schema/pipeline.schema.json"

func TestEmbeddedSchemaIsTheAuthoritativeFileVerbatim(t *testing.T) {
	want, err := os.ReadFile(filepath.Clean(authoritativeSchema))
	if err != nil {
		t.Fatalf("read authoritative schema: %v", err)
	}
	if got := pipeline.SchemaJSON(); string(got) != string(want) {
		t.Error("the embedded schema differs from schema/pipeline.schema.json; run `pnpm run generate`")
	}
}

func TestTypedStepsGivesEachBlockItsOwnType(t *testing.T) {
	fixture := findValidFixture(t, "every-block-type")

	_, steps, err := pipeline.Parse(fixture)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("decoded %d steps, want 4", len(steps))
	}

	filter, ok := steps[0].(*pipeline.FilterStep)
	if !ok {
		t.Fatalf("step 0 is %T, want *pipeline.FilterStep", steps[0])
	}
	if filter.Configuration.Expression == "" {
		t.Error("the filter step decoded without its expression")
	}
	if filter.CustomCodeRef() != nil {
		t.Error("the filter step has no custom-code reference, so it should read as nil")
	}

	transform, ok := steps[1].(*pipeline.TransformStep)
	if !ok {
		t.Fatalf("step 1 is %T, want *pipeline.TransformStep", steps[1])
	}
	if len(transform.Configuration.Mappings) != 2 {
		t.Errorf("the transform step decoded %d mappings, want 2", len(transform.Configuration.Mappings))
	}

	validate, ok := steps[2].(*pipeline.ValidateStep)
	if !ok {
		t.Fatalf("step 2 is %T, want *pipeline.ValidateStep", steps[2])
	}
	if validate.Configuration.OnInvalid != pipeline.ValidateStepConfigurationOnInvalidDeadLetter {
		t.Errorf("the validate step decoded onInvalid as %q", validate.Configuration.OnInvalid)
	}

	custom, ok := steps[3].(*pipeline.CustomCodeStep)
	if !ok {
		t.Fatalf("step 3 is %T, want *pipeline.CustomCodeStep", steps[3])
	}
	if custom.CustomCodeRef() == nil {
		t.Fatal("the custom-code step lost its custom-code reference")
	}
	if custom.CustomCodeRef().Path != "src/discounts.go" {
		t.Errorf("the custom-code reference decoded as %q", custom.CustomCodeRef().Path)
	}
}

func TestTypedStepsRefusesAnUnknownBlockType(t *testing.T) {
	// Reaching TypedSteps with an unknown type means validation was skipped,
	// so this is the backstop: no half-built step comes out of it either.
	unvalidated := &pipeline.Pipeline{
		ProcessingSteps: []pipeline.ProcessingStep{
			map[string]any{
				"id":            "aggregate-hourly",
				"type":          "aggregate",
				"configuration": map[string]any{"window": "1h"},
			},
		},
	}

	steps, err := pipeline.TypedSteps(unvalidated)
	if err == nil {
		t.Fatalf("TypedSteps accepted an unknown block type and returned %d steps", len(steps))
	}
	if !strings.Contains(err.Error(), "aggregate") {
		t.Errorf("the error should name the type it does not know, got %q", err)
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, _, err := pipeline.Parse([]byte("{not json")); err == nil {
		t.Error("Parse accepted malformed JSON")
	}
}

func findValidFixture(t *testing.T, name string) []byte {
	t.Helper()
	for _, fixture := range loadFixtures(t).Valid {
		if fixture.Name == name {
			return fixture.Pipeline
		}
	}
	t.Fatalf("no valid fixture named %q", name)
	return nil
}
