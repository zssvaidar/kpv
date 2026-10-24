package pipeline

// YAML is the on-disk format of a pipeline, and the single source of truth for
// what a pipeline is, is a JSON Schema. So YAML is not validated on its own
// terms: a document is converted into the JSON data model first and then
// handed to the same validator and the same generated model the JSON side
// uses. One schema, one validator, two formats.
//
// The parser is gopkg.in/yaml.v3, chosen for how it behaves on the two
// documents a naive bridge gets wrong. Measured, not remembered:
//
//	duplicate key, "id: a\nlanguage: python\nid: b\n"
//	  yaml.v3, into a Go value  -> error, `mapping key "id" already defined at line 1`
//	  sigs.k8s.io/yaml YAMLToJSON -> no error, {"id":"b"}, the last one silently wins
//
//	empty file and a comments-only file
//	  yaml.v3 Decoder.Decode    -> io.EOF, so "no document" is distinguishable
//	  yaml.v3 Unmarshal         -> no error, nil, which reads as an empty pipeline
//	  sigs.k8s.io/yaml YAMLToJSON -> no error, "null"
//
// So: yaml.v3 through a Decoder, never Unmarshal. Duplicate keys are rejected
// here as well, by the walk below rather than by the library, because that is
// what gives the offending key a JSON Pointer and a line instead of a string;
// TestTheYAMLLibraryRejectsDuplicateKeysToo pins the library's own behaviour so
// a swap to a permissive parser cannot pass unnoticed.
//
// Comments are dropped: a pipeline file is written from the model, and the
// model has nowhere to keep them. TestCommentsAreDroppedOnWrite says so on
// purpose.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// maxAliasDepth bounds how far an anchor may point at another anchor, so a
// self-referential document cannot be expanded until the agent runs out of
// memory.
const maxAliasDepth = 64

// yamlIndent is the indentation of every file this package writes. It is fixed
// because the bytes have to be reproducible.
const yamlIndent = 2

// ParseYAML validates a YAML pipeline document against the pipeline schema and
// decodes it into the generated model together with its typed processing
// steps. A document that fails anywhere is not decoded at all, and the failure
// is a *FileError naming what and where.
func ParseYAML(data []byte) (*Pipeline, []Step, error) {
	encoded, err := YAMLToJSON(data)
	if err != nil {
		return nil, nil, err
	}

	parsed, steps, err := Parse(encoded)
	if err != nil {
		var problems ValidationErrors
		if errors.As(err, &problems) {
			return nil, nil, &FileError{
				Kind:     KindSchema,
				Message:  "the document does not match the pipeline schema",
				Location: problems[0].InstanceLocation,
				Problems: problems,
				Err:      problems,
			}
		}
		return nil, nil, &FileError{
			Kind:    KindModel,
			Message: "the document could not be decoded into the pipeline model",
			Err:     err,
		}
	}
	return parsed, steps, nil
}

// MarshalYAML renders a pipeline as the bytes of a pipeline file.
//
// The pipeline is validated first, so an invalid pipeline cannot be written at
// all. Key order is the generated model's field order, and the only map in the
// model is a processing step's raw form, whose keys encoding/json sorts; there
// is no map iteration anywhere in the output, so the same pipeline always
// produces the same bytes, in this process and in the next one.
func MarshalYAML(p *Pipeline) ([]byte, error) {
	if p == nil {
		return nil, &FileError{Kind: KindModel, Message: "there is no pipeline to write"}
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, &FileError{Kind: KindModel, Message: "the pipeline could not be encoded", Err: err}
	}

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, &FileError{Kind: KindModel, Message: "the encoded pipeline is not JSON", Err: err}
	}
	problems, err := Validate(document)
	if err != nil {
		return nil, &FileError{Kind: KindSchema, Message: "the pipeline could not be validated", Err: err}
	}
	if len(problems) > 0 {
		return nil, &FileError{
			Kind:     KindSchema,
			Message:  "the pipeline does not match the pipeline schema, so it was not written",
			Location: problems[0].InstanceLocation,
			Problems: problems,
			Err:      problems,
		}
	}

	out, err := JSONToYAML(encoded)
	if err != nil {
		return nil, &FileError{Kind: KindModel, Message: "the pipeline could not be rendered as YAML", Err: err}
	}
	return out, nil
}

