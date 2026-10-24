// Reading and writing pipeline.yaml: the round trip, the bytes a write
// produces, and what every malformed file produces instead of a pipeline.

package pipeline_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

// pipelineFile is the name a pipeline file has in these tests. Which directory
// it lives in is #48, so every test is handed a path of its own.
const pipelineFile = "pipeline.yaml"

func TestAPipelineRoundTripsFromModelToYAMLAndBack(t *testing.T) {
	for _, fixture := range loadFixtures(t).Valid {
		t.Run(fixture.Name, func(t *testing.T) {
			want, wantSteps, err := pipeline.Parse(fixture.Pipeline)
			if err != nil {
				t.Fatalf("Parse the fixture: %v", err)
			}

			path := filepath.Join(t.TempDir(), pipelineFile)
			if err := pipeline.SaveFile(path, want); err != nil {
				t.Fatalf("SaveFile: %v", err)
			}
			got, gotSteps, err := pipeline.LoadFile(path)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}

			if !reflect.DeepEqual(withoutEmptySteps(got), withoutEmptySteps(want)) {
				t.Errorf("the pipeline changed on the round trip\n got %+v\nwant %+v", got, want)
			}
			if len(gotSteps) != len(wantSteps) || (len(gotSteps) > 0 && !reflect.DeepEqual(gotSteps, wantSteps)) {
				t.Errorf("the typed steps changed on the round trip\n got %+v\nwant %+v", gotSteps, wantSteps)
			}
		})
	}
}

// withoutEmptySteps normalises the one difference a round trip is allowed to
// make. The schema says absent and empty processingSteps both mean a pipeline
// that only moves messages from its source to its destination, and the
// generated model marks the field omitempty, so a pipeline with an empty list
// is written with no list at all and reads back with none.
// TestAnEmptyStepListIsWrittenAsNoStepList states that on its own.
func withoutEmptySteps(p *pipeline.Pipeline) pipeline.Pipeline {
	copied := *p
	if len(copied.ProcessingSteps) == 0 {
		copied.ProcessingSteps = nil
	}
	return copied
}

func TestAnEmptyStepListIsWrittenAsNoStepList(t *testing.T) {
	loaded, steps, err := pipeline.Parse(findValidFixture(t, "zero-processing-steps"))
	if err != nil {
		t.Fatalf("Parse the fixture: %v", err)
	}
	if len(loaded.ProcessingSteps) != 0 || len(steps) != 0 {
		t.Fatalf("the fixture is meant to have an empty step list, it has %d", len(loaded.ProcessingSteps))
	}

	path := filepath.Join(t.TempDir(), pipelineFile)
	if err := pipeline.SaveFile(path, loaded); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	if written := string(read(t, path)); strings.Contains(written, "processingSteps") {
		t.Errorf("an empty step list was written out:\n%s", written)
	}

	reloaded, reloadedSteps, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if reloaded.ProcessingSteps != nil || len(reloadedSteps) != 0 {
		t.Errorf("the reloaded pipeline has %d steps, want none", len(reloaded.ProcessingSteps))
	}
}

