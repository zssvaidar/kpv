// The Go half of the shared working-copy endpoint fixtures (#43). The
// TypeScript half is packages/schema/src/workcopy-fixtures.test.ts and reads
// the same file, so what the agent sends and takes and what the browser
// parses and sends are held to the same cases.

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
	workcopyFixtures   = "../../../schema/fixtures/workcopy-responses.json"
	workcopySchemaCopy = "workcopy.schema.json"
	workcopySchemaID   = "https://github.com/zssvaidar/kpv/schema/workcopy.schema.json"
)

type workcopyFixtureFile struct {
	Lists             directoryFixtureSet `json:"lists"`
	Reviews           directoryFixtureSet `json:"reviews"`
	RevisionRequests  directoryFixtureSet `json:"revisionRequests"`
	SnapshotRequests  directoryFixtureSet `json:"snapshotRequests"`
	Reports           directoryFixtureSet `json:"reports"`
	Discarded         directoryFixtureSet `json:"discarded"`
	Removed           directoryFixtureSet `json:"removed"`
	Errors            directoryFixtureSet `json:"errors"`
	PairingViolations struct {
		Lists   []directoryFixtureCase `json:"lists"`
		Reviews []directoryFixtureCase `json:"reviews"`
		Reports []directoryFixtureCase `json:"reports"`
		Errors  []directoryFixtureCase `json:"errors"`
	} `json:"pairingViolations"`
}

// workcopySchema compiles one definition of the working-copy schema, or the
// root for "".
func workcopySchema(t *testing.T, definition string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(workcopySchemaCopy)
	if err != nil {
		t.Fatalf("read the working-copy schema copy: %v", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode the working-copy schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(workcopySchemaID, document); err != nil {
		t.Fatal(err)
	}
	location := ""
	if definition != "" {
		location = "#/$defs/" + definition
	}
	schema, err := compiler.Compile(workcopySchemaID + location)
	if err != nil {
		t.Fatalf("compile %s: %v", location, err)
	}
	return schema
}

func loadWorkcopyFixtures(t *testing.T) workcopyFixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(workcopyFixtures))
	if err != nil {
		t.Fatalf("read the shared working-copy fixtures: %v", err)
	}
	var loaded workcopyFixtureFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("decode the shared working-copy fixtures: %v", err)
	}
	return loaded
}

// workcopyPairingsHold reports whether a value keeps the pairings the schema
// states in words.
func workcopyPairingsHold(value any) bool {
	switch typed := value.(type) {
	case *WorkcopyList:
		for _, copy := range typed.Copies {
			if copy.Available != (copy.Reason == nil) ||
				(copy.Reason != nil && *copy.Reason == WorkcopyErrorKindRootUnavailable) != (copy.RootReason != nil) {
				return false
			}
		}
	case *WorkcopyReview:
		if (typed.Revision != nil) != (len(typed.Files) > 0) {
			return false
		}
		for _, file := range typed.Files {
			if file.State == WorkcopyStateReady {
				if (file.BaseText != nil) != (file.Change != WorkcopyChangeAdded) || (file.StagedText != nil) != (file.Change != WorkcopyChangeDeleted) {
					return false
				}
			} else if file.BaseText != nil || file.StagedText != nil {
				return false
			}
		}
	case *WorkcopyReport:
		complete := true
		for _, file := range typed.Files {
			if file.Result == WorkcopyResultFailed || file.Result == WorkcopyResultNotWritten {
				complete = false
			}
			if (file.Result == WorkcopyResultFailed) != (file.Kind != nil) {
				return false
			}
		}
		return complete == typed.Complete
	case *WorkcopyError:
		if (typed.Error == WorkcopyErrorKindRootUnavailable) != (typed.Reason != nil) || (typed.Error == WorkcopyErrorKindDrift) != (typed.Drift != nil) {
			return false
		}
		for _, drift := range typed.Drift {
			if drift.Now == WorkcopyStateReady {
				return false
			}
		}
	}
	return true
}

