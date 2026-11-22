package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/workcopy"
)

// The working-copy endpoints (#43): list the working copies, review what is
// staged for one pipeline, apply it, restore a snapshot, discard a working
// copy and remove a snapshot. They go through the *workcopy.Store in
// Options.Workcopies and are registered by newHandler beside the source
// endpoints, inside the same wrap, for the reason directoriesPattern gives.
// No route stages a change: staging is the in-process workcopy.Store.Stage,
// which #77 and #97 call (internal/workcopy/source_test.go holds that this
// package names no staging method). Every shape is generated from
// schema/workcopy.schema.json.
//
// Apply, restore, discard and snapshot removal change state, so #58's
// relaxation does not reach them.
const (
	workcopiesListPattern   = "GET /api/workcopies"
	workcopyReviewPattern   = "GET /api/workcopies/review"
	workcopyApplyPattern    = "POST /api/workcopies/apply"
	workcopyRestorePattern  = "POST /api/workcopies/restore"
	workcopyDiscardPattern  = "POST /api/workcopies/discard"
	workcopySnapshotPattern = "DELETE /api/workcopies/snapshot"
)

// maxWorkcopyRequestBytes caps an apply, restore, discard or snapshot body:
// a root, a directory, a revision or snapshot id and the keys.
const maxWorkcopyRequestBytes = 8192

var (
	revisionPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	snapshotIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
)

// workcopyFailures is what each kind answers: a status and a fixed sentence,
// nothing from an underlying error and no path.
var workcopyFailures = map[WorkcopyErrorKind]sourceFailure{
	WorkcopyErrorKindInvalid: {http.StatusBadRequest,
		"name a listed root and its pipeline directory, and a revision or snapshot as the review gave it"},
	WorkcopyErrorKindUnavailable: {http.StatusServiceUnavailable,
		"the agent has no config directory, so it keeps no staged changes and applies none"},
	WorkcopyErrorKindNotAuthorized: {http.StatusForbidden,
		"this is not an authorized project directory, so nothing was written"},
	WorkcopyErrorKindRootUnavailable: {http.StatusConflict,
		"this project directory is unavailable, so nothing was written"},
	WorkcopyErrorKindNotFound: {http.StatusNotFound,
		"there is no such pipeline or snapshot"},
	WorkcopyErrorKindLink: {http.StatusConflict,
		"a symbolic link on the way to a staged file, or the file itself, is never followed, so nothing was written"},
	WorkcopyErrorKindChanged: {http.StatusConflict,
		"what is staged changed since the review was drawn, or a directory on the way changed, so nothing was written; look at the review again"},
	WorkcopyErrorKindUnreadable: {http.StatusUnprocessableEntity,
		"the agent may not read or search a staged file or a directory on its path"},
	WorkcopyErrorKindGenerated: {http.StatusForbidden,
		"files under generated/ belong to the tool and are never staged"},
	WorkcopyErrorKindReserved: {http.StatusForbidden,
		"pipeline.yaml is the pipeline itself and is never deleted"},
	WorkcopyErrorKindNotRegularFile: {http.StatusUnprocessableEntity,
		"a staged path names a directory, FIFO, socket or device, not a file"},
	WorkcopyErrorKindTooLarge: {http.StatusRequestEntityTooLarge,
		"a staged file is over 1 MiB"},
	WorkcopyErrorKindNotText: {http.StatusUnprocessableEntity,
		"a staged file is not UTF-8 text without NUL bytes"},
	WorkcopyErrorKindTooMany: {http.StatusUnprocessableEntity,
		"more than 32 files would be staged"},
	WorkcopyErrorKindDrift: {http.StatusConflict,
		"some project files are not what was staged from, so nothing was written"},
	WorkcopyErrorKindSnapshot: {http.StatusInternalServerError,
		"the snapshot could not be written, so nothing in the project was"},
	WorkcopyErrorKindIo: {http.StatusInternalServerError,
		"the staged changes could not be read or written"},
}

// workcopiesListRoute answers GET /api/workcopies.
func (g *guard) workcopiesListRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if g.workcopies == nil {
			reject(w)
			return
		}
		list, err := g.workcopies.List()
		if err != nil {
			writeWorkcopyError(w, err)
			return
		}
		answer := WorkcopyList{Copies: []WorkcopySummary{}, Unreadable: list.Unreadable}
		for _, copy := range list.Copies {
			summary := WorkcopySummary{
				Root: copy.Root, Directory: directoryOf(copy.Directory), Revision: WorkcopyRevision(copy.Revision),
				Files: copy.Files, Available: copy.Available,
			}
			if !copy.Available {
				reason := workcopyKindOf(copy.Reason)
				summary.Reason = &reason
				if reason == WorkcopyErrorKindRootUnavailable {
					root := WorkcopyRootReason(rootReasonOf(string(copy.RootReason)))
					summary.RootReason = &root
				}
			}
			answer.Copies = append(answer.Copies, summary)
		}
		writeJSON(w, answer)
	}
}