// YAMLToJSON converts one YAML pipeline document into the JSON bytes the
// schema validator and the generated model read. This is the bridge the
// pipeline file crosses: nothing downstream of it knows the file was YAML.
//
// It is strict where YAML is permissive. A file with no document in it, a file
// with more than one, a key defined twice, a key that is not a string and a
// tag outside the JSON data model are all rejected, with a *FileError naming
// the field and the line.
func YAMLToJSON(data []byte) ([]byte, error) {
	value, fault := decodeYAMLDocument(data)
	if fault != nil {
		return nil, fault
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, &FileError{
			Kind:    KindUnsupportedValue,
			Message: "the document cannot be represented as JSON",
			Err:     err,
		}
	}
	return encoded, nil
}

// decodeYAMLDocument parses exactly one YAML document out of data and converts
// it to the JSON data model.
func decodeYAMLDocument(data []byte) (any, *FileError) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, emptyDocumentFault()
		}
		return nil, syntaxFault(err)
	}

	// A pipeline file holds one pipeline. A second document would otherwise be
	// silently ignored, which is the same class of mistake as a duplicate key.
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case err == nil:
		return nil, &FileError{
			Kind:    KindMultipleDocuments,
			Line:    extra.Line,
			Column:  extra.Column,
			Message: "the file holds more than one YAML document, and a pipeline file holds exactly one",
		}
	case !errors.Is(err, io.EOF):
		return nil, syntaxFault(err)
	}

	root := &document
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) != 1 {
			return nil, emptyDocumentFault()
		}
		root = root.Content[0]
	}
	return yamlValue(root, "", 0)
}

// emptyDocumentFault is the failure for a file with no document in it. It is
// its own kind because the naive bridge reads such a file as an empty
// pipeline, which is the one answer it must never give.
func emptyDocumentFault() *FileError {
	return &FileError{
		Kind:    KindEmptyDocument,
		Message: "the file holds no YAML document: it is empty, or nothing but comments and whitespace",
	}
}

// yamlLine pulls the line number out of a parser message, which is the only
// place yaml.v3 reports it for a syntax error.
var yamlLine = regexp.MustCompile(`line (\d+):`)

// syntaxFault turns a parser error into a structured failure, keeping the
// parser's own wording as the message and its line number when it gave one.
func syntaxFault(err error) *FileError {
	fault := &FileError{
		Kind:    KindSyntax,
		Message: "the file is not well-formed YAML",
		Err:     err,
	}
	if match := yamlLine.FindStringSubmatch(err.Error()); match != nil {
		if line, convErr := strconv.Atoi(match[1]); convErr == nil {
			fault.Line = line
		}
	}
	return fault
}

// yamlValue converts one node into the JSON data model. location is the JSON
// Pointer of the node inside the document, so every failure can name the field
// it is about.
func yamlValue(node *yaml.Node, location string, depth int) (any, *FileError) {
	switch node.Kind {
	case yaml.AliasNode:
		if node.Alias == nil {
			return nil, &FileError{
				Kind: KindSyntax, Location: location, Line: node.Line, Column: node.Column,
				Message: fmt.Sprintf("the alias *%s points at no anchor", node.Value),
			}
		}
		if depth >= maxAliasDepth {
			return nil, &FileError{
				Kind: KindUnsupportedValue, Location: location, Line: node.Line, Column: node.Column,
				Message: fmt.Sprintf("aliases are nested more than %d deep", maxAliasDepth),
			}
		}
		return yamlValue(node.Alias, location, depth+1)

	case yaml.ScalarNode:
		return yamlScalar(node, location)

	case yaml.SequenceNode:
		// Order is the document's order, kept as given: for processingSteps it
		// is the order the pipeline runs in.
		items := make([]any, 0, len(node.Content))
		for index, child := range node.Content {
			item, fault := yamlValue(child, location+"/"+strconv.Itoa(index), depth)
			if fault != nil {
				return nil, fault
			}
			items = append(items, item)
		}
		return items, nil

	case yaml.MappingNode:
		return yamlMapping(node, location, depth)

	default:
		return nil, &FileError{
			Kind: KindSyntax, Location: location, Line: node.Line, Column: node.Column,
			Message: "the document contains a YAML node that is not a value",
		}
	}
}

// yamlMapping converts a mapping, rejecting a key that is defined twice and a
// key that is not a string.
func yamlMapping(node *yaml.Node, location string, depth int) (any, *FileError) {
	object := make(map[string]any, len(node.Content)/2)
	definedOn := make(map[string]int, len(node.Content)/2)

	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key, fault := mappingKey(keyNode, location)
		if fault != nil {
			return nil, fault
		}
		field := location + "/" + escapePointerToken(key)

		if line, duplicate := definedOn[key]; duplicate {
			return nil, &FileError{
				Kind: KindDuplicateKey, Location: field, Line: keyNode.Line, Column: keyNode.Column,
				Message: fmt.Sprintf(
					"the key %q is defined twice, already on line %d; which value was meant is not something to guess",
					key, line),
			}
		}
		definedOn[key] = keyNode.Line

		value, fault := yamlValue(valueNode, field, depth)
		if fault != nil {
			return nil, fault
		}
		object[key] = value
	}
	return object, nil
}

