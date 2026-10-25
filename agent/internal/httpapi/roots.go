package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// The project root endpoints (#63): list the authorized roots, authorize a
// directory the user picked while browsing, and revoke one. They are
// registered by newHandler beside the directory browsing endpoint, inside the
// same wrap, for the same reason that endpoint is (see directoriesPattern):
// they need state the guard carries and answer structured errors. Every shape
// they take and answer is generated from schema/roots.schema.json.
//
// POST and DELETE change state, so #58's relaxation does not reach them: it is
// GET and HEAD only, and a browser sends Origin on any other method, so these
// two need an allowed Origin like a WebSocket handshake does.
const (
	rootsListPattern      = "GET /api/roots"
	rootsAuthorizePattern = "POST /api/roots"
	rootsRevokePattern    = "DELETE /api/roots"
)

// maxRootRequestBytes caps a POST or DELETE body. A request carries one path,
// and 4096 bytes is the longest path Linux accepts, so a real request never
// comes near it.
const maxRootRequestBytes = 4096

// rootsFailure is the whole of a failed root request's answer: a status, a
// kind and a fixed sentence.
type rootsFailure struct {
	status  int
	message string
}

// rootsFailures is what each kind answers. As with directoryFailures, nothing
// from the underlying error is passed on: projectfs's errors can carry the
// path resolution reached outside the home directory, so the kind is all that
// is taken from one, and two refusals of a kind are byte-identical. The one
// addition is the stored root an overlaps refusal names, which is the user's
// own and listed by GET /api/roots anyway.
var rootsFailures = map[RootsErrorKind]rootsFailure{
	RootsErrorKindInvalid: {http.StatusBadRequest,
		"send one JSON object with one absolute path in it, as application/json and at most 4096 bytes"},
	RootsErrorKindForbidden: {http.StatusForbidden,
		"only a directory inside the home directory can be a project directory"},
	RootsErrorKindTooBroad: {http.StatusForbidden,
		"the home directory, the directories above it and / are too broad to be a project directory"},
	RootsErrorKindNotFound: {http.StatusNotFound,
		"this directory does not exist"},
	RootsErrorKindNotDirectory: {http.StatusConflict,
		"a project directory must be a directory"},
	RootsErrorKindUnreadable: {http.StatusForbidden,
		"the agent is not permitted to read and search this directory"},
	RootsErrorKindOverlaps: {http.StatusConflict,
		"this directory is inside, or contains, a project directory that is already authorized"},
	RootsErrorKindIo: {http.StatusInternalServerError,
		"the project directories could not be changed"},
}

// revokeNotFound is DELETE's notFound: the path is not a stored root, which is
// the answer whether or not a directory is there.
var revokeNotFound = rootsFailure{http.StatusNotFound, "this path is not an authorized project directory"}

// rootsErrorKindOf maps a projectfs failure onto the kind the API reports.
// Anything that is not one of projectfs's own refusals is io.
func rootsErrorKindOf(err error) RootsErrorKind {
	switch projectfs.KindOf(err) {
	case projectfs.KindInvalid:
		return RootsErrorKindInvalid
	case projectfs.KindForbidden:
		return RootsErrorKindForbidden
	case projectfs.KindTooBroad:
		return RootsErrorKindTooBroad
	case projectfs.KindNotFound:
		return RootsErrorKindNotFound
	case projectfs.KindNotDirectory:
		return RootsErrorKindNotDirectory
	case projectfs.KindUnreadable:
		return RootsErrorKindUnreadable
	case projectfs.KindOverlaps:
		return RootsErrorKindOverlaps
	default:
		return RootsErrorKindIo
	}
}

// rootsListRoute answers GET /api/roots.
func (g *guard) rootsListRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if g.roots == nil {
			reject(w)
			return
		}
		writeJSONStatus(w, http.StatusOK, rootListOf(g.roots.List()))
	}
}

// rootsAuthorizeRoute answers POST /api/roots.
func (g *guard) rootsAuthorizeRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.roots == nil {
			reject(w)
			return
		}
		path, ok := readRootRequest(r)
		if !ok {
			writeRootsError(w, RootsErrorKindInvalid, "")
			return
		}
		authorized, err := g.roots.Authorize(path)
		if err != nil {
			kind := rootsErrorKindOf(err)
			g.logger.Debug("a project directory was not authorized", "kind", string(kind))
			writeRootsError(w, kind, projectfs.OverlappedRoot(err))
			return
		}
		writeJSONStatus(w, http.StatusOK, AuthorizedRoot{Path: authorized})
	}
}

// rootsRevokeRoute answers DELETE /api/roots.
func (g *guard) rootsRevokeRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.roots == nil {
			reject(w)
			return
		}
		path, ok := readRootRequest(r)
		if !ok {
			writeRootsError(w, RootsErrorKindInvalid, "")
			return
		}
		remaining, err := g.roots.Revoke(path)
		if err != nil {
			kind := rootsErrorKindOf(err)
			g.logger.Debug("a project directory was not revoked", "kind", string(kind))
			if kind == RootsErrorKindNotFound {
				writeJSONStatus(w, revokeNotFound.status, RootsError{Error: kind, Message: revokeNotFound.message})
				return
			}
			writeRootsError(w, kind, "")
			return
		}
		writeJSONStatus(w, http.StatusOK, rootListOf(remaining))
	}
}

// writeRootsError answers a failed root request. root is sent only with an
// overlaps refusal.
func writeRootsError(w http.ResponseWriter, kind RootsErrorKind, root string) {
	failure, known := rootsFailures[kind]
	if !known {
		kind = RootsErrorKindIo
		failure = rootsFailures[kind]
	}
	answer := RootsError{Error: kind, Message: failure.message}
	if kind == RootsErrorKindOverlaps && root != "" {
		answer.Root = &root
	}
	writeJSONStatus(w, failure.status, answer)
}

// rootListOf turns the stored roots into the generated answer.
func rootListOf(roots []projectfs.RootStatus) RootList {
	out := RootList{Roots: make([]ProjectRoot, 0, len(roots))}
	for _, root := range roots {
		converted := ProjectRoot{Path: root.Path, Available: root.Available}
		if !root.Available {
			reason := RootUnavailableReason(root.Reason)
			converted.Reason = &reason
		}
		out.Roots = append(out.Roots, converted)
	}
	return out
}

// readRootRequest reads a RootRequest body and returns its path, or false for
// anything that is not exactly one: a Content-Type other than
// application/json (with at most a UTF-8 charset), a body over the cap or not
// UTF-8, anything but one JSON object, an unknown field, path missing, given
// twice, empty or not a string. Whether the path is absolute is projectfs's
// to say, and it says invalid too.
//
// The object is read token by token rather than unmarshalled, because
// encoding/json keeps the last of two duplicate keys and matches a key
// case-insensitively, and neither is a request to work out the intent of.
func readRootRequest(r *http.Request) (string, bool) {
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRootRequestBytes+1))
	if err != nil || len(body) > maxRootRequestBytes || !utf8.Valid(body) {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return "", false
	}
	path, seen := "", false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "path" || seen {
			return "", false
		}
		value, err := decoder.Token()
		text, isString := value.(string)
		if err != nil || !isString {
			return "", false
		}
		path, seen = text, true
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return "", false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", false
	}
	if !seen || path == "" {
		return "", false
	}
	return path, true
}

// isJSONContentType accepts exactly one Content-Type, application/json, with
// no parameter but a UTF-8 charset.
func isJSONContentType(values []string) bool {
	if len(values) != 1 {
		return false
	}
	media, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || media != "application/json" {
		return false
	}
	for name, value := range parameters {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}
