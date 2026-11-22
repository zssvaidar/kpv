package catalog

import (
	"bytes"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// A source file is a file inside a pipeline directory that a custom-code
// block's customCode.path names (#87): ReadSource reads one and WriteSource
// writes one, addressed by the pipeline's Location and the path relative to
// the pipeline directory, the one holding pipeline.yaml.
//
// The agent decides nothing about a file by its name but two things: a path
// whose first segment is generated (ignoring letter case) is the tool's
// (internal/codegen), readable but never written here, and pipeline.yaml at
// the top of the pipeline directory (ignoring letter case) is the pipeline
// itself, which is neither read nor written as a source file.
//
// The pipeline directory and every directory on the path are pinned
// (projectfs.Path.Pin) one name at a time, never through a symbolic link, and
// the file is read or written by its single name in the last pinned
// directory. Nothing outside the pipeline directory is read, created or
// changed.

// generatedDirectory is the first segment of a path that belongs to the tool.
const generatedDirectory = "generated"

// sourceDirectoryMode is the mode a directory a write creates gets.
const sourceDirectoryMode fs.FileMode = 0o755

// maxSourcePath is customCodeReference.path's maxLength. The pattern allows
// ASCII only, so characters are bytes.
const maxSourcePath = 512

// sourcePathPattern is customCodeReference.path's pattern in
// schema/pipeline.schema.json, which schema/source.schema.json's sourcePath
// copies (generation holds the two to the same rule).
var sourcePathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// SourceFile is one source file read from a pipeline directory.
type SourceFile struct {
	Location
	// Path is the path as the request gave it.
	Path string
	// Text is the file's exact bytes: UTF-8, no NUL, at most MaxFileSize.
	Text []byte
	// SHA256 is the lowercase hex SHA-256 of Text.
	SHA256 string
	// Generated is true when the path is under generated/.
	Generated bool
}

// SourceWrite is a write of one source file. Exactly one of ExpectedSHA256
// and ExpectedAbsent is set.
type SourceWrite struct {
	Location
	Path string
	Text string
	// ExpectedSHA256 is the hash the file must still have; a file with
	// another is KindChanged and is not written.
	ExpectedSHA256 string
	// ExpectedAbsent is a write that creates the file; a file already there
	// is KindExists and is not written.
	ExpectedAbsent bool
}

// sourceSegments splits a source path into its segments, or answers false for
// one this package will not reason about: anything the schema's pattern or
// length refuses, a backslash, a NUL, and an empty, "." or ".." segment or one
// starting with "." (a trailing "/" is an empty segment). The pattern lets
// src/../../x and src/.env through, so this check is the agent's.
func sourceSegments(path string) ([]string, bool) {
	if path == "" || len(path) > maxSourcePath || strings.ContainsAny(path, "\\\x00") || !sourcePathPattern.MatchString(path) {
		return nil, false
	}
	segments := strings.Split(path, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return nil, false
		}
	}
	return segments, true
}

// isGenerated reports whether a path is under generated/, ignoring letter
// case, because the disk may.
func isGenerated(segments []string) bool {
	return strings.EqualFold(segments[0], generatedDirectory)
}

// isReserved reports whether a path is the pipeline's own pipeline.yaml,
// ignoring letter case, because the disk may.
func isReserved(segments []string) bool {
	return len(segments) == 1 && strings.EqualFold(segments[0], FileName)
}

// ReadSource reads the source file at path in the pipeline at location.
//
// A location that is not a single directory name, or a path sourceSegments
// refuses, is KindInvalid and nothing is touched; pipeline.yaml is
// KindReserved. The root answers as Open answers it (KindNotAuthorized,
// KindRootUnavailable), and a directory that is not a listed pipeline is
// KindNotFound. A missing file is KindNotFound; a link on the path
// (the pipeline directory, a directory under it, the file) is KindLink, or
// KindChanged when it was swapped in while being opened; a directory, FIFO or
// device is KindNotRegularFile; a file over MaxFileSize KindTooLarge, not read
// past the limit; bytes that are not UTF-8 or hold a NUL KindNotText. The
// bytes are answered exactly as read.
func (c *Catalog) ReadSource(location Location, path string) (*SourceFile, error) {
	segments, ok := sourceSegments(path)
	if !ok || !validLocation(location) {
		return nil, &Error{Kind: KindInvalid}
	}
	if isReserved(segments) {
		return nil, &Error{Kind: KindReserved}
	}
	dir, release, err := c.pinPipelineDirectory(location)
	if err != nil {
		return nil, err
	}
	defer release()
	parent, releaseParent, err := c.pinSourceParent(location.Root, dir, segments[:len(segments)-1], false)
	if err != nil {
		return nil, err
	}
	defer releaseParent()
	data, err := c.readSourceIn(location.Root, parent, segments[len(segments)-1])
	if err == nil {
		err = c.rootStatusError(location.Root)
	}
	if err != nil {
		return nil, err
	}
	return &SourceFile{
		Location: location, Path: path, Text: data, SHA256: digest(data), Generated: isGenerated(segments),
	}, nil
}

