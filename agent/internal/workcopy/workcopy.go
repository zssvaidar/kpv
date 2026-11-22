// Package workcopy is the managed working copy of a pipeline directory and
// its apply flow (#43): the edits the tool makes on the user's behalf (#77's
// rewrites of other pipelines' pipeline.yaml, #97's write-back into imported
// source) are staged here and reach the project only when the user reviews
// them and applies them. A user's own explicit save is never staged: it is
// catalog.WriteSource's direct, hash-checked write (#87, #14, #15, #78), as
// are pipeline create (#48), internal/codegen and internal/secrets.
//
// A working copy is an overlay, not a copy of the tree. It belongs to one
// pipeline directory, addressed by catalog.Location, and holds only the files
// staged: for each, its path relative to the pipeline directory, the SHA-256
// of the project file it was based on (or that there was none), and the new
// bytes or a deletion. Apply checks every staged file's project copy against
// that hash before anything is written, takes a snapshot of what it will
// change, and only then writes; restore puts a snapshot back with the same
// checks.
//
// The working copies and snapshots are files of a projectfs.PrivateTree in
// the agent's store directory (os.UserConfigDir()/kpv/workcopies/), which
// #63 keeps outside every authorized root. The project is read and written
// only through catalog.Files, which holds the same serialization as
// catalog.WriteSource.
package workcopy

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// TreeName is the directory of the store the working copies and snapshots
// are kept in.
const TreeName = "workcopies"

// MaxFiles is the most files one working copy stages, which keeps a working
// copy's file, and a snapshot's, well inside projectfs.MaxPrivateTreeFile.
const MaxFiles = 32

const (
	storeVersion   = 1
	copyPrefix     = "copy-"
	snapshotPrefix = "snap-"
	fileSuffix     = ".json"
)

