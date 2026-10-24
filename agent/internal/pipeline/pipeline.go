// Package pipeline is the agent's view of the pipeline model.
//
// The structs in models_gen.go are generated from schema/pipeline.schema.json,
// which is the single source of truth for the model; so is the copy of that
// schema in this directory, which exists only because a Go module cannot embed
// a file from outside itself. Everything else in this package is the thin
// hand-written layer around them: validating a document against the same
// schema, and giving the discriminated union of processing steps a Go type
// again, which JSON Schema's oneOf cannot express on its own.
//
// Run `pnpm run generate` after changing the schema; CI fails when the
// generated files or the schema copy are stale.
package pipeline

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed pipeline.schema.json
var schemaJSON []byte

// SchemaJSON returns the authoritative JSON Schema, verbatim.
func SchemaJSON() []byte {
	return bytes.Clone(schemaJSON)
}

// schemaID is the $id the schema declares, used as its compilation URL.
const schemaID = "https://github.com/zssvaidar/kpv/schema/pipeline.schema.json"

// loadedSchema is the compiled schema plus the same schema as a plain
// document, which the x-kpv annotation rules walk.
type loadedSchema struct {
	compiled *jsonschema.Schema
	document map[string]any
}

// loadSchema compiles the embedded schema on first use and keeps the result.
var loadSchema = sync.OnceValues(func() (loadedSchema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return loadedSchema{}, fmt.Errorf("decode embedded schema: %w", err)
	}
	document, ok := doc.(map[string]any)
	if !ok {
		return loadedSchema{}, fmt.Errorf("embedded schema is %T, want a JSON object", doc)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, doc); err != nil {
		return loadedSchema{}, fmt.Errorf("add embedded schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return loadedSchema{}, fmt.Errorf("compile embedded schema: %w", err)
	}
	return loadedSchema{compiled: compiled, document: document}, nil
})