// workcopyReviewRoute answers GET /api/workcopies/review?root=...&directory=...
func (g *guard) workcopyReviewRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.workcopies == nil {
			reject(w)
			return
		}
		location, ok := readOpenQuery(r)
		if !ok {
			writeWorkcopyError(w, &workcopy.Error{Kind: workcopy.KindInvalid})
			return
		}
		review, err := g.workcopies.Review(location)
		if err != nil {
			g.logger.Debug("a review was not drawn", "kind", string(workcopy.KindOf(err)))
			writeWorkcopyError(w, err)
			return
		}
		answer := WorkcopyReview{
			Root: review.Root, Directory: directoryOf(review.Directory), Files: []WorkcopyFile{},
			Unreadable: review.Unreadable, Snapshots: []WorkcopySnapshot{},
		}
		if review.Revision != "" {
			revision := WorkcopyRevision(review.Revision)
			answer.Revision = &revision
		}
		for _, file := range review.Files {
			entry := WorkcopyFile{Path: WorkcopyPath(file.Path), Change: WorkcopyChange(file.Change), State: WorkcopyState(file.State)}
			if file.State == workcopy.Ready {
				if file.Change != workcopy.Added {
					text := string(file.Base)
					entry.BaseText = &text
				}
				if file.Change != workcopy.Deleted {
					text := string(file.Staged)
					entry.StagedText = &text
				}
			}
			answer.Files = append(answer.Files, entry)
		}
		for _, snapshot := range review.Snapshots {
			paths := make([]WorkcopyPath, 0, len(snapshot.Paths))
			for _, path := range snapshot.Paths {
				paths = append(paths, WorkcopyPath(path))
			}
			answer.Snapshots = append(answer.Snapshots, WorkcopySnapshot{
				ID: WorkcopySnapshotID(snapshot.ID), Taken: snapshot.Taken.UTC().Truncate(time.Second), Paths: paths,
			})
		}
		writeJSON(w, answer)
	}
}

// workcopyApplyRoute answers POST /api/workcopies/apply.
func (g *guard) workcopyApplyRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.workcopies == nil {
			reject(w)
			return
		}
		location, revision, ok := readWorkcopyRequest(r, "revision", revisionPattern)
		if !ok {
			writeWorkcopyError(w, &workcopy.Error{Kind: workcopy.KindInvalid})
			return
		}
		report, err := g.workcopies.Apply(location, revision)
		if err != nil {
			writeWorkcopyError(w, err)
			return
		}
		writeJSON(w, reportOf(report))
	}
}

// workcopyRestoreRoute answers POST /api/workcopies/restore.
func (g *guard) workcopyRestoreRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.workcopies == nil {
			reject(w)
			return
		}
		location, id, ok := readWorkcopyRequest(r, "snapshot", snapshotIDPattern)
		if !ok {
			writeWorkcopyError(w, &workcopy.Error{Kind: workcopy.KindInvalid})
			return
		}
		report, err := g.workcopies.Restore(location, id)
		if err != nil {
			writeWorkcopyError(w, err)
			return
		}
		writeJSON(w, reportOf(report))
	}
}

// workcopyDiscardRoute answers POST /api/workcopies/discard.
func (g *guard) workcopyDiscardRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.workcopies == nil {
			reject(w)
			return
		}
		location, revision, ok := readWorkcopyRequest(r, "revision", revisionPattern)
		if !ok {
			writeWorkcopyError(w, &workcopy.Error{Kind: workcopy.KindInvalid})
			return
		}
		count, err := g.workcopies.Discard(location, revision)
		if err != nil {
			writeWorkcopyError(w, err)
			return
		}
		writeJSON(w, WorkcopyDiscarded{Root: location.Root, Directory: directoryOf(location.Directory), Discarded: count})
	}
}

