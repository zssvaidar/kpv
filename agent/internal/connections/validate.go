package connections

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// schemaSource is the copy of schema/connection.schema.json that generation
// writes into this package (scripts/generate-schema.mjs, extraSchemaCopies).
//
//go:embed connection.schema.json
var schemaSource []byte

const schemaID = "https://github.com/zssvaidar/kpv/schema/connection.schema.json"

// Problem is one way a connection, or a request about one, is not valid: a
// JSON Pointer to the field it is about, and a sentence that never quotes a
// credential or a certificate. Keyword is the schema keyword that refused it
// (an absolute pointer into connection.schema.json), or Rule the combination
// rule (rules.go); exactly one of them is set.
type Problem struct {
	Pointer string
	Keyword string
	Rule    string
	Message string
}

type compiled struct {
	definitions map[string]*jsonschema.Schema
}

var (
	compileOnce sync.Once
	compiledSet *compiled
	compileErr  error
)

// The definitions a document is validated against.
const (
	definitionConnection = "connection"
	definitionUpdate     = "connectionUpdate"
	definitionDelete     = "connectionDelete"
)

func schemas() (*compiled, error) {
	compileOnce.Do(func() {
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaSource))
		if err != nil {
			compileErr = fmt.Errorf("decode the connection schema: %w", err)
			return
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource(schemaID, document); err != nil {
			compileErr = fmt.Errorf("add the connection schema: %w", err)
			return
		}
		set := &compiled{definitions: map[string]*jsonschema.Schema{}}
		for _, name := range []string{definitionConnection, definitionUpdate, definitionDelete} {
			schema, err := compiler.Compile(schemaID + "#/$defs/" + name)
			if err != nil {
				compileErr = fmt.Errorf("compile the connection schema's %s: %w", name, err)
				return
			}
			set.definitions[name] = schema
		}
		compiledSet = set
	})
	return compiledSet, compileErr
}

// validateAgainst reports every way value, a decoded JSON document, fails
// the named definition of the schema. Messages name the rule, never the
// value: the validator's own wording can quote a string, and a string here
// can be a certificate.
func validateAgainst(definition string, value any) ([]Problem, error) {
	set, err := schemas()
	if err != nil {
		return nil, err
	}
	err = set.definitions[definition].Validate(value)
	if err == nil {
		return nil, nil
	}
	var invalid *jsonschema.ValidationError
	if !errors.As(err, &invalid) {
		return nil, fmt.Errorf("validate a connection: %w", err)
	}
	return flatten(invalid, nil), nil
}

// ValidateRecord reports every way value, a decoded JSON document, fails as
// a connection record: the schema, then the combination rules. It does not
// look inside PEM text; the store does that when it takes a connection.
func ValidateRecord(value any) ([]Problem, error) {
	found, err := validateAgainst(definitionConnection, value)
	if err != nil {
		return nil, err
	}
	if object, ok := value.(map[string]any); ok {
		found = append(found, ruleProblems(present(object))...)
	}
	return found, nil
}

func flatten(e *jsonschema.ValidationError, out []Problem) []Problem {
	keywordPath := e.ErrorKind.KeywordPath()
	_, indirection := e.ErrorKind.(*kind.Reference)
	if !indirection && (len(keywordPath) > 0 || len(e.Causes) == 0) {
		found := Problem{
			Pointer: pointer(e.InstanceLocation),
			Keyword: schemaPointer(e.SchemaURL) + pointer(keywordPath),
			Message: messageOf(e.ErrorKind),
		}
		duplicate := false
		for _, existing := range out {
			if existing.Pointer == found.Pointer && existing.Keyword == found.Keyword {
				duplicate = true
			}
		}
		if !duplicate {
			out = append(out, found)
		}
	}
	for _, cause := range e.Causes {
		out = flatten(cause, out)
	}
	return out
}

var englishPrinter = message.NewPrinter(language.English)

// messageOf is the validator's message for the kinds that never quote the
// value, and a fixed sentence for the ones that can (a pattern or an enum
// mismatch, a type mismatch, an unknown key).
func messageOf(errorKind jsonschema.ErrorKind) string {
	switch errorKind.(type) {
	case *kind.Pattern:
		return "this is not in the expected form"
	case *kind.Enum, *kind.Const:
		return "this is not one of the allowed values"
	case *kind.Type:
		return "this has the wrong type"
	case *kind.AdditionalProperties:
		return "this holds a field it does not have"
	case *kind.UniqueItems:
		return "this list holds the same value twice"
	default:
		return errorKind.LocalizedString(englishPrinter)
	}
}

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

func pointer(tokens []string) string {
	var out strings.Builder
	for _, token := range tokens {
		out.WriteString("/")
		out.WriteString(strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1"))
	}
	return out.String()
}
