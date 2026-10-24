package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Step is one processing step, decoded into the generated struct for its type.
//
// The generated ProcessingStep is an empty interface because JSON Schema's
// oneOf has no shape in Go. The type discriminator is what gives it a type
// back, and the only knowledge added here is which generated struct goes with
// which generated discriminator constant.
type Step interface {
	// StepID is the step's stable identifier, unique within the pipeline.
	StepID() Identifier
	// StepType is the value of the type discriminator.
	StepType() string
	// CustomCodeRef is the step's custom-code reference, nil when it has none.
	CustomCodeRef() *CustomCodeReference
}

// StepID returns the step's stable identifier.
func (s *FilterStep) StepID() Identifier { return s.ID }

// StepType returns the step's type discriminator.
func (s *FilterStep) StepType() string { return string(s.Type) }

// CustomCodeRef returns the step's custom-code reference, nil when it has none.
func (s *FilterStep) CustomCodeRef() *CustomCodeReference { return s.CustomCode }

// StepID returns the step's stable identifier.
func (s *TransformStep) StepID() Identifier { return s.ID }

// StepType returns the step's type discriminator.
func (s *TransformStep) StepType() string { return string(s.Type) }

// CustomCodeRef returns the step's custom-code reference, nil when it has none.
func (s *TransformStep) CustomCodeRef() *CustomCodeReference { return s.CustomCode }

// StepID returns the step's stable identifier.
func (s *ValidateStep) StepID() Identifier { return s.ID }

// StepType returns the step's type discriminator.
func (s *ValidateStep) StepType() string { return string(s.Type) }

// CustomCodeRef returns the step's custom-code reference, nil when it has none.
func (s *ValidateStep) CustomCodeRef() *CustomCodeReference { return s.CustomCode }

// StepID returns the step's stable identifier.
func (s *CustomCodeStep) StepID() Identifier { return s.ID }

// StepType returns the step's type discriminator.
func (s *CustomCodeStep) StepType() string { return string(s.Type) }

// CustomCodeRef returns the step's custom-code reference, nil when it has none.
func (s *CustomCodeStep) CustomCodeRef() *CustomCodeReference { return s.CustomCode }

// Parse validates raw JSON against the schema and decodes it into the
// generated Pipeline struct together with its typed processing steps. A
// document that fails validation is not decoded at all, so no caller ever sees
// a half-built pipeline.
func Parse(data []byte) (*Pipeline, []Step, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("decode pipeline: %w", err)
	}
	problems, err := Validate(document)
	if err != nil {
		return nil, nil, err
	}
	if len(problems) > 0 {
		return nil, nil, problems
	}

	var parsed Pipeline
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, nil, fmt.Errorf("decode pipeline: %w", err)
	}
	steps, err := TypedSteps(&parsed)
	if err != nil {
		return nil, nil, err
	}
	return &parsed, steps, nil
}

// TypedSteps decodes a pipeline's processing steps into the generated struct
// for each step's type. It fails on a type no block covers rather than hand
// back a partial step; a document that went through Parse cannot get here with
// one, because the schema rejects it first.
func TypedSteps(p *Pipeline) ([]Step, error) {
	steps := make([]Step, 0, len(p.ProcessingSteps))
	for index, raw := range p.ProcessingSteps {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("processing step %d: %w", index, err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(encoded, &probe); err != nil {
			return nil, fmt.Errorf("processing step %d: %w", index, err)
		}

		var step Step
		switch probe.Type {
		case string(FilterStepTypeFilter):
			step = &FilterStep{}
		case string(TransformStepTypeTransform):
			step = &TransformStep{}
		case string(ValidateStepTypeValidate):
			step = &ValidateStep{}
		case string(CustomCodeStepTypeCustomCode):
			step = &CustomCodeStep{}
		default:
			return nil, fmt.Errorf("processing step %d: unknown type %q", index, probe.Type)
		}
		if err := json.Unmarshal(encoded, step); err != nil {
			return nil, fmt.Errorf("processing step %d: %w", index, err)
		}
		steps = append(steps, step)
	}
	return steps, nil
}
