package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
)

// The pipeline endpoints (#48): list every pipeline under the authorized
// roots, open one by its root and directory, and create one. They are
// registered by newHandler beside the root endpoints, inside the same wrap,
// for the reason directoriesPattern gives: they need state the guard carries
// and answer structured errors. Every shape they take and answer is
// generated from schema/pipelines.schema.json.
//
// POST changes state, so #58's relaxation does not reach it, as for
// rootsAuthorizePattern.
const (
	pipelinesListPattern   = "GET /api/pipelines"
	pipelinesOpenPattern   = "GET /api/pipelines/open"
	pipelinesCreatePattern = "POST /api/pipelines"
)

// The open route's query parameters. directory is left out for a root that
// is itself the pipeline; present and empty is invalid.
const (
	rootParameter      = "root"
	directoryParameter = "directory"
)

// maxCreateRequestBytes caps a create body: a root path of at most 4096
// bytes, a name of at most 256 characters and three short values.
const maxCreateRequestBytes = 8192

// pipelinesFailure is the whole of a failed pipeline request's answer: a
// status, a kind and a fixed sentence.
type pipelinesFailure struct {
	status  int
	message string
}

// pipelinesFailures is what each kind answers. Nothing from an underlying
// error is passed on, and no path but the root and directory the request
// named; the one addition is the document's own problems, which describe what
// is in pipeline.yaml (or would be), not the disk.
var pipelinesFailures = map[PipelinesErrorKind]pipelinesFailure{
	PipelinesErrorKindInvalid: {http.StatusBadRequest,
		"name a listed root and one directory name in it, or send a create request whose fields make a valid pipeline"},
	PipelinesErrorKindNotAuthorized: {http.StatusForbidden,
		"this is not an authorized project directory"},
	PipelinesErrorKindRootUnavailable: {http.StatusConflict,
		"this project directory is unavailable"},
	PipelinesErrorKindNotFound: {http.StatusNotFound,
		"there is no pipeline in this directory"},
	PipelinesErrorKindUnreadable: {http.StatusUnprocessableEntity,
		"this pipeline's pipeline.yaml did not load"},
	PipelinesErrorKindRootIsPipeline: {http.StatusConflict,
		"this project directory is itself a pipeline, so a pipeline cannot be created inside it"},
	PipelinesErrorKindChanged: {http.StatusConflict,
		"the new pipeline's directory was replaced before it was written into, so nothing was written"},
	PipelinesErrorKindNoFreeID: {http.StatusConflict,
		"every id this name gives is already taken"},
	PipelinesErrorKindIo: {http.StatusInternalServerError,
		"the pipeline could not be read or written"},
}

// pipelinesListRoute answers GET /api/pipelines.
func (g *guard) pipelinesListRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if g.pipelines == nil {
			reject(w)
			return
		}
		writeJSONStatus(w, http.StatusOK, pipelineListOf(g.pipelines.List()))
	}
}

// pipelinesOpenRoute answers GET /api/pipelines/open?root=...&directory=...
func (g *guard) pipelinesOpenRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.pipelines == nil {
			reject(w)
			return
		}
		location, ok := readOpenQuery(r)
		if !ok {
			writePipelinesError(w, &catalog.Error{Kind: catalog.KindInvalid})
			return
		}
		document, err := g.pipelines.Open(location)
		if err != nil {
			g.logger.Debug("a pipeline was not opened", "kind", string(catalog.KindOf(err)))
			writePipelinesError(w, err)
			return
		}
		answer, ok := documentOf(document)
		if !ok {
			writePipelinesError(w, &catalog.Error{Kind: catalog.KindIO})
			return
		}
		writeJSONStatus(w, http.StatusOK, answer)
	}
}

// pipelinesCreateRoute answers POST /api/pipelines.
func (g *guard) pipelinesCreateRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.pipelines == nil {
			reject(w)
			return
		}
		request, ok := readCreateRequest(r)
		if !ok {
			writePipelinesError(w, &catalog.Error{Kind: catalog.KindInvalid})
			return
		}
		document, err := g.pipelines.Create(request)
		if err != nil {
			writePipelinesError(w, err)
			return
		}
		answer, ok := documentOf(document)
		if !ok {
			writePipelinesError(w, &catalog.Error{Kind: catalog.KindIO})
			return
		}
		writeJSONStatus(w, http.StatusOK, answer)
	}
}