func TestSavingAnUnchangedPipelineTwiceIsByteIdentical(t *testing.T) {
	loaded, _, err := pipeline.Parse(findValidFixture(t, "every-block-type"))
	if err != nil {
		t.Fatalf("Parse the fixture: %v", err)
	}

	path := filepath.Join(t.TempDir(), pipelineFile)
	if err := pipeline.SaveFile(path, loaded); err != nil {
		t.Fatalf("first SaveFile: %v", err)
	}
	first := read(t, path)
	if err := pipeline.SaveFile(path, loaded); err != nil {
		t.Fatalf("second SaveFile: %v", err)
	}
	if second := read(t, path); !bytes.Equal(first, second) {
		t.Errorf("saving the same pipeline twice produced different bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	// And the bytes a load-save cycle produces are the bytes it started from,
	// so a file the agent wrote is a fixed point.
	reloaded, _, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := pipeline.SaveFile(path, reloaded); err != nil {
		t.Fatalf("SaveFile after LoadFile: %v", err)
	}
	if again := read(t, path); !bytes.Equal(first, again) {
		t.Errorf("a load-save cycle changed the file\nbefore:\n%s\nafter:\n%s", first, again)
	}
}

// saveTargetEnv names the file the helper process writes. Go randomises map
// iteration order per process, so writing the same pipeline from two separate
// processes is the only way to tell a deterministic encoder from one that only
// happens to agree with itself.
const saveTargetEnv = "KPV_TEST_SAVE_TARGET"

func TestSavingIsByteIdenticalInSeparateProcesses(t *testing.T) {
	directory := t.TempDir()
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	var written [][]byte
	for run := range 3 {
		path := filepath.Join(directory, "from-process-"+string(rune('a'+run))+".yaml")
		command := exec.Command(os.Args[0], "-test.run=^TestSaveInAnotherProcess$")
		command.Dir = here
		command.Env = append(os.Environ(), saveTargetEnv+"="+path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("the helper process failed: %v\n%s", err, output)
		}
		written = append(written, read(t, path))
	}

	for run, bytesWritten := range written[1:] {
		if !bytes.Equal(written[0], bytesWritten) {
			t.Fatalf("process %d wrote different bytes than process 0\nprocess 0:\n%s\nprocess %d:\n%s",
				run+1, written[0], run+1, bytesWritten)
		}
	}
	if len(written[0]) == 0 {
		t.Fatal("the helper process wrote nothing")
	}
}

// TestSaveInAnotherProcess is the helper half of
// TestSavingIsByteIdenticalInSeparateProcesses: a fresh process that saves one
// pipeline and exits.
func TestSaveInAnotherProcess(t *testing.T) {
	target := os.Getenv(saveTargetEnv)
	if target == "" {
		t.Skip("the helper for TestSavingIsByteIdenticalInSeparateProcesses")
	}
	loaded, _, err := pipeline.Parse(findValidFixture(t, "every-block-type"))
	if err != nil {
		t.Fatalf("Parse the fixture: %v", err)
	}
	if err := pipeline.SaveFile(target, loaded); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
}

// stepsInOrder is a pipeline whose processing steps are deliberately not in
// alphabetical or any other derivable order, with comments in it.
const stepsInOrder = `# a pipeline with its steps in the order it runs them
id: ordered
language: python
source:
  id: in
  type: kafkaSource
  topic: in.v1
processingSteps:
  - id: zeta-validate # last alphabetically, first in the pipeline
    type: validate
    configuration:
      rules:
        - field: amount
      onInvalid: drop
  - id: alpha-filter
    type: filter
    configuration:
      expression: amount > 0
  - id: middle-transform
    type: transform
    configuration:
      mappings:
        - targetField: total
          expression: amount
destination:
  id: out
  type: kafkaDestination
  topic: out.v1
runtimeConfiguration:
  networkMode: docker
`

func TestProcessingStepOrderSurvivesAReadAndAWrite(t *testing.T) {
	want := []string{"zeta-validate", "alpha-filter", "middle-transform"}

	path := filepath.Join(t.TempDir(), pipelineFile)
	write(t, path, []byte(stepsInOrder))

	loaded, steps, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	got := make([]string, 0, len(steps))
	for _, step := range steps {
		got = append(got, string(step.StepID()))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reading reordered the steps: got %v, want %v", got, want)
	}

	if err := pipeline.SaveFile(path, loaded); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	written := string(read(t, path))
	at := make([]int, len(want))
	for index, id := range want {
		if at[index] = strings.Index(written, "id: "+id); at[index] < 0 {
			t.Fatalf("the written file lost the step %q:\n%s", id, written)
		}
	}
	for index := 1; index < len(at); index++ {
		if at[index] < at[index-1] {
			t.Errorf("writing reordered the steps, %q came out before %q:\n%s", want[index], want[index-1], written)
		}
	}
}

func TestCommentsAreDroppedOnWrite(t *testing.T) {
	// Comment preservation is not required: a pipeline file is written from
	// the model, and the model has nowhere to keep a comment. This asserts the
	// loss deliberately, so it stays a decision and not a surprise.
	path := filepath.Join(t.TempDir(), pipelineFile)
	write(t, path, []byte(stepsInOrder))

	loaded, _, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := pipeline.SaveFile(path, loaded); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	written := string(read(t, path))
	if strings.Contains(written, "#") {
		t.Errorf("the written file kept a comment, which this package does not promise:\n%s", written)
	}
	if !strings.Contains(written, "id: ordered") {
		t.Errorf("the written file lost the pipeline itself:\n%s", written)
	}
}

func TestEveryInvalidSharedFixtureIsRejectedAsAYAMLFile(t *testing.T) {
	// The one shared fixture set, read as YAML files: the same documents the
	// JSON side rejects have to be rejected here, at the same locations, so
	// the format a pipeline arrives in cannot change the answer.
	for _, fixture := range loadFixtures(t).Invalid {
		t.Run(fixture.Name, func(t *testing.T) {
			asYAML, err := pipeline.JSONToYAML(fixture.Pipeline)
			if err != nil {
				t.Fatalf("render the fixture as YAML: %v", err)
			}
			path := filepath.Join(t.TempDir(), pipelineFile)
			write(t, path, asYAML)

			_, _, err = pipeline.LoadFile(path)
			fault := fileError(t, err)
			if fault.Kind != pipeline.KindSchema {
				t.Fatalf("%s failed as %q, want %q: %v", fixture.Name, fault.Kind, pipeline.KindSchema, fault)
			}
			if fault.Path != path {
				t.Errorf("the failure names %q, want the file %q", fault.Path, path)
			}
			for _, want := range fixture.Errors {
				if !reports(fault.Problems, want) {
					t.Errorf("no failure at instance %q, schema %q; got %v",
						want.InstanceLocation, want.KeywordLocation, fault.Problems)
				}
			}
		})
	}
}

func TestMalformedFilesFailWithAStructuredError(t *testing.T) {
	valid := func() string {
		return stepsInOrder
	}
	replace := func(before, after string) string {
		return strings.Replace(valid(), before, after, 1)
	}

	for _, testCase := range []struct {
		name         string
		content      string
		kind         pipeline.ErrorKind
		location     string
		wantMessage  string
		schemaErrors []expectedError
	}{
		{
			name:     "invalid YAML syntax",
			content:  "id: ordered\nlanguage: [python\n",
			kind:     pipeline.KindSyntax,
			location: "",
		},
		{
			name:     "a tab where YAML wants a space",
			content:  "id: ordered\nsource:\n\tid: in\n",
			kind:     pipeline.KindSyntax,
			location: "",
		},
		{
			name:    "valid YAML of the wrong type at the root",
			content: "- id: ordered\n- language: python\n",
			kind:    pipeline.KindSchema,
			schemaErrors: []expectedError{
				{InstanceLocation: "", KeywordLocation: "/type"},
			},
		},
		{
			name:    "valid YAML of the wrong type in a field",
			content: replace("language: python", "language: 7"),
			kind:    pipeline.KindSchema,
			schemaErrors: []expectedError{
				{InstanceLocation: "/language", KeywordLocation: "/$defs/language/type"},
			},
		},
		{
			name:    "a missing required field",
			content: replace("language: python\n", ""),
			kind:    pipeline.KindSchema,
			schemaErrors: []expectedError{
				{InstanceLocation: "", KeywordLocation: "/required"},
			},
		},
		{
			name:    "an unknown extra field",
			content: replace("id: ordered", "id: ordered\nretries: 3"),
			kind:    pipeline.KindSchema,
			schemaErrors: []expectedError{
				{InstanceLocation: "", KeywordLocation: "/additionalProperties"},
			},
		},
		{
			name:    "an unknown extra field inside a step",
			content: replace("expression: amount > 0", "expression: amount > 0\n      invert: true"),
			kind:    pipeline.KindSchema,
			schemaErrors: []expectedError{
				{
					InstanceLocation: "/processingSteps/1/configuration",
					KeywordLocation:  "/$defs/filterStep/properties/configuration/additionalProperties",
				},
			},
		},
		{
			name:        "an empty file",
			content:     "",
			kind:        pipeline.KindEmptyDocument,
			wantMessage: "no YAML document",
		},
		{
			name:        "a file of nothing but whitespace",
			content:     "\n   \n  \n",
			kind:        pipeline.KindEmptyDocument,
			wantMessage: "no YAML document",
		},
		{
			name:        "a file of nothing but comments",
			content:     "# a pipeline, one day\n# but not yet\n",
			kind:        pipeline.KindEmptyDocument,
			wantMessage: "no YAML document",
		},
		{
			name:        "a duplicate key at the root",
			content:     replace("language: python", "language: python\nid: ordered-again"),
			kind:        pipeline.KindDuplicateKey,
			location:    "/id",
			wantMessage: `the key "id" is defined twice`,
		},
		{
			name:        "a duplicate key inside a block",
			content:     replace("topic: in.v1", "topic: in.v1\n  topic: in.v2"),
			kind:        pipeline.KindDuplicateKey,
			location:    "/source/topic",
			wantMessage: `the key "topic" is defined twice`,
		},
		{
			name:        "a duplicate key inside a step",
			content:     replace("id: alpha-filter", "id: alpha-filter\n    id: alpha-filter-again"),
			kind:        pipeline.KindDuplicateKey,
			location:    "/processingSteps/1/id",
			wantMessage: `the key "id" is defined twice`,
		},
		{
			name:        "a key that is not a string",
			content:     replace("id: ordered", "7: ordered"),
			kind:        pipeline.KindUnsupportedValue,
			wantMessage: "is not a string",
		},
		{
			name:        "a value JSON cannot hold",
			content:     replace("language: python", "language: 2026-10-24"),
			kind:        pipeline.KindUnsupportedValue,
			location:    "/language",
			wantMessage: "no JSON equivalent",
		},
		{
			name:        "two documents in one file",
			content:     valid() + "---\n" + valid(),
			kind:        pipeline.KindMultipleDocuments,
			wantMessage: "more than one YAML document",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), pipelineFile)
			write(t, path, []byte(testCase.content))

			loaded, steps, err := pipeline.LoadFile(path)
			if loaded != nil || steps != nil {
				t.Errorf("a malformed file produced a pipeline: %+v", loaded)
			}
			fault := fileError(t, err)
			if fault.Kind != testCase.kind {
				t.Fatalf("failed as %q, want %q: %v", fault.Kind, testCase.kind, fault)
			}
			if fault.Path != path {
				t.Errorf("the failure names %q, want the file %q", fault.Path, path)
			}
			if fault.Op != "load" {
				t.Errorf("the failure says op %q, want %q", fault.Op, "load")
			}
			if testCase.location != "" && fault.Location != testCase.location {
				t.Errorf("the failure points at %q, want %q", fault.Location, testCase.location)
			}
			if testCase.wantMessage != "" && !strings.Contains(fault.Message, testCase.wantMessage) {
				t.Errorf("the message %q does not mention %q", fault.Message, testCase.wantMessage)
			}
			for _, want := range testCase.schemaErrors {
				if !reports(fault.Problems, want) {
					t.Errorf("no failure at instance %q, schema %q; got %v",
						want.InstanceLocation, want.KeywordLocation, fault.Problems)
				}
			}
			if fault.Error() == "" {
				t.Error("the failure renders as an empty string")
			}
		})
	}
}

