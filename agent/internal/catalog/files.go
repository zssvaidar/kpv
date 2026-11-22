package catalog

import (
	"io"
	"io/fs"
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// Files is what internal/workcopy (#43) reads and writes a pipeline
// directory through when it applies staged changes or restores a snapshot:
// the pipeline directory pinned as WriteSource pins it, every directory on a
// path pinned one name at a time (pinOne) and held until the operation ends,
// and files read with readSourceIn and written with Path.WriteAtomically.
// It shares the source paths' rules (sourceSegments) with two differences:
// pipeline.yaml at the top may be read and written, because #77 rewrites it,
// and generated/ is refused, because internal/codegen owns it.
//
// A Files exists only inside WithFiles, which holds Catalog.source for the
// whole of it, so an apply and a WriteSource to the same agent never
// interleave. It is not safe for use outside the function it was handed to.
// WriteSource's own code path is not changed by any of this.
type Files struct {
	c        *Catalog
	location Location
	dir      projectfs.Path
	dirInfo  fs.FileInfo
	pins     map[string]*heldPin
	order    []string
}

// heldPin is one directory on a path, held from the first time it was
// walked: the directory above it, its name there and what it was.
type heldPin struct {
	pin    *projectfs.PinnedDir
	parent projectfs.Path
	name   string
	info   fs.FileInfo
}

// Current is what is at a path in the pipeline directory now.
type Current struct {
	// Present is true when something is there.
	Present bool
	// Data is the file's exact bytes, set when Present and Kind is "".
	Data []byte
	// Kind is set when what is there is not a readable text file: KindLink,
	// KindNotRegularFile (also a file where a directory on the path should
	// be), KindTooLarge, KindNotText, KindUnreadable, or KindChanged for a
	// file swapped while it was read.
	Kind ErrorKind
}

// CheckStagedPath is the kind a path a working copy may hold is refused
// with, or "": a path sourceSegments refuses is KindInvalid, one under
// generated/ (ignoring letter case) KindGenerated. pipeline.yaml at the top
// is allowed.
func CheckStagedPath(path string) ErrorKind {
	segments, ok := sourceSegments(path)
	switch {
	case !ok:
		return KindInvalid
	case isGenerated(segments):
		return KindGenerated
	}
	return ""
}

// IsPipelineFile reports whether a valid staged path is the pipeline's own
// pipeline.yaml, ignoring letter case.
func IsPipelineFile(path string) bool {
	segments, ok := sourceSegments(path)
	return ok && isReserved(segments)
}

// CheckStagedText is the kind bytes a working copy may stage are refused
// with, or "": over MaxFileSize KindTooLarge, not UTF-8 or holding a NUL
// KindNotText, as WriteSource refuses a text.
func CheckStagedText(data []byte) ErrorKind {
	switch {
	case len(data) > MaxFileSize:
		return KindTooLarge
	case !isText(data):
		return KindNotText
	}
	return ""
}

// WithFiles pins the listed pipeline at location as WriteSource does and
// hands it to fn as a Files, holding Catalog.source until fn returns; every
// pin is released after. The pipeline answers as WriteSource's does:
// KindInvalid, KindNotAuthorized, KindRootUnavailable, KindNotFound,
// KindLink, KindChanged, KindUnreadable. Otherwise it answers what fn does.
func (c *Catalog) WithFiles(location Location, fn func(*Files) error) error {
	if !validLocation(location) {
		return &Error{Kind: KindInvalid}
	}
	c.source.Lock()
	defer c.source.Unlock()
	dir, release, err := c.pinPipelineDirectory(location)
	if err != nil {
		return err
	}
	defer release()
	info, err := dir.Stat()
	if err != nil {
		return c.unlessRootGone(location.Root, KindChanged)
	}
	files := &Files{c: c, location: location, dir: dir, dirInfo: info, pins: map[string]*heldPin{}}
	defer files.release()
	return fn(files)
}

func (f *Files) release() {
	for index := len(f.order) - 1; index >= 0; index-- {
		_ = f.pins[f.order[index]].pin.Close()
	}
	f.pins = map[string]*heldPin{}
	f.order = nil
}

// segmentsOf checks a staged path for Read, Write and Remove.
func segmentsOf(path string) ([]string, error) {
	if kind := CheckStagedPath(path); kind != "" {
		return nil, &Error{Kind: kind}
	}
	segments, _ := sourceSegments(path)
	return segments, nil
}

// parent walks the directories of a path, pinning each the first time, and
// answers the last one. A directory that is not there answers missing, and
// with create it is made (0755) inside the pinned directory above it first.
// A file where a directory should be answers notDirectory. A link, a swap or
// an unsearchable directory is the *Error pinOne answers.
func (f *Files) parent(directories []string, create bool) (dir projectfs.Path, missing, notDirectory bool, err error) {
	current := f.dir
	for index, name := range directories {
		key := strings.Join(directories[:index+1], "/")
		if held, ok := f.pins[key]; ok {
			current = held.pin.Path()
			continue
		}
		next, err := current.Join(name)
		if err != nil {
			return projectfs.Path{}, false, false, &Error{Kind: KindInvalid}
		}
		info, err := next.Lstat()
		switch {
		case err != nil && projectfs.KindOf(err) == projectfs.KindNotFound && create:
			if err := next.Mkdir(sourceDirectoryMode); err != nil && projectfs.KindOf(err) == projectfs.KindForbidden {
				return projectfs.Path{}, false, false, f.c.rootFault(f.location.Root, projectfs.KindForbidden)
			}
		case err != nil && (projectfs.KindOf(err) == projectfs.KindNotFound || projectfs.KindOf(err) == projectfs.KindNotDirectory):
			return projectfs.Path{}, true, false, nil
		case err == nil && info.Mode()&fs.ModeSymlink == 0 && !info.IsDir():
			return projectfs.Path{}, false, true, nil
		}
		pin, err := f.c.pinOne(f.location.Root, current, name)
		if err != nil {
			return projectfs.Path{}, false, false, err
		}
		held, err := pin.Path().Stat()
		if err != nil {
			_ = pin.Close()
			return projectfs.Path{}, false, false, f.c.unlessRootGone(f.location.Root, KindChanged)
		}
		f.pins[key] = &heldPin{pin: pin, parent: current, name: name, info: held}
		f.order = append(f.order, key)
		current = pin.Path()
	}
	return current, false, false, nil
}

// Read answers what is at path now. A missing file or directory on the way
// is not Present. A file that is there but is not readable text is Present
// with its Kind and no Data. The error is for the path itself: a path
// CheckStagedPath refuses, a link, swap or unsearchable directory on the way
// (KindLink, KindChanged, KindUnreadable), the root gone
// (KindNotAuthorized, KindRootUnavailable), or KindIO.
func (f *Files) Read(path string) (Current, error) {
	segments, err := segmentsOf(path)
	if err != nil {
		return Current{}, err
	}
	parent, missing, notDirectory, err := f.parent(segments[:len(segments)-1], false)
	switch {
	case err != nil:
		return Current{}, err
	case missing:
		return Current{}, nil
	case notDirectory:
		return Current{Present: true, Kind: KindNotRegularFile}, nil
	}
	data, err := f.c.readSourceIn(f.location.Root, parent, segments[len(segments)-1])
	if err == errNoSource {
		return Current{}, nil
	}
	switch KindOf(err) {
	case "":
		return Current{Present: true, Data: data}, nil
	case KindLink, KindNotRegularFile, KindTooLarge, KindNotText, KindUnreadable, KindChanged:
		return Current{Present: true, Kind: KindOf(err)}, nil
	}
	return Current{}, err
}

// Verify checks, right before anything is written, that the root is still
// available (asked as #67 asks) and still holds the pinned pipeline
// directory under its name, and that every directory walked so far is still
// the one pinned under its name: a link is KindLink, anything else
// KindChanged, and the root answers for itself.
func (f *Files) Verify() error {
	root := f.location.Root
	if fault := f.c.rootStatus(root); fault != nil {
		return fault
	}
	rootPin, failed := f.c.pinRoot(root)
	if failed != "" {
		return f.c.rootFault(root, failed)
	}
	defer func() { _ = rootPin.Close() }()
	var now fs.FileInfo
	var err error
	if f.location.Directory == "" {
		now, err = rootPin.Path().Stat()
	} else {
		named, joinErr := rootPin.Path().Join(f.location.Directory)
		if joinErr != nil {
			return &Error{Kind: KindInvalid}
		}
		now, err = named.Lstat()
	}
	if fault := f.same(now, err, f.dirInfo); fault != nil {
		return fault
	}
	for _, key := range f.order {
		held := f.pins[key]
		named, err := held.parent.Join(held.name)
		if err != nil {
			return &Error{Kind: KindInvalid}
		}
		now, err := named.Lstat()
		if fault := f.same(now, err, held.info); fault != nil {
			return fault
		}
	}
	return nil
}

// same is nil when now describes the directory held, and the *Error saying
// otherwise when not.
func (f *Files) same(now fs.FileInfo, err error, held fs.FileInfo) error {
	switch {
	case err != nil && projectfs.KindOf(err) == projectfs.KindForbidden:
		return f.c.rootFault(f.location.Root, projectfs.KindForbidden)
	case err != nil:
		return f.c.unlessRootGone(f.location.Root, KindChanged)
	case now.Mode()&fs.ModeSymlink != 0:
		return f.c.unlessRootGone(f.location.Root, KindLink)
	case !now.IsDir() || !sameFile(now, held):
		return f.c.unlessRootGone(f.location.Root, KindChanged)
	}
	return nil
}

// Write writes data to path with Path.WriteAtomically by its single name in
// the pinned directory holding it (0644 when new; an existing file keeps its
// mode), making each missing directory on the way (0755) inside the pinned
// directory above it, as WriteSource's ExpectedAbsent write does. Its
// errors are Read's, KindNotRegularFile for a file where a directory should
// be, and KindIO for a write that failed, which leaves the previous bytes.
func (f *Files) Write(path string, data []byte) error {
	segments, err := segmentsOf(path)
	if err != nil {
		return err
	}
	parent, _, notDirectory, err := f.parent(segments[:len(segments)-1], true)
	switch {
	case err != nil:
		return err
	case notDirectory:
		return &Error{Kind: KindNotRegularFile}
	}
	file, err := parent.Join(segments[len(segments)-1])
	if err != nil {
		return &Error{Kind: KindInvalid}
	}
	if err := file.WriteAtomically(fileMode, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}); err != nil {
		if projectfs.KindOf(err) == projectfs.KindForbidden {
			return f.c.rootFault(f.location.Root, projectfs.KindForbidden)
		}
		f.c.logger.Debug("a staged file was not written", "kind", string(projectfs.KindOf(err)))
		return &Error{Kind: KindIO}
	}
	return nil
}