// readOpenQuery reads the open route's query: root once, directory at most
// once, and nothing else but the token the guard reads. A directory that is
// present is a single name; catalog.Open refuses one that is not.
func readOpenQuery(r *http.Request) (catalog.Location, bool) {
	query := r.URL.Query()
	for name := range query {
		if name != rootParameter && name != directoryParameter && name != tokenParameter {
			return catalog.Location{}, false
		}
	}
	roots, directories := query[rootParameter], query[directoryParameter]
	if len(roots) != 1 || roots[0] == "" || len(directories) > 1 {
		return catalog.Location{}, false
	}
	location := catalog.Location{Root: roots[0]}
	if len(directories) == 1 {
		if directories[0] == "" {
			return catalog.Location{}, false
		}
		location.Directory = directories[0]
	}
	return location, true
}

// createFields are the create request's fields, each a string, each required
// exactly once and spelled exactly so.
var createFields = []string{"root", "name", "language", "sourceTopic", "destinationTopic", "networkMode"}

// readCreateRequest reads a CreatePipelineRequest body, or false for anything
// that is not exactly one: a Content-Type other than application/json, a body
// over the cap or not UTF-8, anything but one JSON object, a key that is not
// one of createFields exactly (a key that matches one only ignoring case is
// refused), a key given twice, a field missing or not a string. It is read
// token by token, for the reason readRootRequest gives.
func readCreateRequest(r *http.Request) (catalog.CreateRequest, bool) {
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return catalog.CreateRequest{}, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCreateRequestBytes+1))
	if err != nil || len(body) > maxCreateRequestBytes || !utf8.Valid(body) {
		return catalog.CreateRequest{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return catalog.CreateRequest{}, false
	}
	known := map[string]bool{}
	for _, field := range createFields {
		known[field] = true
	}
	values := map[string]string{}
	for decoder.More() {
		key, err := decoder.Token()
		name, isString := key.(string)
		if err != nil || !isString || !known[name] {
			return catalog.CreateRequest{}, false
		}
		if _, seen := values[name]; seen {
			return catalog.CreateRequest{}, false
		}
		value, err := decoder.Token()
		text, isString := value.(string)
		if err != nil || !isString {
			return catalog.CreateRequest{}, false
		}
		values[name] = text
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return catalog.CreateRequest{}, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return catalog.CreateRequest{}, false
	}
	if len(values) != len(createFields) || values["root"] == "" {
		return catalog.CreateRequest{}, false
	}
	return catalog.CreateRequest{
		Root: values["root"], Name: values["name"], Language: values["language"],
		SourceTopic: values["sourceTopic"], DestinationTopic: values["destinationTopic"], NetworkMode: values["networkMode"],
	}, true
}

// writePipelinesError answers a failed pipeline request from the *catalog.Error
// err is: its kind's status and sentence, and the details the schema lets a
// PipelinesError carry.
func writePipelinesError(w http.ResponseWriter, err error) {
	var fault *catalog.Error
	if !errors.As(err, &fault) {
		fault = &catalog.Error{Kind: catalog.KindIO}
	}
	kind := PipelinesErrorKind(fault.Kind)
	failure, known := pipelinesFailures[kind]
	if !known {
		kind = PipelinesErrorKindIo
		failure = pipelinesFailures[kind]
	}
	answer := PipelinesError{Error: kind, Message: failure.message}
	switch kind {
	case PipelinesErrorKindRootUnavailable:
		reason := rootReasonOf(string(fault.Reason))
		answer.Reason = &reason
	case PipelinesErrorKindUnreadable:
		unreadable := unreadableOf(fault.Unreadable)
		answer.Unreadable = &unreadable
	case PipelinesErrorKindInvalid:
		if len(fault.Problems) > 0 {
			answer.Problems = pipelineProblemsOf(fault.Problems)
		}
	}
	if fault.DirectoryLeftBehind != nil {
		left := *fault.DirectoryLeftBehind
		answer.DirectoryLeftBehind = &left
	}
	writeJSONStatus(w, failure.status, answer)
}