func TestADuplicateKeyIsNotResolvedToTheLastOccurrence(t *testing.T) {
	// The silent failure this guards against: a parser that reads the file
	// below as topic in.v2 and reports nothing.
	path := filepath.Join(t.TempDir(), pipelineFile)
	write(t, path, []byte(strings.Replace(stepsInOrder, "topic: in.v1", "topic: in.v1\n  topic: in.v2", 1)))

	loaded, _, err := pipeline.LoadFile(path)
	if err == nil {
		t.Fatalf("a duplicate key was accepted, and the pipeline reads source.topic as %q",
			loaded.Source.Topic)
	}
	fault := fileError(t, err)
	if fault.Kind != pipeline.KindDuplicateKey {
		t.Fatalf("failed as %q, want %q: %v", fault.Kind, pipeline.KindDuplicateKey, fault)
	}
	if fault.Line != 8 {
		t.Errorf("the failure is on line %d, want the second occurrence on line 8", fault.Line)
	}
	if !strings.Contains(fault.Message, "line 7") {
		t.Errorf("the message %q does not name the first occurrence on line 7", fault.Message)
	}
}

func TestLoadReportsAMissingFileAsAFileFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), pipelineFile)
	_, _, err := pipeline.LoadFile(path)
	fault := fileError(t, err)
	if fault.Kind != pipeline.KindIO {
		t.Errorf("failed as %q, want %q: %v", fault.Kind, pipeline.KindIO, fault)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the failure does not unwrap to os.ErrNotExist: %v", err)
	}
}

