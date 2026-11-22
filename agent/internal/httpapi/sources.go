package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
)

// The source endpoints (#87): read one source file of a listed pipeline, by
// the pipeline's root and directory as the open route takes them and the
// file's path inside the pipeline directory, and write it back. They are
// registered by newHandler beside the pipeline endpoints, inside the same
// wrap, for the reason directoriesPattern gives, and go through the same
// *catalog.Catalog. Every shape they take and answer is generated from
// schema/source.schema.json.
//
// PUT changes state, so #58's relaxation does not reach it, as for
// rootsAuthorizePattern.
const (
	sourceReadPattern  = "GET /api/pipelines/source"
	sourceWritePattern = "PUT /api/pipelines/source"
)

// maxSourceRequestBytes caps a write body: a text of catalog.MaxFileSize
// bytes, which JSON escaping can make at most six times longer (\u00XX), and
// 16384 bytes for the root, directory, path, hash and the keys. A text that
// fits the body and is over catalog.MaxFileSize is tooLarge.
const maxSourceRequestBytes = 6*catalog.MaxFileSize + 16384

// sha256Pattern is schema/source.schema.json's sourceSha256.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sourceFailure is the whole of a failed source request's answer: a status, a
// kind and a fixed sentence.
type sourceFailure struct {
	status  int
	message string
}

// sourceFailures is what each kind answers. Nothing from an underlying error
// is passed on, no path, and never the file's text.
var sourceFailures = map[SourceErrorKind]sourceFailure{
	SourceErrorKindInvalid: {http.StatusBadRequest,
		"name a listed root, its pipeline directory and a relative path inside it, and send a write with exactly one of expectedSha256 and expectedAbsent"},
	SourceErrorKindNotAuthorized: {http.StatusForbidden,
		"this is not an authorized project directory"},
	SourceErrorKindRootUnavailable: {http.StatusConflict,
		"this project directory is unavailable"},
	SourceErrorKindNotFound: {http.StatusNotFound,
		"there is no such pipeline, or no such file in it"},
	SourceErrorKindReserved: {http.StatusForbidden,
		"pipeline.yaml is the pipeline itself, not a source file"},
	SourceErrorKindGenerated: {http.StatusForbidden,
		"files under generated/ belong to the tool and are not written here"},
	SourceErrorKindLink: {http.StatusConflict,
		"a symbolic link on the way to this file, or the file itself, is never followed"},
	SourceErrorKindChanged: {http.StatusConflict,
		"the file or a directory on its path changed since it was read, so nothing was written"},
	SourceErrorKindExists: {http.StatusConflict,
		"the file already exists, so nothing was written"},
	SourceErrorKindNotRegularFile: {http.StatusUnprocessableEntity,
		"this path names a directory, FIFO, socket or device, not a file"},
	SourceErrorKindTooLarge: {http.StatusRequestEntityTooLarge,
		"the file or the text is over 1 MiB"},
	SourceErrorKindNotText: {http.StatusUnprocessableEntity,
		"the file or the text is not UTF-8 text without NUL bytes"},
	SourceErrorKindUnreadable: {http.StatusUnprocessableEntity,
		"the agent may not read or search this file or a directory on its path"},
	SourceErrorKindIo: {http.StatusInternalServerError,
		"the file could not be read or written"},
}

// sourceReadRoute answers GET /api/pipelines/source?root=...&directory=...&path=...
func (g *guard) sourceReadRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.pipelines == nil {
			reject(w)
			return
		}
		location, path, ok := readSourceQuery(r)
		if !ok {
			writeSourceError(w, &catalog.Error{Kind: catalog.KindInvalid})
			return
		}
		file, err := g.pipelines.ReadSource(location, path)
		if err != nil {
			g.logger.Debug("a source file was not read", "kind", string(catalog.KindOf(err)))
			writeSourceError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusOK, SourceFile{
			Root: file.Root, Directory: directoryOf(file.Directory), Path: SourcePath(file.Path),
			Text: string(file.Text), Sha256: SourceSha256(file.SHA256), Generated: file.Generated,
		})
	}
}

// sourceWriteRoute answers PUT /api/pipelines/source.
func (g *guard) sourceWriteRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.pipelines == nil {
			reject(w)
			return
		}
		write, ok := readSourceWriteRequest(r)
		if !ok {
			writeSourceError(w, &catalog.Error{Kind: catalog.KindInvalid})
			return
		}
		sum, err := g.pipelines.WriteSource(write)
		if err != nil {
			g.logger.Debug("a source file was not written", "kind", string(catalog.KindOf(err)))
			writeSourceError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusOK, SourceWritten{
			Root: write.Root, Directory: directoryOf(write.Directory), Path: SourcePath(write.Path), Sha256: SourceSha256(sum),
		})
	}
}

