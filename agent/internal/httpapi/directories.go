package httpapi

import (
	"net/http"
	"strconv"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// directoriesPattern is the directory browsing endpoint (#7): one page of a
// directory, whether it looks like a project, and whether the page is all of
// it. It is what lets the user pick a project directory from the browser
// without a native file dialog.
//
// It is registered by newHandler beside the channel rather than listed in
// apiRoutes, for the same reason the channel is: the rows of that table are
// stateless handlers that answer 200 with an object of strings, and the
// table's tests hold every row to exactly that. This route needs the Browser
// the guard carries, and answers a structured listing or a structured error.
// It is on the same mux, inside the same wrap, so the token and the origin are
// checked before anything here runs, and directories_test.go holds it to the
// same rejection matrix as everything else.
//
// The query parameters are path, an absolute path, defaulting to the user's
// home directory, and limit, from 1 to projectfs.MaxListing, defaulting to
// the most. The answer is a DirectoryListing, or a DirectoryError with a
// status saying which kind of failure it was; both shapes are generated from
// schema/directory.schema.json.
const directoriesPattern = "GET /api/directories"

// Query parameters of the directory browsing endpoint.
const (
	pathParameter  = "path"
	limitParameter = "limit"
)

// directoryFailures is the whole of what a failed browsing request says: a
// status, a kind and a fixed sentence per kind.
//
// Nothing from the underlying error is passed on, and that is the point. A
// projectfs refusal can carry the path that resolution arrived at outside the
// browsable directories, and its Error text prints it, which would tell a
// caller what exists out there. So the kind is the only thing taken from it,
// and two refusals of the same kind are byte-identical answers whatever the
// paths behind them were.
var directoryFailures = map[DirectoryErrorKind]struct {
	status  int
	message string
}{
	DirectoryErrorKindInvalid: {http.StatusBadRequest,
		"give an absolute path to a directory once, and a limit from 1 to 1000 if any"},
	DirectoryErrorKindForbidden: {http.StatusForbidden,
		"only the authorized project directories and the home directory can be browsed"},
	DirectoryErrorKindNotFound: {http.StatusNotFound,
		"this directory does not exist"},
	DirectoryErrorKindUnreadable: {http.StatusForbidden,
		"the agent is not permitted to read this directory"},
	DirectoryErrorKindNotDirectory: {http.StatusConflict,
		"this path is not a directory"},
	DirectoryErrorKindIo: {http.StatusInternalServerError,
		"this directory could not be read"},
}

// directoryErrorKindOf maps a projectfs failure onto the kind the API reports.
// Anything that is not one of projectfs's own errors is io, never forbidden or
// notFound: those two are claims, and only the guard makes them.
func directoryErrorKindOf(err error) DirectoryErrorKind {
	switch projectfs.KindOf(err) {
	case projectfs.KindForbidden:
		return DirectoryErrorKindForbidden
	case projectfs.KindNotFound:
		return DirectoryErrorKindNotFound
	case projectfs.KindUnreadable:
		return DirectoryErrorKindUnreadable
	case projectfs.KindNotDirectory:
		return DirectoryErrorKindNotDirectory
	case projectfs.KindInvalid:
		return DirectoryErrorKindInvalid
	default:
		return DirectoryErrorKindIo
	}
}

// directoriesRoute is the handler for the directory browsing endpoint.
func (g *guard) directoriesRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.browser == nil {
			// A server built with no browser browses nothing, and says so the
			// way it says everything else.
			reject(w)
			return
		}
		query := r.URL.Query()
		paths, limits := query[pathParameter], query[limitParameter]
		if len(paths) > 1 || len(limits) > 1 {
			// A parameter given twice is not a request to work out the intent
			// of, the same rule the Authorization header follows.
			writeDirectoryError(w, DirectoryErrorKindInvalid)
			return
		}
		path := g.browser.Home()
		if len(paths) == 1 {
			path = paths[0]
		}
		if path == "" {
			writeDirectoryError(w, DirectoryErrorKindInvalid)
			return
		}
		limit := projectfs.MaxListing
		if len(limits) == 1 {
			parsed, err := strconv.Atoi(limits[0])
			if err != nil || parsed < 1 || parsed > projectfs.MaxListing {
				writeDirectoryError(w, DirectoryErrorKindInvalid)
				return
			}
			limit = parsed
		}

		listing, err := g.browser.List(path, limit)
		if err != nil {
			// The kind is logged and nothing else, for the reason
			// directoryFailures gives: the error's own text can describe what
			// is outside the browsable directories.
			kind := directoryErrorKindOf(err)
			g.logger.Debug("a directory listing was refused", "kind", string(kind))
			writeDirectoryError(w, kind)
			return
		}
		writeJSONStatus(w, http.StatusOK, directoryListingOf(listing))
	}
}

// writeDirectoryError answers a failed browsing request.
func writeDirectoryError(w http.ResponseWriter, kind DirectoryErrorKind) {
	failure, known := directoryFailures[kind]
	if !known {
		kind = DirectoryErrorKindIo
		failure = directoryFailures[kind]
	}
	writeJSONStatus(w, failure.status, DirectoryError{Error: kind, Message: failure.message})
}

// directoryListingOf turns a projectfs listing into the generated response
// type, so the shape on the wire is the one the schema says.
func directoryListingOf(listing projectfs.Listing) DirectoryListing {
	out := DirectoryListing{
		Path:             listing.Path,
		Entries:          make([]DirectoryEntry, 0, len(listing.Entries)),
		Truncated:        listing.Truncated,
		Limit:            listing.Limit,
		LooksLikeProject: listing.LooksLikeProject,
		Evidence:         make([]ProjectEvidence, 0, len(listing.Evidence)),
	}
	if listing.Parent != "" {
		parent := listing.Parent
		out.Parent = &parent
	}
	for _, entry := range listing.Entries {
		converted := DirectoryEntry{
			Name:       entry.Name,
			Path:       entry.Path,
			Kind:       DirectoryEntryKind(entry.Kind),
			Unreadable: entry.Unreadable,
		}
		if entry.LinkTarget != "" {
			target := LinkTarget(entry.LinkTarget)
			converted.LinkTarget = &target
		}
		out.Entries = append(out.Entries, converted)
	}
	for _, evidence := range listing.Evidence {
		out.Evidence = append(out.Evidence, ProjectEvidence{Marker: evidence.Marker, Describes: evidence.Describes})
	}
	return out
}