func TestSaveRefusesAnInvalidPipelineAndKeepsThePreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), pipelineFile)
	write(t, path, []byte(stepsInOrder))
	before := read(t, path)

	loaded, _, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	loaded.Language = "cobol"

	err = pipeline.SaveFile(path, loaded)
	fault := fileError(t, err)
	if fault.Kind != pipeline.KindSchema {
		t.Fatalf("failed as %q, want %q: %v", fault.Kind, pipeline.KindSchema, fault)
	}
	if fault.Op != "save" {
		t.Errorf("the failure says op %q, want %q", fault.Op, "save")
	}
	if !reports(fault.Problems, expectedError{InstanceLocation: "/language", KeywordLocation: "/$defs/language/enum"}) {
		t.Errorf("the failure does not point at /language: %v", fault.Problems)
	}
	if after := read(t, path); !bytes.Equal(before, after) {
		t.Errorf("a refused save changed the file\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if err := pipeline.SaveFile(path, nil); err == nil {
		t.Error("SaveFile accepted a nil pipeline")
	}
}

// refusing is the shape #6 will have: a guard that sees every path and can say
// no. Until then nothing in the package implements PathGuard, so this stands in
// for it and proves the seam is on the path of both Load and Save.
type refusing struct {
	seen     []string
	refuse   bool
	resolved string
}