// workcopySnapshotRoute answers DELETE /api/workcopies/snapshot.
func (g *guard) workcopySnapshotRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.workcopies == nil {
			reject(w)
			return
		}
		location, id, ok := readWorkcopyRequest(r, "snapshot", snapshotIDPattern)
		if !ok {
			writeWorkcopyError(w, &workcopy.Error{Kind: workcopy.KindInvalid})
			return
		}
		if err := g.workcopies.RemoveSnapshot(location, id); err != nil {
			writeWorkcopyError(w, err)
			return
		}
		writeJSON(w, WorkcopySnapshotRemoved{Root: location.Root, Directory: directoryOf(location.Directory), Snapshot: WorkcopySnapshotID(id)})
	}
}

func reportOf(report *workcopy.Report) WorkcopyReport {
	answer := WorkcopyReport{
		Root: report.Root, Directory: directoryOf(report.Directory), Snapshot: WorkcopySnapshotID(report.Snapshot),
		Complete: report.Complete, Files: []WorkcopyOutcome{},
	}
	for _, file := range report.Files {
		outcome := WorkcopyOutcome{Path: WorkcopyPath(file.Path), Result: WorkcopyResult(file.Result)}
		if file.Result == workcopy.Failed {
			kind := workcopyKindOf(file.Kind)
			outcome.Kind = &kind
		}
		answer.Files = append(answer.Files, outcome)
	}
	return answer
}

// readWorkcopyRequest reads a WorkcopyRevisionRequest or a
// WorkcopySnapshotRequest body, whose third field is named field and must
// match pattern, or false for anything that is not exactly one: a
// Content-Type other than application/json, a body over
// maxWorkcopyRequestBytes or not UTF-8, anything but one JSON object of
// strings, a key that is not root, directory or field exactly, a key given
// twice, root or field missing or empty, or an empty directory. It is read
// token by token, for the reason readRootRequest gives.
func readWorkcopyRequest(r *http.Request, field string, pattern *regexp.Regexp) (catalog.Location, string, bool) {
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return catalog.Location{}, "", false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWorkcopyRequestBytes+1))
	if err != nil || len(body) > maxWorkcopyRequestBytes || !utf8.Valid(body) {
		return catalog.Location{}, "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return catalog.Location{}, "", false
	}
	fields := map[string]string{}
	for decoder.More() {
		key, err := decoder.Token()
		name, isString := key.(string)
		if err != nil || !isString || (name != "root" && name != "directory" && name != field) {
			return catalog.Location{}, "", false
		}
		value, err := decoder.Token()
		text, isText := value.(string)
		if _, seen := fields[name]; err != nil || !isText || seen {
			return catalog.Location{}, "", false
		}
		fields[name] = text
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return catalog.Location{}, "", false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return catalog.Location{}, "", false
	}
	root, directory, value := fields["root"], fields["directory"], fields[field]
	_, hasDirectory := fields["directory"]
	if root == "" || (hasDirectory && directory == "") || !pattern.MatchString(value) {
		return catalog.Location{}, "", false
	}
	return catalog.Location{Root: root, Directory: directory}, value, true
}

// workcopyKindOf is a workcopy kind as the schema's, io for anything unknown.
func workcopyKindOf(kind workcopy.ErrorKind) WorkcopyErrorKind {
	if _, known := workcopyFailures[WorkcopyErrorKind(kind)]; known {
		return WorkcopyErrorKind(kind)
	}
	return WorkcopyErrorKindIo
}

// writeWorkcopyError answers a failed working-copy request from the
// *workcopy.Error err is: its kind's status and sentence, the root's reason
// for rootUnavailable, and every drifted file for drift.
func writeWorkcopyError(w http.ResponseWriter, err error) {
	var fault *workcopy.Error
	if !errors.As(err, &fault) {
		fault = &workcopy.Error{Kind: workcopy.KindIO}
	}
	kind := workcopyKindOf(fault.Kind)
	failure := workcopyFailures[kind]
	answer := WorkcopyError{Error: kind, Message: failure.message}
	switch kind {
	case WorkcopyErrorKindRootUnavailable:
		reason := WorkcopyRootReason(rootReasonOf(string(fault.Reason)))
		answer.Reason = &reason
	case WorkcopyErrorKindDrift:
		for _, drift := range fault.Drift {
			answer.Drift = append(answer.Drift, WorkcopyDrift{Path: WorkcopyPath(drift.Path), Now: WorkcopyState(drift.Now)})
		}
		if len(answer.Drift) == 0 {
			kind = WorkcopyErrorKindIo
			answer = WorkcopyError{Error: kind, Message: workcopyFailures[kind].message}
			failure = workcopyFailures[kind]
		}
	}
	writeJSONStatus(w, failure.status, answer)
}