// readSourceQuery reads the read route's query: root and path once each,
// directory at most once and not empty, and nothing else but the token the
// guard reads. path is the same parameter name as the directory browsing
// endpoint's (pathParameter). catalog.ReadSource checks the path.
func readSourceQuery(r *http.Request) (catalog.Location, string, bool) {
	query := r.URL.Query()
	for name := range query {
		if name != rootParameter && name != directoryParameter && name != pathParameter && name != tokenParameter {
			return catalog.Location{}, "", false
		}
	}
	roots, directories, paths := query[rootParameter], query[directoryParameter], query[pathParameter]
	if len(roots) != 1 || roots[0] == "" || len(directories) > 1 || len(paths) != 1 || paths[0] == "" {
		return catalog.Location{}, "", false
	}
	location := catalog.Location{Root: roots[0]}
	if len(directories) == 1 {
		if directories[0] == "" {
			return catalog.Location{}, "", false
		}
		location.Directory = directories[0]
	}
	return location, paths[0], true
}

// readSourceWriteRequest reads a SourceWriteRequest body, or false for
// anything that is not exactly one: a Content-Type other than
// application/json, a body over maxSourceRequestBytes (not read further) or
// not UTF-8, anything but one JSON object, a key that is not one of the six
// exactly, a key given twice, root, path or text missing, a field of the
// wrong type, an empty root or directory, an expectedSha256 that is not a
// lowercase hex SHA-256, an expectedAbsent that is not true, or neither or
// both of those two. It is read token by token, for the reason
// readRootRequest gives.
func readSourceWriteRequest(r *http.Request) (catalog.SourceWrite, bool) {
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return catalog.SourceWrite{}, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSourceRequestBytes+1))
	if err != nil || len(body) > maxSourceRequestBytes || !utf8.Valid(body) {
		return catalog.SourceWrite{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return catalog.SourceWrite{}, false
	}
	fields := map[string]string{}
	absent, sawAbsent := false, false
	for decoder.More() {
		key, err := decoder.Token()
		name, isString := key.(string)
		if err != nil || !isString {
			return catalog.SourceWrite{}, false
		}
		value, err := decoder.Token()
		if err != nil {
			return catalog.SourceWrite{}, false
		}
		switch name {
		case "root", "directory", "path", "text", "expectedSha256":
			text, isString := value.(string)
			if _, seen := fields[name]; seen || !isString {
				return catalog.SourceWrite{}, false
			}
			fields[name] = text
		case "expectedAbsent":
			flag, isBool := value.(bool)
			if sawAbsent || !isBool || !flag {
				return catalog.SourceWrite{}, false
			}
			absent, sawAbsent = true, true
		default:
			return catalog.SourceWrite{}, false
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return catalog.SourceWrite{}, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return catalog.SourceWrite{}, false
	}
	root, hasRoot := fields["root"]
	path, hasPath := fields["path"]
	text, hasText := fields["text"]
	directory, hasDirectory := fields["directory"]
	sum, hasSum := fields["expectedSha256"]
	switch {
	case !hasRoot || root == "" || !hasPath || !hasText || (hasDirectory && directory == ""):
		return catalog.SourceWrite{}, false
	case hasSum == absent, hasSum && !sha256Pattern.MatchString(sum):
		return catalog.SourceWrite{}, false
	}
	return catalog.SourceWrite{
		Location: catalog.Location{Root: root, Directory: directory},
		Path:     path, Text: text, ExpectedSHA256: sum, ExpectedAbsent: absent,
	}, true
}

// writeSourceError answers a failed source request from the *catalog.Error
// err is: its kind's status and sentence, the root's reason for
// rootUnavailable, and the current hash for a changed file.
func writeSourceError(w http.ResponseWriter, err error) {
	var fault *catalog.Error
	if !errors.As(err, &fault) {
		fault = &catalog.Error{Kind: catalog.KindIO}
	}
	kind := SourceErrorKind(fault.Kind)
	failure, known := sourceFailures[kind]
	if !known {
		kind = SourceErrorKindIo
		failure = sourceFailures[kind]
	}
	answer := SourceError{Error: kind, Message: failure.message}
	switch {
	case kind == SourceErrorKindRootUnavailable:
		reason := SourceRootReason(rootReasonOf(string(fault.Reason)))
		answer.Reason = &reason
	case kind == SourceErrorKindChanged && sha256Pattern.MatchString(fault.SHA256):
		sum := SourceSha256(fault.SHA256)
		answer.Sha256 = &sum
	}
	writeJSONStatus(w, failure.status, answer)
}