// pipelineListOf turns the catalog's list into the generated answer.
func pipelineListOf(list catalog.List) PipelineList {
	out := PipelineList{Roots: make([]PipelineRoot, 0, len(list.Roots))}
	for _, root := range list.Roots {
		converted := PipelineRoot{
			Path: root.Path, Available: root.Available, Truncated: root.Truncated,
			SkippedLinks: root.SkippedLinks, Pipelines: make([]PipelineEntry, 0, len(root.Pipelines)),
		}
		if !root.Available {
			reason := rootReasonOf(string(root.Reason))
			converted.Reason = &reason
		}
		for _, entry := range root.Pipelines {
			converted.Pipelines = append(converted.Pipelines, pipelineEntryOf(entry))
		}
		out.Roots = append(out.Roots, converted)
	}
	return out
}

func pipelineEntryOf(entry catalog.Entry) PipelineEntry {
	out := PipelineEntry{Root: entry.Root, Directory: directoryOf(entry.Directory), Readable: entry.Readable()}
	if !entry.Modified.IsZero() {
		modified := entry.Modified.UTC().Format(time.RFC3339Nano)
		out.Modified = &modified
	}
	if entry.Unreadable != nil {
		unreadable := unreadableOf(entry.Unreadable)
		out.Unreadable = &unreadable
	} else {
		id, language := entry.ID, entry.Language
		out.ID, out.Language = &id, &language
		if entry.Name != "" {
			name := entry.Name
			out.Name = &name
		}
	}
	for _, other := range entry.Duplicates {
		out.DuplicateID = append(out.DuplicateID, PipelineLocation{Root: other.Root, Directory: directoryOf(other.Directory)})
	}
	return out
}

// directoryOf is a location's directory as the schema has it: absent for a
// root that is itself the pipeline.
func directoryOf(directory string) *string {
	if directory == "" {
		return nil
	}
	return &directory
}

func unreadableOf(unreadable *catalog.Unreadable) UnreadablePipeline {
	if unreadable == nil {
		return UnreadablePipeline{Kind: UnreadableKindUnreadable, Problems: []PipelineProblem{}}
	}
	return UnreadablePipeline{Kind: UnreadableKind(unreadable.Kind), Problems: pipelineProblemsOf(unreadable.Problems)}
}

func pipelineProblemsOf(problems []catalog.Problem) []PipelineProblem {
	out := make([]PipelineProblem, 0, len(problems))
	for _, problem := range problems {
		converted := PipelineProblem{Pointer: problem.Pointer, Message: problem.Message}
		if converted.Message == "" {
			converted.Message = "this value is not valid"
		}
		if problem.Line > 0 {
			line := problem.Line
			converted.Line = &line
		}
		if problem.Column > 0 {
			column := problem.Column
			converted.Column = &column
		}
		out = append(out, converted)
	}
	return out
}

// rootReasonOf maps a root's reason onto the schema's, io for anything the
// schema does not name.
func rootReasonOf(kind string) PipelineRootReason {
	for _, known := range enumValues_PipelineRootReason {
		if kind == known {
			return PipelineRootReason(kind)
		}
	}
	return PipelineRootReasonIo
}

// documentOf is the generated answer for an opened or created pipeline. The
// JSON is decoded into the plain object the schema gives the pipeline, which
// is all this package knows of it.
func documentOf(document *catalog.Document) (PipelineDocument, bool) {
	var object map[string]interface{}
	if err := json.Unmarshal(document.JSON, &object); err != nil || object == nil {
		return PipelineDocument{}, false
	}
	return PipelineDocument{
		Root: document.Root, Directory: directoryOf(document.Directory),
		Pipeline: PipelineDocumentPipeline(object), Sha256: document.SHA256,
	}, true
}
