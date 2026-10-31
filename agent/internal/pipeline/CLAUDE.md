# internal/pipeline

Reads and writes a pipeline file. The model is the generated one
(`models_gen.go`, from `schema/pipeline.schema.json`); this package is the
validation and file layer around it. Nothing in the running agent uses it yet:
`cmd/agent` does not import it and `httpapi` may not (`agent/CLAUDE.md`).
Which directory a pipeline lives in, and moving the store onto the guard's
operations, is #48.

- YAML is bridged through JSON (`YAMLToJSON`, `JSONToYAML` in `yaml.go`), so
  the one JSON Schema validator (`validate.go`) covers both formats
- every failure is one `*FileError` (`errors.go`): a `Kind` to branch on, a
  JSON Pointer `Location`, `Line` and `Column`, and `Problems` with the same
  pointers `validate.go` reports, which are the ones
  `packages/schema/src/validate.ts` reports too
- the reader rejects what YAML allows: a duplicate key, no document, more than
  one document, a non-string key, a tag outside the JSON data model. It uses
  `yaml.v3` through a `Decoder`, never `Unmarshal`; `yaml.go` says why
  (`TestMalformedFilesFailWithAStructuredError`,
  `TestADuplicateKeyIsNotResolvedToTheLastOccurrence` in `file_test.go`)
- `Save` validates before writing and writes atomically (a temporary file in
  the same directory, then a rename), and the same pipeline is always the same
  bytes, even across processes. Comments are dropped (`atomic_test.go`,
  `file_test.go`)
- `Store.resolve` is the one place a path enters the filesystem, through
  `Store.Guard`. `projectfs.Guard.PipelineSeam()` satisfies it, but it hands a
  path back to be opened with `os`, which is the weaker route; `seam.go` in
  `projectfs` says why. `LoadFile` and `SaveFile` have no guard
  (`TestEveryPathGoesThroughTheGuard`)
- the generated `ProcessingStep` is an empty interface; `TypedSteps` in
  `steps.go` maps each `type` discriminator to its struct, so a new step type
  needs a case there