func (g *refusing) ResolvePipelinePath(path string) (string, error) {
	g.seen = append(g.seen, path)
	if g.refuse {
		return "", errors.New("outside the project")
	}
	if g.resolved != "" {
		return g.resolved, nil
	}
	return path, nil
}

func TestEveryPathGoesThroughTheGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), pipelineFile)
	write(t, path, []byte(stepsInOrder))

	guard := &refusing{}
	store := pipeline.Store{Guard: guard}

	loaded, _, err := store.Load(path)
	if err != nil {
		t.Fatalf("Load through a guard: %v", err)
	}
	if err := store.Save(path, loaded); err != nil {
		t.Fatalf("Save through a guard: %v", err)
	}
	if !reflect.DeepEqual(guard.seen, []string{path, path}) {
		t.Errorf("the guard saw %v, want the path once per call", guard.seen)
	}
}

func TestAGuardThatRefusesStopsTheReadAndTheWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), pipelineFile)
	write(t, path, []byte(stepsInOrder))
	before := read(t, path)

	loaded, _, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	store := pipeline.Store{Guard: &refusing{refuse: true}}
	if _, _, err := store.Load(path); fileError(t, err).Kind != pipeline.KindForbidden {
		t.Errorf("a refused load failed as %q, want %q", fileError(t, err).Kind, pipeline.KindForbidden)
	}
	err = store.Save(path, loaded)
	if fault := fileError(t, err); fault.Kind != pipeline.KindForbidden {
		t.Errorf("a refused save failed as %q, want %q", fault.Kind, pipeline.KindForbidden)
	}
	if after := read(t, path); !bytes.Equal(before, after) {
		t.Errorf("a refused save changed the file\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSaveKeepsThePreviousFileWhenTheDirectoryCannotBeWrittenTo(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, pipelineFile)
	write(t, path, []byte(stepsInOrder))
	before := read(t, path)

	loaded, _, err := pipeline.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	loaded.RuntimeConfiguration.NetworkMode = "host"

	// Read and execute, so the file is still readable and no temporary file
	// can be created next to it.
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatalf("chmod the directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })

	err = pipeline.SaveFile(path, loaded)
	fault := fileError(t, err)
	if fault.Kind != pipeline.KindIO {
		t.Fatalf("failed as %q, want %q: %v", fault.Kind, pipeline.KindIO, fault)
	}
	if after := read(t, path); !bytes.Equal(before, after) {
		t.Errorf("a failed save replaced the previous file\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSaveLeavesNothingBesideThePipelineFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, pipelineFile)
	loaded, _, err := pipeline.Parse(findValidFixture(t, "every-block-type"))
	if err != nil {
		t.Fatalf("Parse the fixture: %v", err)
	}
	if err := pipeline.SaveFile(path, loaded); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != pipelineFile {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the directory holds %v, want just %q", names, pipelineFile)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the pipeline file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("a new pipeline file has mode %v, want 0644", info.Mode().Perm())
	}
}

// fileError asserts that err is the structured failure this package promises,
// rather than a bare string or a panic.
func fileError(t *testing.T, err error) *pipeline.FileError {
	t.Helper()
	if err == nil {
		t.Fatal("expected a failure, got none")
	}
	var fault *pipeline.FileError
	if !errors.As(err, &fault) {
		t.Fatalf("the failure is %T, not a *pipeline.FileError: %v", err, err)
	}
	if fault.Kind == "" {
		t.Errorf("the failure has no kind: %v", fault)
	}
	if fault.Message == "" {
		t.Errorf("the failure has no message: %v", fault)
	}
	return fault
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
