# internal/pipeline

Parses, validates and renders a pipeline document. The model is the generated
one (`models_gen.go`, from `schema/pipeline.schema.json`); this package is the
validation layer around it and makes no filesystem call. Reading and writing
`pipeline.yaml`, and where a pipeline lives, are `internal/catalog`'s (#48);
`httpapi` may not import this package (`agent/CLAUDE.md`).

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
- `MarshalYAML` validates before rendering, and the same pipeline is always
  the same bytes, even across processes. Comments are dropped
  (`file_test.go`, which puts the bytes on disk itself)
- `PositionInYAML` gives the line and column a JSON Pointer names in a YAML
  document, so a schema problem can be shown on the line a person edits
- the generated `ProcessingStep` is an empty interface; `TypedSteps` in
  `steps.go` maps each `type` discriminator to its struct, so a new step type
  needs a case there
