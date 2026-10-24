package pipeline

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// ValidationError is one way a document fails the schema, located in both the
// document and the schema so a caller can point at the offending field and say
// which rule rejected it.
type ValidationError struct {
	// InstanceLocation is a JSON Pointer into the document; "" is the root.
	InstanceLocation string
	// KeywordLocation is a JSON Pointer into the schema document, naming the
	// keyword that rejected the value: "/$defs/language/enum", not the path
	// the validator walked to reach it. The TypeScript side reports the same
	// pointer, so one shared fixture can state it once for both.
	KeywordLocation string
	// Message is a human-readable explanation, not a stable identifier.
	Message string
}

func (e ValidationError) Error() string {
	where := e.InstanceLocation
	if where == "" {
		where = "/"
	}
	return fmt.Sprintf("%s: %s (%s)", where, e.Message, e.KeywordLocation)
}

// ValidationErrors is every failure found in one document, in the order the
// validator reported them.
type ValidationErrors []ValidationError

func (errs ValidationErrors) Error() string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "; ")
}

// Validate reports every way value fails the pipeline schema. value is a
// decoded JSON document, as returned by jsonschema.UnmarshalJSON or by
// unmarshalling into an any. A nil result means the document is valid.
//
// Validation is the schema plus the x-kpv-uniqueBy annotations it carries,
// which express a rule JSON Schema itself cannot: duplicate processing-step
// identifiers within one pipeline.
func Validate(value any) (ValidationErrors, error) {
	schema, err := loadSchema()
	if err != nil {
		return nil, err
	}

	var found ValidationErrors
	if err := schema.compiled.Validate(value); err != nil {
		var invalid *jsonschema.ValidationError
		if !errors.As(err, &invalid) {
			return nil, fmt.Errorf("validate pipeline: %w", err)
		}
		found = flatten(invalid, found)
	}
	return append(found, uniqueByViolations(schema.document, value, "", "")...), nil
}

// flatten walks the validator's error tree into a flat list of failures, each
// with an absolute location in the schema.
//
// A reference, a group or any other node that only says "the subschema over
// there failed" is pure structure; its causes say what actually failed, so
// only they are reported. The same failure can be reached through several
// branches of a union, so each pair of locations is reported once.
func flatten(e *jsonschema.ValidationError, out ValidationErrors) ValidationErrors {
	keywordPath := e.ErrorKind.KeywordPath()
	_, indirection := e.ErrorKind.(*kind.Reference)
	if !indirection && (len(keywordPath) > 0 || len(e.Causes) == 0) {
		found := ValidationError{
			InstanceLocation: jsonPointer(e.InstanceLocation),
			KeywordLocation:  schemaPointer(e.SchemaURL) + jsonPointer(keywordPath),
			Message:          e.ErrorKind.LocalizedString(englishPrinter),
		}
		if !containsLocation(out, found) {
			out = append(out, found)
		}
	}
	for _, cause := range e.Causes {
		out = flatten(cause, out)
	}
	return out
}

// containsLocation reports whether the same pair of locations was already
// recorded.
func containsLocation(errs ValidationErrors, candidate ValidationError) bool {
	for _, err := range errs {
		if err.InstanceLocation == candidate.InstanceLocation &&
			err.KeywordLocation == candidate.KeywordLocation {
			return true
		}
	}
	return false
}

// englishPrinter renders the validator's messages. They are for a human to
// read; the two locations are what a caller should branch on.
var englishPrinter = message.NewPrinter(language.English)

// schemaPointer turns an absolute schema URL into a JSON Pointer into the
// schema document, which is what the fragment of that URL already is. The
// pointer is relative to the one schema this package compiles, so the URL of
// the schema itself becomes the empty pointer.
func schemaPointer(schemaURL string) string {
	_, fragment, found := strings.Cut(schemaURL, "#")
	if !found {
		return ""
	}
	unescaped, err := url.PathUnescape(fragment)
	if err != nil {
		return fragment
	}
	return unescaped
}

// jsonPointer joins already-unescaped reference tokens into a JSON Pointer.
func jsonPointer(tokens []string) string {
	var out strings.Builder
	for _, token := range tokens {
		out.WriteString("/")
		out.WriteString(escapePointerToken(token))
	}
	return out.String()
}

// uniqueByViolations applies every x-kpv-uniqueBy annotation reachable from the
// schema root through "properties". The annotation names a property of the
// array's items that has to be unique across the array.
//
// Recursion deliberately only follows "properties": the annotation is a local
// extension, and keeping its reach small keeps it readable and keeps the Go and
// TypeScript implementations identical.
func uniqueByViolations(schemaNode map[string]any, value any, keywordLocation, instanceLocation string) ValidationErrors {
	var out ValidationErrors

	if property, ok := schemaNode["x-kpv-uniqueBy"].(string); ok {
		if items, ok := value.([]any); ok {
			seen := make(map[string]int, len(items))
			for index, item := range items {
				object, ok := item.(map[string]any)
				if !ok {
					continue
				}
				key, ok := object[property].(string)
				if !ok {
					continue
				}
				first, duplicate := seen[key]
				if !duplicate {
					seen[key] = index
					continue
				}
				out = append(out, ValidationError{
					InstanceLocation: instanceLocation + "/" + strconv.Itoa(index) + "/" + escapePointerToken(property),
					KeywordLocation:  keywordLocation + "/x-kpv-uniqueBy",
					Message: fmt.Sprintf(
						"duplicate %s %q, already used at index %d", property, key, first),
				})
			}
		}
	}

	properties, ok := schemaNode["properties"].(map[string]any)
	if !ok {
		return out
	}
	object, ok := value.(map[string]any)
	if !ok {
		return out
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sub := properties[name]
		subSchema, ok := sub.(map[string]any)
		if !ok {
			continue
		}
		child, present := object[name]
		if !present {
			continue
		}
		token := escapePointerToken(name)
		out = append(out, uniqueByViolations(
			subSchema,
			child,
			keywordLocation+"/properties/"+token,
			instanceLocation+"/"+token,
		)...)
	}
	return out
}

// escapePointerToken escapes one JSON Pointer reference token, per RFC 6901.
func escapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
