package codegen

import (
	"errors"
	"io/fs"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// tree is generated/ inside one pipeline directory, looked at through the
// guard. Every path in it is relative to generated/.
//
// Every directory a run looks into or writes into is pinned (projectfs
// Path.Pin) the moment it is found to be a real directory, and every file is
// reached by its single name relative to the pinned directory that holds it.
// So once a directory has been checked, nothing that happens to its name --
// renamed away, replaced by a link -- redirects what the run does in it: the
// run keeps working in the directory it checked, wherever that now is.
type tree struct {
	// pipeline is the pipeline directory, pinned.
	pipeline *projectfs.PinnedDir
	// exists says generated/ is there, as a directory, pinned at
	// directories[""].
	exists bool
	// directories remembers what each directory under generated/ was found
	// to be, "" for generated/ itself, so a path deep in an absent directory
	// is not looked up, and so a directory found once is the one used after.
	directories map[string]directoryState
	// manifestBytes is the manifest as it was read, nil when there is none.
	manifestBytes []byte
}

type directoryCondition int

const (
	directoryPresent directoryCondition = iota
	directoryAbsent
	directoryLink
	directoryNotDirectory
	directoryFailed
)

// directoryState is what a directory under generated/ is; for anything but
// present and absent, at is the directory that is the trouble, which may be
// one above the directory asked about. A present directory is held in pin.
type directoryState struct {
	condition directoryCondition
	at        string
	kind      projectfs.ErrorKind
	pin       *projectfs.PinnedDir
}

// errNotPinned is a path whose directory was not found to be a real
// directory. Every caller checks the directory first, so it is a guard
// against a mistake here, not something a run reports in practice.
var errNotPinned = errors.New("codegen: the directory holding this path was not checked")

// openTree pins dir and looks at generated/ inside it. A pipeline directory
// the guard will not pin (a component of its path is now a link: changed;
// the root revoked: forbidden), or a generated/ that is a link, not a
// directory, or that the guard refuses, is a problem for the whole run. The
// tree it returns holds descriptors until close.
func openTree(dir projectfs.Path) (*tree, *Error) {
	pinned, err := dir.Pin()
	if err != nil {
		return nil, guardProblem(ProblemGuard, err)
	}
	t := &tree{pipeline: pinned, directories: map[string]directoryState{}}
	fail := func(problem *Error) (*tree, *Error) {
		t.close()
		return nil, problem
	}
	generated, err := pinned.Path().Join(GeneratedDir)
	if err != nil {
		return fail(guardProblem(ProblemGuard, err))
	}
	info, err := generated.Lstat()
	switch {
	case projectfs.IsNotFound(err):
		t.directories[""] = directoryState{condition: directoryAbsent}
		return t, nil
	case err != nil:
		return fail(guardProblem(ProblemGuard, err))
	case info.Mode()&fs.ModeSymlink != 0:
		return fail(&Error{Problem: Problem{Kind: ProblemGeneratedIsLink}})
	case !info.IsDir():
		return fail(&Error{Problem: Problem{Kind: ProblemGeneratedNotDirectory}})
	}
	pin, err := generated.Pin()
	if err != nil {
		return fail(guardProblem(ProblemGuard, err))
	}
	t.directories[""] = directoryState{condition: directoryPresent, pin: pin}
	t.exists = true
	return t, nil
}

// createGenerated makes generated/ in the pinned pipeline directory and pins
// it. One that appears in the meantime is not adopted: Mkdir fails, and so
// does the run.
func (t *tree) createGenerated() error {
	generated, err := t.pipeline.Path().Join(GeneratedDir)
	if err != nil {
		return err
	}
	if err := generated.Mkdir(0o755); err != nil {
		return err
	}
	pin, err := generated.Pin()
	if err != nil {
		return err
	}
	t.directories[""] = directoryState{condition: directoryPresent, pin: pin}
	t.exists = true
	return nil
}

// close releases every descriptor the tree holds.
func (t *tree) close() {
	for _, state := range t.directories {
		if state.pin != nil {
			_ = state.pin.Close()
		}
	}
	_ = t.pipeline.Close()
}

// pinned is the guard's handle to the directory at path under generated/
// ("" is generated/ itself), which must have been found present.
func (t *tree) pinned(directory string) (projectfs.Path, error) {
	state, known := t.directories[directory]
	if !known || state.pin == nil {
		return projectfs.Path{}, errNotPinned
	}
	return state.pin.Path(), nil
}

// at is the guard's handle to a file under generated/: its single name,
// joined to the pinned directory that holds it.
func (t *tree) at(path string) (projectfs.Path, error) {
	directory, name := splitPath(path)
	parent, err := t.pinned(directory)
	if err != nil {
		return projectfs.Path{}, err
	}
	return parent.Join(name)
}

// directory is what the directory at path is, checking every directory on
// the way to it first. "" is generated/ itself. A directory found present is
// not looked at again by name: its pin is what is used from then on.
func (t *tree) directory(path string) directoryState {
	if state, known := t.directories[path]; known {
		return state
	}
	if path == "" {
		return directoryState{condition: directoryAbsent}
	}
	parent, _ := splitPath(path)
	state := t.directory(parent)
	if state.condition == directoryPresent {
		state = t.lookAtDirectory(path)
	} else {
		state.pin = nil
	}
	t.directories[path] = state
	return state
}

// lookAtDirectory describes one directory whose parent is a real, pinned
// directory, and pins it if it is a real directory too.
func (t *tree) lookAtDirectory(path string) directoryState {
	handle, err := t.at(path)
	if err != nil {
		return directoryState{condition: directoryFailed, at: path, kind: kindOf(err)}
	}
	info, err := handle.Lstat()
	switch {
	case projectfs.IsNotFound(err):
		return directoryState{condition: directoryAbsent}
	case err != nil:
		return directoryState{condition: directoryFailed, at: path, kind: kindOf(err)}
	case info.Mode()&fs.ModeSymlink != 0:
		return directoryState{condition: directoryLink, at: path}
	case !info.IsDir():
		return directoryState{condition: directoryNotDirectory, at: path}
	}
	pin, err := handle.Pin()
	if err != nil {
		return directoryState{condition: directoryFailed, at: path, kind: kindOf(err)}
	}
	return directoryState{condition: directoryPresent, pin: pin}
}

// readManifest reads the manifest. A missing one is (nil, nil); one that is
// there and cannot be read, parsed or understood stops the run.
func (t *tree) readManifest() (manifest, *Error) {
	if !t.exists {
		return nil, nil
	}
	handle, err := t.at(ManifestName)
	if err != nil {
		return nil, guardProblem(ProblemManifestUnreadable, err)
	}
	info, err := handle.Lstat()
	switch {
	case projectfs.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, guardProblem(ProblemManifestUnreadable, err)
	case !info.Mode().IsRegular():
		return nil, &Error{Problem: Problem{Kind: ProblemManifestUnreadable}}
	}
	data, err := handle.ReadFile()
	if err != nil {
		return nil, guardProblem(ProblemManifestUnreadable, err)
	}
	parsed, err := decodeManifest(data)
	if errors.Is(err, errManifestVersion) {
		return nil, &Error{Problem: Problem{Kind: ProblemManifestUnknownVersion}, Err: err}
	}
	if err != nil {
		return nil, &Error{Problem: Problem{Kind: ProblemManifestInvalid}, Err: err}
	}
	t.manifestBytes = data
	return parsed, nil
}

// fileState is what is at a path under generated/, as far as a run cares.
type fileState struct {
	absent  bool
	regular bool
	report  FileReport // for anything that is not a regular file or absent
	content []byte     // for a regular file
}

// lookAtFile describes the file at path, its directories already checked.
func (t *tree) lookAtFile(path string) fileState {
	directory, _ := splitPath(path)
	switch state := t.directory(directory); state.condition {
	case directoryAbsent:
		return fileState{absent: true}
	case directoryLink:
		return fileState{report: FileReport{Path: path, Status: StatusBlocked, Reason: ReasonDirectoryIsLink, At: state.at}}
	case directoryNotDirectory:
		return fileState{report: FileReport{Path: path, Status: StatusBlocked, Reason: ReasonDirectoryNotDirectory, At: state.at}}
	case directoryFailed:
		return fileState{report: FileReport{Path: path, Status: StatusFailed, At: state.at, Kind: state.kind}}
	}
	handle, err := t.at(path)
	if err != nil {
		return fileState{report: FileReport{Path: path, Status: StatusFailed, Kind: kindOf(err)}}
	}
	info, err := handle.Lstat()
	switch {
	case projectfs.IsNotFound(err):
		return fileState{absent: true}
	case err != nil:
		return fileState{report: FileReport{Path: path, Status: StatusFailed, Kind: kindOf(err)}}
	case !info.Mode().IsRegular():
		return fileState{report: FileReport{Path: path, Status: StatusNotRegularFile}}
	}
	data, err := handle.ReadFile()
	if err != nil {
		return fileState{report: FileReport{Path: path, Status: StatusFailed, Kind: kindOf(err)}}
	}
	return fileState{regular: true, content: data}
}

// examine decides what a run does with one output: the status it would end
// with if nothing stops the run.
func (t *tree) examine(file File, entry *manifestEntry) FileReport {
	state := t.lookAtFile(file.Path)
	switch {
	case state.absent && entry != nil:
		return FileReport{Path: file.Path, Status: StatusRegenerated}
	case state.absent:
		return FileReport{Path: file.Path, Status: StatusWritten}
	case !state.regular:
		return state.report
	case string(state.content) == string(file.Content):
		return FileReport{Path: file.Path, Status: StatusUnchanged}
	case entry != nil && hashOf(state.content) == entry.SHA256:
		return FileReport{Path: file.Path, Status: StatusWritten}
	case entry != nil:
		return FileReport{Path: file.Path, Status: StatusModified}
	}
	return FileReport{Path: file.Path, Status: StatusUnowned}
}

// makeDirectories creates the directories under generated/ that path needs,
// one at a time, each by its single name in its pinned parent, and pins each
// one it creates. A directory already found present is used through its pin
// and not looked at again by name, so one renamed since keeps the writes and
// a link put where it was is never followed. Something that appears where a
// directory is to be made (a link, a file) is refused rather than written
// through. It returns the report for a path that cannot be written, or nil.
func (t *tree) makeDirectories(path string) *FileReport {
	directory, _ := splitPath(path)
	if directory == "" {
		return nil
	}
	for _, step := range append(parents(directory), directory) {
		state := t.directory(step)
		if state.condition == directoryAbsent {
			handle, err := t.at(step)
			if err == nil {
				err = handle.Mkdir(0o755)
			}
			// Look again either way: pin what was made, or see what
			// appeared in the meantime.
			state = t.lookAtDirectory(step)
			if state.condition == directoryAbsent {
				state = directoryState{condition: directoryFailed, at: step, kind: kindOf(err)}
			}
			t.directories[step] = state
		}
		switch state.condition {
		case directoryLink:
			return &FileReport{Path: path, Status: StatusBlocked, Reason: ReasonDirectoryIsLink, At: state.at}
		case directoryNotDirectory:
			return &FileReport{Path: path, Status: StatusBlocked, Reason: ReasonDirectoryNotDirectory, At: state.at}
		case directoryFailed:
			return &FileReport{Path: path, Status: StatusFailed, At: state.at, Kind: state.kind}
		}
	}
	return nil
}

// removeTemporaries removes what an interrupted atomic write left behind:
// a regular file named ".<name>.tmp-<16 hex digits>" next to a file this
// package writes or lists, in a directory under generated/ that is a real
// directory. Nothing else of that shape is touched, and a failure to remove
// one is not a reason to stop: it is never listed or reported either way.
func (t *tree) removeTemporaries(paths []string) {
	known := map[string]map[string]bool{"": {ManifestName: true}}
	for _, path := range paths {
		directory, name := splitPath(path)
		if known[directory] == nil {
			known[directory] = map[string]bool{}
		}
		known[directory][name] = true
	}
	for directory, names := range known {
		if t.directory(directory).condition != directoryPresent {
			continue
		}
		handle, err := t.pinned(directory)
		if err != nil {
			continue
		}
		entries, err := handle.ReadDir()
		if err != nil {
			continue
		}
		for _, entry := range entries {
			base, temporary := temporaryBase(entry.Name())
			if !temporary || !names[base] || !entry.Type().IsRegular() {
				continue
			}
			if leftover, err := handle.Join(entry.Name()); err == nil {
				_ = leftover.Remove()
			}
		}
	}
}