// WriteSource writes the source file at path in the pipeline at location and
// answers the SHA-256 of the bytes written, which are exactly write.Text.
//
// Every refusal ReadSource has applies, checked the same way; a path under
// generated/ is KindGenerated, and a text over MaxFileSize KindTooLarge and
// one holding a NUL KindNotText, before anything is touched. With
// ExpectedSHA256, a file that is not there is KindNotFound and one with
// another hash KindChanged with its current hash in Error.SHA256; with
// ExpectedAbsent, a file that is there is KindExists. Neither writes
// anything. A directory missing on the path is created inside the pinned
// directory above it (0755) only for ExpectedAbsent, and the file is written
// with Path.WriteAtomically by its single name in the pinned directory that
// holds it (0644 when new; an existing file keeps its mode), so a write that
// fails part-way leaves the previous bytes in place. Writes are serialized,
// so a second write with the same expected hash sees the first's and is
// KindChanged. A directory a failed write created is left behind, empty.
func (c *Catalog) WriteSource(write SourceWrite) (string, error) {
	segments, ok := sourceSegments(write.Path)
	if !ok || !validLocation(write.Location) || write.ExpectedAbsent == (write.ExpectedSHA256 != "") {
		return "", &Error{Kind: KindInvalid}
	}
	switch {
	case isReserved(segments):
		return "", &Error{Kind: KindReserved}
	case isGenerated(segments):
		return "", &Error{Kind: KindGenerated}
	case len(write.Text) > MaxFileSize:
		return "", &Error{Kind: KindTooLarge}
	case !isText([]byte(write.Text)):
		return "", &Error{Kind: KindNotText}
	}

	c.source.Lock()
	defer c.source.Unlock()

	dir, release, err := c.pinPipelineDirectory(write.Location)
	if err != nil {
		return "", err
	}
	defer release()
	parent, releaseParent, err := c.pinSourceParent(write.Root, dir, segments[:len(segments)-1], write.ExpectedAbsent)
	if err != nil {
		return "", err
	}
	defer releaseParent()

	name := segments[len(segments)-1]
	current, err := c.readSourceIn(write.Root, parent, name)
	switch {
	case err == errNoSource && write.ExpectedAbsent:
	case err != nil:
		return "", err
	case write.ExpectedAbsent:
		return "", &Error{Kind: KindExists}
	case digest(current) != write.ExpectedSHA256:
		return "", &Error{Kind: KindChanged, SHA256: digest(current)}
	}
	if fault := c.rootStatusError(write.Root); fault != nil {
		return "", fault
	}

	file, err := parent.Join(name)
	if err != nil {
		return "", &Error{Kind: KindInvalid}
	}
	data := []byte(write.Text)
	if err := file.WriteAtomically(fileMode, func(w io.Writer) error {
		half := len(data) / 2
		if _, err := w.Write(data[:half]); err != nil {
			return err
		}
		if c.hook.midSourceWrite != nil {
			if err := c.hook.midSourceWrite(); err != nil {
				return err
			}
		}
		_, err := w.Write(data[half:])
		return err
	}); err != nil {
		if projectfs.KindOf(err) == projectfs.KindForbidden {
			return "", c.rootFault(write.Root, projectfs.KindForbidden)
		}
		c.logger.Debug("a source file was not written", "kind", string(projectfs.KindOf(err)))
		return "", &Error{Kind: KindIO}
	}
	return digest(data), nil
}

// errNoSource is a source file that is not there.
var errNoSource = &Error{Kind: KindNotFound}

// validLocation reports whether a location is one Open would look at: a root,
// and a directory that is absent or a single name.
func validLocation(location Location) bool {
	return location.Root != "" && (location.Directory == "" || validDirectory(location.Directory))
}

