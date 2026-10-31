package codegen

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// tree is generated/ inside one pipeline directory, looked at through the
// guard. Every path in it is relative to generated/.
type tree struct {
	generated projectfs.Path
	// exists says generated/ is there, as a directory.
	exists bool
	// directories remembers what each directory under generated/ was found
	// to be, so a path deep in an absent directory is not looked up.
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
// one above the directory asked about.
type directoryState struct {
	condition directoryCondition
	at        string
	kind      projectfs.ErrorKind
}

// openTree looks at generated/ inside dir. A generated/ that is a link, or
// not a directory, or that the guard refuses, is a problem for the whole run.
func openTree(dir projectfs.Path) (*tree, *Error) {
	generated, err := dir.Join(GeneratedDir)
	if err != nil {
		return nil, guardProblem(ProblemGuard, err)
	}
	t := &tree{generated: generated, directories: map[string]directoryState{}}
	info, err := generated.Lstat()
	switch {
	case projectfs.IsNotFound(err):
		return t, nil
	case err != nil:
		return nil, guardProblem(ProblemGuard, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, &Error{Problem: Problem{Kind: ProblemGeneratedIsLink}}
	case !info.IsDir():
		return nil, &Error{Problem: Problem{Kind: ProblemGeneratedNotDirectory}}
	}
	t.exists = true
	return t, nil
}

// at is the guard's handle to a path under generated/. Join does not follow
// links, which is why it is used segment by segment rather than resolving
// the whole path.
func (t *tree) at(path string) (projectfs.Path, error) {
	if path == "" {
		return t.generated, nil
	}
	return t.generated.Join(strings.Split(path, "/")...)
}

// directory is what the directory at path is, checking every directory on
// the way to it first. "" is generated/ itself.
func (t *tree) directory(path string) directoryState {
	if path == "" {
		if t.exists {
			return directoryState{condition: directoryPresent}
		}
		return directoryState{condition: directoryAbsent}
	}
	if state, known := t.directories[path]; known {
		return state
	}
	parent, _ := splitPath(path)
	state := t.directory(parent)
	if state.condition == directoryPresent {
		state = t.lookAtDirectory(path)
	}
	t.directories[path] = state
	return state
}

// lookAtDirectory describes one directory whose parent is a real directory.
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
	return directoryState{condition: directoryPresent}
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
// one at a time, checking each one that is already there again, so a link
// put in place since the run's checks is refused rather than written through.
// It returns the report for a path that cannot be written, or nil.
func (t *tree) makeDirectories(path string) *FileReport {
	directory, _ := splitPath(path)
	if directory == "" {
		return nil
	}
	for _, step := range append(parents(directory), directory) {
		delete(t.directories, step)
		state := t.lookAtDirectory(step)
		if state.condition == directoryAbsent {
			handle, err := t.at(step)
			if err == nil {
				err = handle.Mkdir(0o755)
			}
			if err == nil {
				state = directoryState{condition: directoryPresent}
			} else {
				// Something may have made it in the meantime; look again.
				state = t.lookAtDirectory(step)
				if state.condition == directoryAbsent {
					state = directoryState{condition: directoryFailed, at: step, kind: kindOf(err)}
				}
			}
		}
		t.directories[step] = state
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
		handle, err := t.at(directory)
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
