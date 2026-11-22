package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// The fixed sentences a Problem is said in.
const (
	problemNotUTF8          = "The output is not UTF-8."
	problemNotJSON          = "The output is not one JSON document."
	problemTrailing         = "The output holds something after its JSON document."
	problemDuplicateKey     = "This key appears more than once in its object."
	problemRequired         = "This required property is missing."
	problemAdditional       = "This property is not part of the contract."
	problemType             = "This value has the wrong type."
	problemEnum             = "This value is not one the contract allows."
	problemMinimum          = "This number is below the contract's minimum."
	problemMinLength        = "This string is shorter than the contract allows."
	problemSchema           = "This value does not match the parser contract."
	problemEmptyRange       = "The range is empty or ends before it starts."
	problemPastEnd          = "The range ends past the end of the source."
	problemStartSplits      = "The range starts inside a UTF-8 character."
	problemEndSplits        = "The range ends inside a UTF-8 character."
	problemOverlap          = "This range overlaps another stage or unrecognized region."
	problemUnparseableStage = "An unparseable result has stages."
	problemUnparseableQuiet = "An unparseable result has no warning."
)

// schemaID is the $id the contract schema declares, used as its compilation
// URL.
const schemaID = "https://github.com/zssvaidar/kpv/schema/parser.schema.json"

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("decode the embedded parser schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return nil, fmt.Errorf("add the embedded parser schema: %w", err)
	}
	return compiler.Compile(schemaID)
})

// Validate is the one validation both transports' output goes through. It
// answers a Result when output passes every rule for source, and the problems
// otherwise, sorted by pointer and then message; never both.
func Validate(source, output []byte) (*Result, []Problem) {
	if !utf8.Valid(output) {
		return nil, []Problem{{Pointer: "", Message: problemNotUTF8}}
	}
	if problems := structure(output); len(problems) > 0 {
		return nil, sorted(problems)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(output))
	if err != nil {
		return nil, []Problem{{Pointer: "", Message: problemNotJSON}}
	}
	schema, err := compiledSchema()
	if err != nil {
		// The embedded schema is generated and tested; this is unreachable
		// short of a broken build, and refusing is the safe answer.
		return nil, []Problem{{Pointer: "", Message: problemSchema}}
	}
	if err := schema.Validate(document); err != nil {
		var invalid *jsonschema.ValidationError
		if !errors.As(err, &invalid) {
			return nil, []Problem{{Pointer: "", Message: problemSchema}}
		}
		return nil, sorted(flatten(invalid, nil))
	}

	// The schema holds, so the shape below is known.
	root := document.(map[string]any)
	stages := root["stages"].([]any)
	unrecognized := root["unrecognized"].([]any)
	warnings := root["warnings"].([]any)

	var problems []Problem
	var spans []span
	check := func(pointer string, rangeValue any, overlaps bool) (span, bool) {
		found, problem := rangeOf(source, pointer, rangeValue.(map[string]any))
		if problem != nil {
			problems = append(problems, *problem)
			return span{}, false
		}
		if overlaps {
			spans = append(spans, found)
		}
		return found, true
	}
	for index, stage := range stages {
		check("/stages/"+strconv.Itoa(index)+"/range", stage.(map[string]any)["range"], true)
	}
	for index, region := range unrecognized {
		check("/unrecognized/"+strconv.Itoa(index)+"/range", region.(map[string]any)["range"], true)
	}
	for index, warning := range warnings {
		if rangeValue, present := warning.(map[string]any)["range"]; present {
			check("/warnings/"+strconv.Itoa(index)+"/range", rangeValue, false)
		}
	}
	problems = append(problems, overlapping(spans)...)
	if root["status"] == string(ParserStatusUnparseable) {
		if len(stages) > 0 {
			problems = append(problems, Problem{Pointer: "/stages", Message: problemUnparseableStage})
		}
		if len(warnings) == 0 {
			problems = append(problems, Problem{Pointer: "/warnings", Message: problemUnparseableQuiet})
		}
	}
	if len(problems) > 0 {
		return nil, sorted(problems)
	}
	return build(root), nil
}

// structure holds what the schema cannot see: the output is exactly one JSON
// document with only whitespace after it, and no object in it has a key
// twice. A decoder into a map keeps the last of two keys silently, which is
// why this reads token by token.
func structure(output []byte) []Problem {
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.UseNumber()
	var problems []Problem
	if !walk(decoder, "", &problems) {
		return []Problem{{Pointer: "", Message: problemNotJSON}}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return []Problem{{Pointer: "", Message: problemTrailing}}
	}
	return problems
}

// walk reads one value, recording a duplicate key at its pointer. It answers
// false when the output is not JSON.
func walk(decoder *json.Decoder, pointer string, problems *[]Problem) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return true
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			child := pointer + "/" + escapePointerToken(key)
			if seen[key] {
				*problems = append(*problems, Problem{Pointer: child, Message: problemDuplicateKey})
			}
			seen[key] = true
			if !walk(decoder, child, problems) {
				return false
			}
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if !walk(decoder, pointer+"/"+strconv.Itoa(index), problems) {
				return false
			}
		}
	default:
		return false
	}
	closing, err := decoder.Token()
	if err != nil {
		return false
	}
	_, ok := closing.(json.Delim)
	return ok
}