// isText reports whether data is UTF-8 with no NUL.
func isText(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

// rootStatusError is rootStatus as an error, nil when the root is available.
func (c *Catalog) rootStatusError(root string) error {
	if fault := c.rootStatus(root); fault != nil {
		return fault
	}
	return nil
}

// pinPipelineDirectory pins the pipeline directory at location, as Open finds
// it, and answers it with the function that releases every pin it took. Only
// a listed pipeline is answered: a directory starting with ".", one in a root
// that is itself a pipeline, or one without pipeline.yaml is KindNotFound. A
// pipeline directory that is a link is KindLink, one swapped while it was
// pinned KindChanged, and one that cannot be searched KindUnreadable.
func (c *Catalog) pinPipelineDirectory(location Location) (projectfs.Path, func(), error) {
	none := func() {}
	if fault := c.rootStatus(location.Root); fault != nil {
		return projectfs.Path{}, none, fault
	}
	rootPin, failed := c.pinRoot(location.Root)
	if failed != "" {
		return projectfs.Path{}, none, c.rootFault(location.Root, failed)
	}
	release := func() { _ = rootPin.Close() }
	dir := rootPin.Path()
	if location.Directory != "" {
		if strings.HasPrefix(location.Directory, ".") {
			release()
			return projectfs.Path{}, none, &Error{Kind: KindNotFound}
		}
		// The list does not look inside a root that is itself a pipeline.
		if file, err := dir.Join(FileName); err == nil {
			if _, err := file.Lstat(); err == nil {
				release()
				return projectfs.Path{}, none, &Error{Kind: KindNotFound}
			}
		}
		pin, err := c.pinOne(location.Root, dir, location.Directory)
		if err != nil {
			release()
			return projectfs.Path{}, none, err
		}
		releaseRoot := release
		release = func() { _ = pin.Close(); releaseRoot() }
		dir = pin.Path()
	}
	file, err := dir.Join(FileName)
	if err != nil {
		release()
		return projectfs.Path{}, none, &Error{Kind: KindIO}
	}
	if _, err := file.Lstat(); err != nil {
		switch projectfs.KindOf(err) {
		case projectfs.KindNotFound, projectfs.KindNotDirectory:
			release()
			return projectfs.Path{}, none, c.notFoundUnlessRootGone(location.Root)
		case projectfs.KindForbidden:
			release()
			return projectfs.Path{}, none, c.rootFault(location.Root, projectfs.KindForbidden)
		}
		// Any other failure to describe it is an unreadable entry the list
		// shows; what is asked for is the file beside it.
	}
	return dir, release, nil
}

// pinSourceParent pins each directory of a source path below dir, one name at
// a time, and answers the last one (dir itself for a file at the top) with the
// function that releases the pins it took. A missing directory is
// KindNotFound unless create is set, when it is made inside the pinned
// directory above it; a file where a directory should be is KindNotFound.
func (c *Catalog) pinSourceParent(root string, dir projectfs.Path, directories []string, create bool) (projectfs.Path, func(), error) {
	var pins []*projectfs.PinnedDir
	release := func() {
		for index := len(pins) - 1; index >= 0; index-- {
			_ = pins[index].Close()
		}
	}
	current := dir
	for _, name := range directories {
		next, err := current.Join(name)
		if err != nil {
			release()
			return projectfs.Path{}, func() {}, &Error{Kind: KindInvalid}
		}
		if _, err := next.Lstat(); err != nil && projectfs.KindOf(err) == projectfs.KindNotFound && create {
			if err := next.Mkdir(sourceDirectoryMode); err != nil && projectfs.KindOf(err) == projectfs.KindForbidden {
				release()
				return projectfs.Path{}, func() {}, c.rootFault(root, projectfs.KindForbidden)
			}
			// Whatever is there now, made here or raced in, is pinned and
			// looked at below like any other directory on the path.
		}
		pin, err := c.pinOne(root, current, name)
		if err != nil {
			release()
			return projectfs.Path{}, func() {}, err
		}
		pins = append(pins, pin)
		current = pin.Path()
	}
	return current, release, nil
}

// pinOne pins the directory name in the pinned directory dir. A link is
// KindLink, a directory that changed while it was opened KindChanged, one that
// cannot be searched KindUnreadable, one that is missing or not a directory
// KindNotFound, and a root that went away answers for itself.
func (c *Catalog) pinOne(root string, dir projectfs.Path, name string) (*projectfs.PinnedDir, error) {
	next, err := dir.Join(name)
	if err != nil {
		return nil, &Error{Kind: KindInvalid}
	}
	info, err := next.Lstat()
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, c.unlessRootGone(root, KindLink)
	}
	if c.hook.beforeSourcePin != nil {
		c.hook.beforeSourcePin(name)
	}
	pin, err := next.Pin()
	if err == nil {
		return pin, nil
	}
	switch projectfs.KindOf(err) {
	case projectfs.KindForbidden:
		return nil, c.rootFault(root, projectfs.KindForbidden)
	case projectfs.KindChanged:
		// A link, a swap, or a directory that cannot be searched, which Pin
		// cannot tell apart; the name is looked at again.
		again, lstatErr := next.Lstat()
		switch {
		case lstatErr == nil && again.Mode()&fs.ModeSymlink != 0:
			return nil, c.unlessRootGone(root, KindLink)
		case lstatErr == nil && again.IsDir() && info != nil && sameFile(info, again):
			return nil, c.unlessRootGone(root, KindUnreadable)
		}
		return nil, c.unlessRootGone(root, KindChanged)
	case projectfs.KindNotFound, projectfs.KindNotDirectory:
		return nil, c.unlessRootGone(root, KindNotFound)
	case projectfs.KindUnreadable:
		return nil, c.unlessRootGone(root, KindUnreadable)
	}
	return nil, c.unlessRootGone(root, KindIO)
}