var (
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	snapshotPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ChangeKind is what a staged change does to its file.
type ChangeKind string

// The changes: a file that was not there, one that was, and a deletion of
// the whole file.
const (
	Added    ChangeKind = "added"
	Modified ChangeKind = "modified"
	Deleted  ChangeKind = "deleted"
)

// State is what a project file is now against what a change was based on.
type State string

const (
	// Ready is a project file that is still what the change was based on.
	Ready State = "ready"
	// Changed is a file changed in the project since; it and every state
	// after it is drift.
	Changed State = "changed"
	// Created is a file created in the project since.
	Created State = "created"
	// DeletedNow is a file deleted in the project since.
	DeletedNow State = "deleted"
	// Link is a file that is now a symbolic link.
	Link State = "link"
	// NotRegularFile is a path that now names a directory or special file.
	NotRegularFile State = "notRegularFile"
	// TooLarge is a file now over 1 MiB.
	TooLarge State = "tooLarge"
	// NotText is a file that is now not UTF-8 text without NUL.
	NotText State = "notText"
	// Unreadable is a file the agent may not read.
	Unreadable State = "unreadable"
)

// Result is what an apply or restore did with one path.
type Result string

// The results: written; not written, because an earlier write failed; and
// failed, with a kind.
const (
	Written    Result = "written"
	NotWritten Result = "notWritten"
	Failed     Result = "failed"
	// Unchanged is a restore's file that was already as the snapshot holds
	// it, because the apply did not write it.
	Unchanged Result = "unchanged"
)

// Change is one change to stage: Text for the file's new bytes, or Delete.
type Change struct {
	Path   string
	Text   []byte
	Delete bool
}

// Summary is one working copy as List answers it.
type Summary struct {
	catalog.Location
	Revision string
	Files    int
	// Available is false when the pipeline is no longer listed (its
	// directory renamed or removed, its root revoked or unavailable); Reason
	// says why, and RootReason why the root is unavailable. Such a working
	// copy can be discarded but not applied.
	Available  bool
	Reason     ErrorKind
	RootReason projectfs.ErrorKind
}

// List is every working copy, sorted by root then directory, and how many
// stored working copies could not be read and were moved aside.
type List struct {
	Copies     []Summary
	Unreadable int
}

// FileReview is one staged file in the review.
type FileReview struct {
	Path   string
	Change ChangeKind
	State  State
	// Base is the project file's bytes, and Staged the staged ones, set only
	// when State is Ready: Base when the change was based on a file, Staged
	// unless the change is a deletion.
	Base, Staged []byte
}

// SnapshotSummary is one snapshot: when it was taken, in UTC, and its paths.
type SnapshotSummary struct {
	ID    string
	Taken time.Time
	Paths []string
}

// Review is what the user is shown before applying.
type Review struct {
	catalog.Location
	// Revision identifies what is staged; "" when nothing is.
	Revision string
	// Files are sorted by path, byte order.
	Files []FileReview
	// Unreadable is true when the stored working copy of this pipeline could
	// not be read, was moved aside, and so nothing is staged.
	Unreadable bool
	// Snapshots are newest first.
	Snapshots []SnapshotSummary
}

// Outcome is one path in an apply or restore report.
type Outcome struct {
	Path   string
	Result Result
	// Kind is why a Failed path was not written.
	Kind ErrorKind
}

// Report is what an apply or restore did, path by path in byte order.
type Report struct {
	catalog.Location
	// Snapshot is the snapshot the apply took, or the one restored.
	Snapshot string
	// Complete is true when every path was written.
	Complete bool
	Files    []Outcome
}

// hooks lets this package's tests act at a point inside an apply or
// restore. The agent sets none.
type hooks struct {
	// afterCheck is called once every file has been checked and before the
	// directories are verified and anything is written.
	afterCheck func()
	// beforeFile is called before each file is written, in order; an error
	// fails that file as a full disk would.
	beforeFile func(index int, path string) error
}

// Store holds every working copy and snapshot. It is safe for concurrent
// use: one operation at a time, and while an apply or restore runs it holds
// catalog.Files, so no catalog.WriteSource interleaves with it.
type Store struct {
	catalog *catalog.Catalog
	tree    *projectfs.PrivateTree
	logger  *slog.Logger
	mutex   sync.Mutex
	// lost holds the working copy files that could not be read and were
	// moved aside, by name, until something is staged or discarded there.
	lost map[string]bool
	now  func() time.Time
	hook hooks
}

// New returns the store over the catalog the pipelines are found through
// and the private tree its files are kept in. A nil tree is no config
// directory: every operation then answers KindUnavailable and nothing is
// written. A nil logger discards everything.
func New(pipelines *catalog.Catalog, tree *projectfs.PrivateTree, logger *slog.Logger) (*Store, error) {
	if pipelines == nil {
		return nil, &Error{Kind: KindInvalid}
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Store{catalog: pipelines, tree: tree, logger: logger, lost: map[string]bool{}, now: time.Now}, nil
}

// The stored documents. Bytes are base64 ([]byte), so a 1 MiB text is at
// most a third larger whatever it holds.

type storedCopy struct {
	Version   int          `json:"version"`
	Root      string       `json:"root"`
	Directory string       `json:"directory"`
	Revision  string       `json:"revision"`
	Files     []storedFile `json:"files"`
}

type storedFile struct {
	Path string `json:"path"`
	// Base is the SHA-256 the change was based on, "" for no file.
	Base   string `json:"base"`
	Delete bool   `json:"delete"`
	Data   []byte `json:"data"`
}

type storedSnapshot struct {
	Version   int            `json:"version"`
	Root      string         `json:"root"`
	Directory string         `json:"directory"`
	ID        string         `json:"id"`
	Taken     time.Time      `json:"taken"`
	Files     []snapshotFile `json:"files"`
}

type snapshotFile struct {
	Path string `json:"path"`
	// BeforeAbsent is a file that was not there; Before its bytes otherwise.
	BeforeAbsent bool   `json:"beforeAbsent"`
	Before       []byte `json:"before"`
	// After is the SHA-256 the apply writes, "" for a deletion.
	After string `json:"after"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// key names a location's files in the tree.
func key(location catalog.Location) string {
	sum := sha256.Sum256([]byte(location.Root + "\x00" + location.Directory))
	return hex.EncodeToString(sum[:16])
}

func copyName(location catalog.Location) string {
	return copyPrefix + key(location) + fileSuffix
}

func snapshotName(location catalog.Location, id string) string {
	return snapshotPrefix + key(location) + "-" + id + fileSuffix
}

func randomHex(n int) (string, error) {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// decodeStrict decodes exactly one JSON document of v's shape.
func decodeStrict(raw []byte, v any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

// validPath is a path a working copy may hold: catalog.CheckStagedPath's
// rules.
func validPath(path string) bool {
	return catalog.CheckStagedPath(path) == ""
}

// parseCopy accepts a stored working copy of location whose every entry
// keeps the rules staging keeps, or answers the problem kind it is refused
// with. An entry whose path breaks them is never followed.
func parseCopy(raw []byte, location catalog.Location) (*storedCopy, string) {
	var stored storedCopy
	if !decodeStrict(raw, &stored) || stored.Version != storeVersion {
		return nil, "corrupt"
	}
	if stored.Root != location.Root || stored.Directory != location.Directory || !revisionPattern.MatchString(stored.Revision) || len(stored.Files) == 0 || len(stored.Files) > MaxFiles {
		return nil, "corrupt"
	}
	seen := map[string]bool{}
	for _, file := range stored.Files {
		if !validPath(file.Path) || seen[file.Path] {
			return nil, "invalidPath"
		}
		seen[file.Path] = true
		switch {
		case file.Base != "" && !sha256Pattern.MatchString(file.Base),
			file.Delete && (file.Base == "" || len(file.Data) != 0 || catalog.IsPipelineFile(file.Path)),
			!file.Delete && catalog.CheckStagedText(file.Data) != "":
			return nil, "corrupt"
		}
	}
	sort.Slice(stored.Files, func(i, j int) bool { return stored.Files[i].Path < stored.Files[j].Path })
	return &stored, ""
}

// parseSnapshot accepts a stored snapshot of location with that id, by the
// same rules.
func parseSnapshot(raw []byte, location catalog.Location, id string) (*storedSnapshot, string) {
	var stored storedSnapshot
	if !decodeStrict(raw, &stored) || stored.Version != storeVersion {
		return nil, "corrupt"
	}
	if stored.Root != location.Root || stored.Directory != location.Directory || stored.ID != id || len(stored.Files) == 0 || len(stored.Files) > MaxFiles {
		return nil, "corrupt"
	}
	seen := map[string]bool{}
	for _, file := range stored.Files {
		if !validPath(file.Path) || seen[file.Path] {
			return nil, "invalidPath"
		}
		seen[file.Path] = true
		switch {
		case file.After != "" && !sha256Pattern.MatchString(file.After),
			file.BeforeAbsent && len(file.Before) != 0,
			!file.BeforeAbsent && catalog.CheckStagedText(file.Before) != "":
			return nil, "corrupt"
		}
	}
	sort.Slice(stored.Files, func(i, j int) bool { return stored.Files[i].Path < stored.Files[j].Path })
	return &stored, ""
}

// readFile reads a file of the tree: its bytes, whether it is there, and a
// problem kind when it could not be read, in which case it has been moved
// aside.
func (s *Store) readFile(name string) ([]byte, bool, string) {
	file, err := s.tree.File(name)
	if err != nil {
		return nil, false, "invalid"
	}
	raw, found, problem := file.Read()
	if problem != "" {
		s.setAside(file, string(problem))
		return nil, false, string(problem)
	}
	return raw, found, ""
}

func (s *Store) setAside(file *projectfs.PrivateFile, problem string) {
	s.logger.Warn("a stored working copy or snapshot could not be read, so it is moved aside", "kind", problem)
	if err := file.SetAside(); err != nil {
		s.logger.Warn("an unreadable working copy or snapshot could not be moved aside", "kind", string(projectfs.KindOf(err)))
	}
}

// loadCopy reads location's working copy: nil when there is none, or when
// it could not be read, which is then moved aside and remembered as lost.
func (s *Store) loadCopy(location catalog.Location) *storedCopy {
	name := copyName(location)
	raw, found, problem := s.readFile(name)
	if problem != "" {
		s.lost[name] = true
		return nil
	}
	if !found {
		return nil
	}
	stored, problem := parseCopy(raw, location)
	if problem != "" {
		if file, err := s.tree.File(name); err == nil {
			s.setAside(file, problem)
		}
		s.lost[name] = true
		return nil
	}
	return stored
}

// saveCopy writes a working copy, or removes it when it holds nothing.
func (s *Store) saveCopy(location catalog.Location, stored *storedCopy) error {
	name := copyName(location)
	if stored == nil || len(stored.Files) == 0 {
		if err := s.tree.Remove(name); err != nil {
			return &Error{Kind: KindIO}
		}
		return nil
	}
	revision, err := randomHex(16)
	if err != nil {
		return &Error{Kind: KindIO}
	}
	stored.Revision = revision
	body, err := json.Marshal(stored)
	if err != nil {
		return &Error{Kind: KindIO}
	}
	file, err := s.tree.File(name)
	if err != nil {
		return &Error{Kind: KindIO}
	}
	if err := file.Write(body); err != nil {
		return &Error{Kind: KindIO}
	}
	return nil
}

// Stage stages changes to the pipeline at location and answers the new
// revision. It is an in-process method, never an HTTP route: #77 and #97
// call it. Nothing in the project is written.
//
// Every path keeps catalog.CheckStagedPath's rules (KindInvalid,
// KindGenerated), a path given twice is KindInvalid, a deletion of
// pipeline.yaml KindReserved, and bytes over 1 MiB or that are not UTF-8
// text without NUL KindTooLarge or KindNotText, before anything is read. A
// path staged for the first time is based on the project file as it is now,
// which must be a readable text file or absent (else its kind); one already
// staged keeps the base it was first staged from. A change back to its base,
// or a deletion of a file that was never there, unstages the path; a
// deletion of a file that is not there and not staged is KindNotFound. More
// than MaxFiles staged is KindTooMany. With no store, KindUnavailable.
func (s *Store) Stage(location catalog.Location, changes ...Change) (string, error) {
	if s.tree == nil {
		return "", &Error{Kind: KindUnavailable}
	}
	if len(changes) == 0 {
		return "", &Error{Kind: KindInvalid}
	}
	seen := map[string]bool{}
	for _, change := range changes {
		if kind := catalog.CheckStagedPath(change.Path); kind != "" {
			return "", &Error{Kind: ErrorKind(kind)}
		}
		if seen[change.Path] {
			return "", &Error{Kind: KindInvalid}
		}
		seen[change.Path] = true
		if change.Delete {
			if len(change.Text) != 0 {
				return "", &Error{Kind: KindInvalid}
			}
			if catalog.IsPipelineFile(change.Path) {
				return "", &Error{Kind: KindReserved}
			}
			continue
		}
		if kind := catalog.CheckStagedText(change.Text); kind != "" {
			return "", &Error{Kind: ErrorKind(kind)}
		}
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	stored := s.loadCopy(location)
	if stored == nil {
		stored = &storedCopy{Version: storeVersion, Root: location.Root, Directory: location.Directory}
	}
	err := s.catalog.WithFiles(location, func(files *catalog.Files) error {
		for _, change := range changes {
			index := -1
			for i, file := range stored.Files {
				if file.Path == change.Path {
					index = i
				}
			}
			base := ""
			if index >= 0 {
				base = stored.Files[index].Base
			} else {
				current, err := files.Read(change.Path)
				if err != nil {
					return err
				}
				if current.Kind != "" {
					return &catalog.Error{Kind: current.Kind}
				}
				if current.Present {
					base = digest(current.Data)
				}
			}
			entry := storedFile{Path: change.Path, Base: base, Delete: change.Delete, Data: append([]byte{}, change.Text...)}
			if change.Delete {
				entry.Data = nil
			}
			unstage := (change.Delete && base == "") || (!change.Delete && base != "" && digest(change.Text) == base)
			switch {
			case unstage && index < 0 && change.Delete:
				return &Error{Kind: KindNotFound}
			case unstage && index >= 0:
				stored.Files = append(stored.Files[:index], stored.Files[index+1:]...)
			case unstage:
			case index >= 0:
				stored.Files[index] = entry
			default:
				stored.Files = append(stored.Files, entry)
			}
		}
		return nil
	})
	if err != nil {
		return "", fromCatalog(err)
	}
	if len(stored.Files) > MaxFiles {
		return "", &Error{Kind: KindTooMany}
	}
	sort.Slice(stored.Files, func(i, j int) bool { return stored.Files[i].Path < stored.Files[j].Path })
	if err := s.saveCopy(location, stored); err != nil {
		return "", err
	}
	delete(s.lost, copyName(location))
	s.logger.Debug("changes were staged", "files", len(stored.Files))
	return stored.Revision, nil
}

// stateOf is what current is against a base hash ("" for no file).
func stateOf(base string, current catalog.Current) State {
	switch {
	case current.Kind != "":
		return State(current.Kind)
	case base == "" && current.Present:
		return Created
	case base == "":
		return Ready
	case !current.Present:
		return DeletedNow
	case digest(current.Data) != base:
		return Changed
	}
	return Ready
}

func changeOf(file storedFile) ChangeKind {
	switch {
	case file.Delete:
		return Deleted
	case file.Base == "":
		return Added
	}
	return Modified
}

// Review answers what is staged for the pipeline at location, each file
// with the project file as it is now, and its snapshots. A pipeline that is
// not listed answers as catalog.WithFiles does. Nothing is written.
func (s *Store) Review(location catalog.Location) (*Review, error) {
	if s.tree == nil {
		return nil, &Error{Kind: KindUnavailable}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	review := &Review{Location: location, Files: []FileReview{}}
	stored := s.loadCopy(location)
	review.Unreadable = s.lost[copyName(location)]
	err := s.catalog.WithFiles(location, func(files *catalog.Files) error {
		if stored == nil {
			return nil
		}
		review.Revision = stored.Revision
		for _, file := range stored.Files {
			current, err := files.Read(file.Path)
			if err != nil {
				return err
			}
			entry := FileReview{Path: file.Path, Change: changeOf(file), State: stateOf(file.Base, current)}
			if entry.State == Ready {
				if file.Base != "" {
					entry.Base = current.Data
				}
				if !file.Delete {
					entry.Staged = file.Data
				}
			}
			review.Files = append(review.Files, entry)
		}
		return nil
	})
	if err != nil {
		return nil, fromCatalog(err)
	}
	review.Snapshots = s.snapshots(location)
	return review, nil
}

// snapshots lists location's snapshots newest first, moving aside any that
// cannot be read.
func (s *Store) snapshots(location catalog.Location) []SnapshotSummary {
	out := []SnapshotSummary{}
	names, err := s.tree.Names()
	if err != nil {
		s.logger.Warn("the snapshots could not be listed", "kind", string(projectfs.KindOf(err)))
		return out
	}
	prefix := snapshotPrefix + key(location) + "-"
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), fileSuffix)
		if stored := s.loadSnapshot(location, id); stored != nil {
			paths := make([]string, 0, len(stored.Files))
			for _, file := range stored.Files {
				paths = append(paths, file.Path)
			}
			out = append(out, SnapshotSummary{ID: id, Taken: stored.Taken.UTC(), Paths: paths})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Taken.Equal(out[j].Taken) {
			return out[i].Taken.After(out[j].Taken)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// loadSnapshot reads one snapshot, nil when it is not there or could not be
// read (it is then moved aside).
func (s *Store) loadSnapshot(location catalog.Location, id string) *storedSnapshot {
	if !snapshotPattern.MatchString(id) {
		return nil
	}
	name := snapshotName(location, id)
	raw, found, problem := s.readFile(name)
	if problem != "" || !found {
		return nil
	}
	stored, problem := parseSnapshot(raw, location, id)
	if problem != "" {
		if file, err := s.tree.File(name); err == nil {
			s.setAside(file, problem)
		}
		return nil
	}
	return stored
}

// List answers every working copy, each with whether its pipeline is still
// listed and why not. Nothing is written but a working copy that cannot be
// read, which is moved aside.
func (s *Store) List() (*List, error) {
	if s.tree == nil {
		return nil, &Error{Kind: KindUnavailable}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	names, err := s.tree.Names()
	if err != nil {
		return nil, &Error{Kind: KindIO}
	}
	list := &List{Copies: []Summary{}}
	for _, name := range names {
		if !strings.HasPrefix(name, copyPrefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		raw, found, problem := s.readFile(name)
		if problem != "" {
			s.lost[name] = true
			continue
		}
		if !found {
			continue
		}
		var probe struct {
			Root      string `json:"root"`
			Directory string `json:"directory"`
		}
		_ = json.Unmarshal(raw, &probe)
		location := catalog.Location{Root: probe.Root, Directory: probe.Directory}
		stored, problem := parseCopy(raw, location)
		if problem != "" || copyName(location) != name {
			if file, err := s.tree.File(name); err == nil {
				s.setAside(file, "corrupt")
			}
			s.lost[name] = true
			continue
		}
		summary := Summary{Location: location, Revision: stored.Revision, Files: len(stored.Files), Available: true}
		if err := s.catalog.WithFiles(location, func(*catalog.Files) error { return nil }); err != nil {
			fault := fromCatalog(err)
			summary.Available, summary.Reason, summary.RootReason = false, fault.Kind, fault.Reason
		}
		list.Copies = append(list.Copies, summary)
	}
	list.Unreadable = len(s.lost)
	sort.Slice(list.Copies, func(i, j int) bool {
		a, b := list.Copies[i].Location, list.Copies[j].Location
		if a.Root != b.Root {
			return a.Root < b.Root
		}
		return a.Directory < b.Directory
	})
	return list, nil
}

// Discard removes the working copy of location, whose revision must be the
// one given (else KindChanged and nothing is removed). The project is not
// touched, so it can be discarded when the pipeline is no longer listed.
// It answers how many files were staged.
func (s *Store) Discard(location catalog.Location, revision string) (int, error) {
	if s.tree == nil {
		return 0, &Error{Kind: KindUnavailable}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	stored := s.loadCopy(location)
	if stored == nil || stored.Revision != revision {
		return 0, &Error{Kind: KindChanged}
	}
	if err := s.saveCopy(location, nil); err != nil {
		return 0, err
	}
	delete(s.lost, copyName(location))
	s.logger.Debug("a working copy was discarded", "files", len(stored.Files))
	return len(stored.Files), nil
}

// RemoveSnapshot removes one snapshot of location; one that is not there is
// KindNotFound.
func (s *Store) RemoveSnapshot(location catalog.Location, id string) error {
	if s.tree == nil {
		return &Error{Kind: KindUnavailable}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.loadSnapshot(location, id) == nil {
		return &Error{Kind: KindNotFound}
	}
	if err := s.tree.Remove(snapshotName(location, id)); err != nil {
		return &Error{Kind: KindIO}
	}
	return nil
}