// flatten turns the validator's error tree into problems, one per failing
// keyword, each at the instance it failed on and said in a fixed sentence. A
// node that only says "the subschema over there failed" is structure, and
// its causes are what is reported.
func flatten(e *jsonschema.ValidationError, out []Problem) []Problem {
	_, indirection := e.ErrorKind.(*kind.Reference)
	if !indirection && (len(e.ErrorKind.KeywordPath()) > 0 || len(e.Causes) == 0) {
		at := jsonPointer(e.InstanceLocation)
		switch typed := e.ErrorKind.(type) {
		case *kind.Required:
			for _, missing := range typed.Missing {
				out = append(out, Problem{Pointer: at + "/" + escapePointerToken(missing), Message: problemRequired})
			}
		case *kind.AdditionalProperties:
			for _, extra := range typed.Properties {
				out = append(out, Problem{Pointer: at + "/" + escapePointerToken(extra), Message: problemAdditional})
			}
		case *kind.Type:
			out = append(out, Problem{Pointer: at, Message: problemType})
		case *kind.Enum, *kind.Const:
			out = append(out, Problem{Pointer: at, Message: problemEnum})
		case *kind.Minimum:
			out = append(out, Problem{Pointer: at, Message: problemMinimum})
		case *kind.MinLength:
			out = append(out, Problem{Pointer: at, Message: problemMinLength})
		default:
			out = append(out, Problem{Pointer: at, Message: problemSchema})
		}
	}
	for _, cause := range e.Causes {
		out = flatten(cause, out)
	}
	return out
}

// span is a checked range and where it was written.
type span struct {
	start, end int
	pointer    string
}

// rangeOf checks one range against the source: 0 <= start < end <= len, both
// on a character boundary.
func rangeOf(source []byte, pointer string, value map[string]any) (span, *Problem) {
	start := offset(value["start"], len(source))
	end := offset(value["end"], len(source))
	switch {
	case start >= end:
		return span{}, &Problem{Pointer: pointer, Message: problemEmptyRange}
	case end > len(source):
		return span{}, &Problem{Pointer: pointer, Message: problemPastEnd}
	case !utf8.RuneStart(source[start]):
		return span{}, &Problem{Pointer: pointer, Message: problemStartSplits}
	case end < len(source) && !utf8.RuneStart(source[end]):
		return span{}, &Problem{Pointer: pointer, Message: problemEndSplits}
	}
	return span{start: start, end: end, pointer: pointer}, nil
}

// offset reads a schema-checked non-negative integer, which may be written as
// 3, 3.0 or 3e0. Anything past length answers length+1, which is past the
// end of the source whatever its size, so an offset too large for an int
// cannot wrap around.
func offset(value any, length int) int {
	past := length + 1
	number, ok := value.(json.Number)
	if !ok {
		return past
	}
	rational, ok := new(big.Rat).SetString(string(number))
	if !ok || !rational.IsInt() || rational.Num().Cmp(big.NewInt(int64(past))) > 0 {
		return past
	}
	return int(rational.Num().Int64())
}

// overlapping reports every stage or unrecognized region that starts before
// one that started earlier has ended. Touching is not overlapping.
func overlapping(spans []span) []Problem {
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})
	var problems []Problem
	furthest := -1
	for _, current := range spans {
		if current.start < furthest {
			problems = append(problems, Problem{Pointer: current.pointer, Message: problemOverlap})
		}
		if current.end > furthest {
			furthest = current.end
		}
	}
	return problems
}

// build makes the Result from a document every rule passed, its stages and
// regions sorted by start.
func build(root map[string]any) *Result {
	result := &Result{Contract: 1, Status: ParserStatus(root["status"].(string))}
	result.Stages = make([]ParserStage, 0, len(root["stages"].([]any)))
	for _, value := range root["stages"].([]any) {
		stage := value.(map[string]any)
		result.Stages = append(result.Stages, ParserStage{
			Type:          ParserStageType(stage["type"].(string)),
			Range:         rangeValue(stage["range"]),
			Configuration: ParserStageConfiguration(stage["configuration"].(map[string]any)),
		})
	}
	result.Unrecognized = make([]ParserUnrecognizedRegion, 0, len(root["unrecognized"].([]any)))
	for _, value := range root["unrecognized"].([]any) {
		region := value.(map[string]any)
		built := ParserUnrecognizedRegion{Range: rangeValue(region["range"])}
		if reason, present := region["reason"].(string); present {
			built.Reason = &reason
		}
		result.Unrecognized = append(result.Unrecognized, built)
	}
	result.Warnings = make([]ParserWarning, 0, len(root["warnings"].([]any)))
	for _, value := range root["warnings"].([]any) {
		warning := value.(map[string]any)
		built := ParserWarning{Message: warning["message"].(string)}
		if at, present := warning["range"]; present {
			parsed := rangeValue(at)
			built.Range = &parsed
		}
		result.Warnings = append(result.Warnings, built)
	}
	sort.SliceStable(result.Stages, func(i, j int) bool { return result.Stages[i].Range.Start < result.Stages[j].Range.Start })
	sort.SliceStable(result.Unrecognized, func(i, j int) bool {
		return result.Unrecognized[i].Range.Start < result.Unrecognized[j].Range.Start
	})
	return result
}

func rangeValue(value any) ParserRange {
	object := value.(map[string]any)
	start := offset(object["start"], MaxSource)
	end := offset(object["end"], MaxSource)
	return ParserRange{Start: start, End: end}
}

// sorted orders problems by pointer and then message, without repeats, so
// two validations of the same output answer the same list.
func sorted(problems []Problem) []Problem {
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Pointer != problems[j].Pointer {
			return problems[i].Pointer < problems[j].Pointer
		}
		return problems[i].Message < problems[j].Message
	})
	out := problems[:0]
	for index, problem := range problems {
		if index > 0 && problem == problems[index-1] {
			continue
		}
		out = append(out, problem)
	}
	return out
}

func jsonPointer(tokens []string) string {
	var out strings.Builder
	for _, token := range tokens {
		out.WriteString("/")
		out.WriteString(escapePointerToken(token))
	}
	return out.String()
}

// escapePointerToken escapes one JSON Pointer reference token, per RFC 6901.
func escapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