// Remove removes the regular file at path and nothing else: a directory it
// leaves empty is kept. A file that is not there is no error; a link is
// KindLink and anything else not a regular file KindNotRegularFile, and
// neither is removed.
func (f *Files) Remove(path string) error {
	segments, err := segmentsOf(path)
	if err != nil {
		return err
	}
	parent, missing, notDirectory, err := f.parent(segments[:len(segments)-1], false)
	switch {
	case err != nil:
		return err
	case missing:
		return nil
	case notDirectory:
		return &Error{Kind: KindNotRegularFile}
	}
	file, err := parent.Join(segments[len(segments)-1])
	if err != nil {
		return &Error{Kind: KindInvalid}
	}
	info, err := file.Lstat()
	switch {
	case err != nil && projectfs.KindOf(err) == projectfs.KindNotFound:
		return nil
	case err != nil:
		return f.c.unlessRootGone(f.location.Root, KindIO)
	case info.Mode()&fs.ModeSymlink != 0:
		return &Error{Kind: KindLink}
	case !info.Mode().IsRegular():
		return &Error{Kind: KindNotRegularFile}
	}
	if err := file.Remove(); err != nil {
		if projectfs.KindOf(err) == projectfs.KindForbidden {
			return f.c.rootFault(f.location.Root, projectfs.KindForbidden)
		}
		return f.c.unlessRootGone(f.location.Root, KindIO)
	}
	return nil
}