// mappingKey reads a mapping key as a string. YAML allows a key to be any
// node; JSON object keys are strings, so anything else is rejected rather than
// coerced, including the merge key, which would otherwise smuggle in fields
// from an anchor.
func mappingKey(node *yaml.Node, location string) (string, *FileError) {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		return node.Value, nil
	}
	return "", &FileError{
		Kind: KindUnsupportedValue, Location: location, Line: node.Line, Column: node.Column,
		Message: fmt.Sprintf("the mapping key %q is not a string (%s), and a pipeline field is named by a string",
			node.Value, node.Tag),
	}
}

// yamlScalar converts a scalar according to the tag the parser resolved, so
// the JSON type a value ends up with is the YAML type it was written as. A tag
// outside the JSON data model is rejected instead of being flattened into a
// string.
func yamlScalar(node *yaml.Node, location string) (any, *FileError) {
	unsupported := func(message string) *FileError {
		return &FileError{
			Kind: KindUnsupportedValue, Location: location, Line: node.Line, Column: node.Column,
			Message: message,
		}
	}

	switch node.Tag {
	case "!!str":
		return node.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		var value bool
		if err := node.Decode(&value); err != nil {
			return nil, unsupported(fmt.Sprintf("%q is not a boolean", node.Value))
		}
		return value, nil
	case "!!int":
		var value int64
		if err := node.Decode(&value); err == nil {
			return value, nil
		}
		var unsigned uint64
		if err := node.Decode(&unsigned); err == nil {
			return unsigned, nil
		}
		return nil, unsupported(fmt.Sprintf("the integer %q does not fit in 64 bits", node.Value))
	case "!!float":
		var value float64
		if err := node.Decode(&value); err != nil {
			return nil, unsupported(fmt.Sprintf("%q is not a number", node.Value))
		}
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, unsupported(fmt.Sprintf("%q has no JSON equivalent", node.Value))
		}
		return value, nil
	default:
		return nil, unsupported(fmt.Sprintf("the YAML tag %s has no JSON equivalent", node.Tag))
	}
}

// JSONToYAML renders a JSON document as YAML, keeping the order the keys
// appear in. It is the other half of the bridge, and it is what makes a saved
// pipeline reproducible: it walks the token stream rather than unmarshalling into a map,
// because a map would hand the encoder its keys in whatever order it felt
// like: Go randomises map iteration per process, and that is exactly the
// difference that would make two runs disagree byte for byte.
func JSONToYAML(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	node, err := yamlNodeFromJSON(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the encoded pipeline has trailing content")
	}

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(yamlIndent)
	if err := encoder.Encode(node); err != nil {
		return nil, fmt.Errorf("encode YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode YAML: %w", err)
	}
	return out.Bytes(), nil
}

// yamlNodeFromJSON reads one JSON value off the stream as a YAML node. Tags
// are set explicitly so the emitter quotes a string that would otherwise read
// back as a number or a boolean.
func yamlNodeFromJSON(decoder *json.Decoder) (*yaml.Node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("read the encoded pipeline: %w", err)
	}

	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("read the encoded pipeline: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("the encoded pipeline has a non-string key %v", keyToken)
				}
				child, err := yamlNodeFromJSON(decoder)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content, yamlScalarNode("!!str", key), child)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, fmt.Errorf("read the encoded pipeline: %w", err)
			}
			return node, nil
		case '[':
			node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for decoder.More() {
				child, err := yamlNodeFromJSON(decoder)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content, child)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, fmt.Errorf("read the encoded pipeline: %w", err)
			}
			return node, nil
		}
		return nil, fmt.Errorf("the encoded pipeline has an unexpected %v", value)
	case string:
		return yamlScalarNode("!!str", value), nil
	case json.Number:
		if strings.ContainsAny(value.String(), ".eE") {
			return yamlScalarNode("!!float", value.String()), nil
		}
		return yamlScalarNode("!!int", value.String()), nil
	case bool:
		return yamlScalarNode("!!bool", strconv.FormatBool(value)), nil
	case nil:
		return yamlScalarNode("!!null", "null"), nil
	default:
		return nil, fmt.Errorf("the encoded pipeline has an unexpected %T", token)
	}
}

// yamlScalarNode is one scalar with its tag stated.
func yamlScalarNode(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}