func TestTheWorkcopyFixturesHoldTheSchemaAndTheGeneratedStructs(t *testing.T) {
	t.Parallel()
	fixtures := loadWorkcopyFixtures(t)
	groups := []struct {
		name       string
		set        directoryFixtureSet
		definition string
		into       func() any
		violations []directoryFixtureCase
	}{
		{"list", fixtures.Lists, "", func() any { return &WorkcopyList{} }, fixtures.PairingViolations.Lists},
		{"review", fixtures.Reviews, "workcopyReview", func() any { return &WorkcopyReview{} }, fixtures.PairingViolations.Reviews},
		{"revision request", fixtures.RevisionRequests, "workcopyRevisionRequest", func() any { return &WorkcopyRevisionRequest{} }, nil},
		{"snapshot request", fixtures.SnapshotRequests, "workcopySnapshotRequest", func() any { return &WorkcopySnapshotRequest{} }, nil},
		{"report", fixtures.Reports, "workcopyReport", func() any { return &WorkcopyReport{} }, fixtures.PairingViolations.Reports},
		{"discarded", fixtures.Discarded, "workcopyDiscarded", func() any { return &WorkcopyDiscarded{} }, nil},
		{"removed", fixtures.Removed, "workcopySnapshotRemoved", func() any { return &WorkcopySnapshotRemoved{} }, nil},
		{"error", fixtures.Errors, "workcopyError", func() any { return &WorkcopyError{} }, fixtures.PairingViolations.Errors},
	}
	for _, group := range groups {
		if len(group.set.Valid) == 0 || len(group.set.Invalid) == 0 {
			t.Fatalf("the %s fixtures need valid and invalid cases", group.name)
		}
		schema := workcopySchema(t, group.definition)
		for _, fixture := range group.set.Valid {
			if err := schema.Validate(decodeDocument(t, fixture.Body)); err != nil {
				t.Errorf("%s %s should be valid (%s): %v", group.name, fixture.Name, fixture.Why, err)
				continue
			}
			into := group.into()
			if err := json.Unmarshal(fixture.Body, into); err != nil {
				t.Errorf("%s %s does not parse into the generated struct: %v", group.name, fixture.Name, err)
				continue
			}
			again, _ := json.Marshal(into)
			if err := schema.Validate(decodeDocument(t, again)); err != nil {
				t.Errorf("%s %s is not valid after a round trip (%s): %v", group.name, fixture.Name, again, err)
			}
			if !workcopyPairingsHold(into) {
				t.Errorf("%s %s breaks a pairing the schema states", group.name, fixture.Name)
			}
			if failure, ok := into.(*WorkcopyError); ok && workcopyFailures[failure.Error].message != failure.Message {
				t.Errorf("the %s message is not the handler table's: %q", failure.Error, failure.Message)
			}
		}
		for _, fixture := range group.set.Invalid {
			if err := schema.Validate(decodeDocument(t, fixture.Body)); err == nil {
				t.Errorf("%s %s should be invalid (%s)", group.name, fixture.Name, fixture.Why)
			}
		}
		if group.violations != nil && len(group.violations) == 0 {
			t.Errorf("the %s pairing violations are empty", group.name)
		}
		for _, fixture := range group.violations {
			if err := schema.Validate(decodeDocument(t, fixture.Body)); err != nil {
				t.Errorf("pairing violation %s should pass the schema: %v", fixture.Name, err)
			}
			into := group.into()
			if err := json.Unmarshal(fixture.Body, into); err != nil {
				t.Fatalf("%s does not parse: %v", fixture.Name, err)
			}
			if workcopyPairingsHold(into) {
				t.Errorf("pairing violation %s keeps every pairing (%s)", fixture.Name, fixture.Why)
			}
		}
	}
	// Every kind the handler table answers is a schema kind, and back.
	for _, kind := range []WorkcopyErrorKind{"invalid", "unavailable", "notAuthorized", "rootUnavailable", "notFound", "link", "changed", "unreadable", "generated", "reserved", "notRegularFile", "tooLarge", "notText", "tooMany", "drift", "snapshot", "io"} {
		if _, ok := workcopyFailures[kind]; !ok {
			t.Errorf("no sentence for %s", kind)
		}
	}
}