// unlessRootGone is an *Error of kind, unless the root itself has gone
// meanwhile, which is then the answer.
func (c *Catalog) unlessRootGone(root string, kind ErrorKind) *Error {
	if fault := c.rootStatus(root); fault != nil {
		return fault
	}
	if kind == KindUnreadable {
		return &Error{Kind: KindUnreadable, Unreadable: unreadable(UnreadableUnreadable)}
	}
	return &Error{Kind: kind}
}

// readSourceIn reads the file name in the pinned directory dir, by its single
// name, and answers its bytes, or errNoSource when it is not there, or the
// *Error saying why it was not read.
//
// The name is described before it is opened, and read only when it is a
// regular file of at most MaxFileSize. The open file must be the file
// described (sameFile), so a link or another file swapped in at that moment
// is KindChanged rather than read, and the name is described once more after
// the read. A FIFO swapped in between the Lstat and the open is the window
// left, as for readIn: the open waits for a writer.
func (c *Catalog) readSourceIn(root string, dir projectfs.Path, name string) ([]byte, error) {
	file, err := dir.Join(name)
	if err != nil {
		return nil, &Error{Kind: KindInvalid}
	}
	failure := func(err error) error {
		switch projectfs.KindOf(err) {
		case projectfs.KindNotFound, projectfs.KindNotDirectory:
			if fault := c.rootStatus(root); fault != nil {
				return fault
			}
			return errNoSource
		case projectfs.KindForbidden:
			// The pinned directory refuses a link that leaves it.
			if again, lstatErr := file.Lstat(); lstatErr == nil && again.Mode()&fs.ModeSymlink != 0 {
				return c.unlessRootGone(root, KindLink)
			}
			return c.rootFault(root, projectfs.KindForbidden)
		case projectfs.KindUnreadable:
			return c.unlessRootGone(root, KindUnreadable)
		}
		return c.unlessRootGone(root, KindIO)
	}
	info, err := file.Lstat()
	switch {
	case err != nil:
		return nil, failure(err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, &Error{Kind: KindLink}
	case !info.Mode().IsRegular():
		return nil, &Error{Kind: KindNotRegularFile}
	case info.Size() > MaxFileSize:
		return nil, &Error{Kind: KindTooLarge}
	}
	opened, err := file.Open()
	if err != nil {
		return nil, failure(err)
	}
	defer func() { _ = opened.Close() }()
	described, err := opened.Stat()
	switch {
	case err != nil:
		return nil, &Error{Kind: KindIO}
	case !sameFile(info, described):
		return nil, &Error{Kind: KindChanged}
	case !described.Mode().IsRegular():
		return nil, &Error{Kind: KindNotRegularFile}
	case described.Size() > MaxFileSize:
		return nil, &Error{Kind: KindTooLarge}
	}
	data, err := io.ReadAll(io.LimitReader(opened, MaxFileSize+1))
	switch {
	case err != nil:
		return nil, &Error{Kind: KindIO}
	case len(data) > MaxFileSize:
		return nil, &Error{Kind: KindTooLarge}
	}
	if after, err := file.Lstat(); err != nil || after.Mode()&fs.ModeSymlink != 0 || !sameFile(info, after) {
		if err == nil && after.Mode()&fs.ModeSymlink != 0 {
			return nil, &Error{Kind: KindLink}
		}
		return nil, &Error{Kind: KindChanged}
	}
	if !isText(data) {
		return nil, &Error{Kind: KindNotText}
	}
	return data, nil
}
