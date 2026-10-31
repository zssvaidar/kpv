// The connection a Kafka block names (#24): only the name is in the pipeline
// file, it survives a round trip unchanged, and a pipeline without one never
// gains the key.

package pipeline_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

const withoutConnection = `id: orders-to-billing
language: python
source:
  id: orders
  type: kafkaSource
  topic: orders.v1
destination:
  id: billing
  type: kafkaDestination
  topic: billing.v1
runtimeConfiguration:
  networkMode: docker
`

func TestAPipelineWithoutAConnectionDoesNotGainTheKey(t *testing.T) {
	t.Parallel()
	parsed, _, err := pipeline.ParseYAML([]byte(withoutConnection))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Source.Connection != nil || parsed.Destination.Connection != nil {
		t.Fatal("a pipeline with no connection read back with one")
	}
	written, err := pipeline.MarshalYAML(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(written, []byte("connection")) {
		t.Fatalf("the written pipeline gained a connection key:\n%s", written)
	}
}

// Each block keeps its own name, a name no stored connection has included:
// the schema cannot see the store, so a dangling name is valid and is never
// dropped or rewritten.
func TestEachBlockKeepsTheConnectionItNamesThroughARoundTrip(t *testing.T) {
	t.Parallel()
	source := []byte(withoutConnection)
	source = bytes.Replace(source, []byte("  topic: orders.v1\n"), []byte("  topic: orders.v1\n  connection: eu-cluster\n"), 1)
	source = bytes.Replace(source, []byte("  topic: billing.v1\n"), []byte("  topic: billing.v1\n  connection: Dangling.Name_9\n"), 1)
	parsed, _, err := pipeline.ParseYAML(source)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		written, err := pipeline.MarshalYAML(parsed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(written, []byte("connection: eu-cluster\n")) || !bytes.Contains(written, []byte("connection: Dangling.Name_9\n")) {
			t.Fatalf("round %d lost a name:\n%s", round, written)
		}
		parsed, _, err = pipeline.ParseYAML(written)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Source.Connection == nil || *parsed.Source.Connection != "eu-cluster" ||
			parsed.Destination.Connection == nil || *parsed.Destination.Connection != "Dangling.Name_9" {
			t.Fatalf("round %d read back %v and %v", round, parsed.Source.Connection, parsed.Destination.Connection)
		}
	}
}

func TestAConnectionNameThatBreaksTheRuleIsRefusedOnItsPath(t *testing.T) {
	t.Parallel()
	for _, name := range []string{`""`, `"my cluster"`} {
		source := bytes.Replace([]byte(withoutConnection), []byte("  topic: orders.v1\n"), []byte("  topic: orders.v1\n  connection: "+name+"\n"), 1)
		_, _, err := pipeline.ParseYAML(source)
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
		var fileErr *pipeline.FileError
		if !errors.As(err, &fileErr) {
			t.Fatalf("not a FileError: %v", err)
		}
		found := false
		for _, problem := range fileErr.Problems {
			if problem.InstanceLocation == "/source/connection" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: no problem at /source/connection in %+v", name, fileErr.Problems)
		}
	}
}
