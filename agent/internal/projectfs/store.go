package projectfs

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The store of authorized roots (#63) is one small JSON file the agent owns,
// under the user's config directory and outside every project:
//
//	{"version": 1, "roots": ["/home/user/code/app", ...]}
//
// It holds real paths and nothing else. In particular it never holds the
// session token, which this package is never given.
const (
	storeVersion = 1
	// storeFileMode and storeDirectoryMode keep the file to its owner.
	storeFileMode      fs.FileMode = 0o600
	storeDirectoryMode fs.FileMode = 0o700
	// maxStoreBytes is far more than any real set of roots needs; a file
	// larger than this is treated as corrupt rather than read.
	maxStoreBytes = 1 << 20
	// The two ways a store can fail to load, reported by kind only.
	storeCorrupt    ErrorKind = "corrupt"
	storeUnreadable ErrorKind = "unreadable"
)

type storeDocument struct {
	Version int      `json:"version"`
	Roots   []string `json:"roots"`
}

type rootStore struct {
	file string
	// real is the store directory's real path, which no root may contain or
	// be inside: a project that held the store could rewrite the set of
	// directories the agent may write to.
	real string
	// pendingBackup says the file on disk could not be read, and has to be
	// moved aside before anything is written over it.
	pendingBackup bool
}

func newRootStore(file string) *rootStore {
	file = filepath.Clean(file)
	store := &rootStore{file: file, real: filepath.Dir(file)}
	// The directory need not exist yet: it is made by the first write. Its
	// real path is what it will be, the existing part resolved and the rest
	// as written.
	if reached, missing, err := realPath(store.real); err == nil {
		store.real = filepath.Join(append([]string{reached}, missing...)...)
	}
	return store
}

// overlaps reports whether a root at real would contain the store directory
// or be inside it.
func (s *rootStore) overlaps(realDir string) bool {
	if _, ok := within(realDir, s.real); ok {
		return true
	}
	_, ok := within(s.real, realDir)
	return ok
}

// load reads the stored paths. A missing file is no roots and no problem; a
// file that cannot be read, or is not exactly the document above, is no roots
// and a problem kind, and is marked to be moved aside before any write.
func (s *rootStore) load() ([]string, ErrorKind) {
	file, err := os.Open(s.file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		s.pendingBackup = true
		return nil, storeUnreadable
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxStoreBytes+1))
	_ = file.Close()
	if err != nil {
		s.pendingBackup = true
		return nil, storeUnreadable
	}
	paths, ok := parseStore(raw)
	if !ok {
		s.pendingBackup = true
		return nil, storeCorrupt
	}
	return paths, ""
}

// parseStore accepts exactly one document of the store's shape, every root an
// absolute, clean path. A duplicate is dropped rather than refused: it can
// only come from a hand edit, and it means nothing.
func parseStore(raw []byte) ([]string, bool) {
	if len(raw) > maxStoreBytes {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document storeDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	if document.Version != storeVersion || document.Roots == nil {
		return nil, false
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(document.Roots))
	for _, path := range document.Roots {
		if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, false
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out, true
}

// moveAside renames a store that could not be read to a backup of its own,
// never over an earlier backup, and clears the mark once it is out of the way.
func (s *rootStore) moveAside() error {
	if !s.pendingBackup {
		return nil
	}
	if _, err := os.Lstat(s.file); errors.Is(err, fs.ErrNotExist) {
		s.pendingBackup = false
		return nil
	}
	base := s.file + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	for attempt := 0; attempt < 100; attempt++ {
		name := base
		if attempt > 0 {
			name += "-" + strconv.Itoa(attempt)
		}
		if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := os.Rename(s.file, name); err != nil {
			return &PathError{Op: "store", Kind: KindIO, Message: "the unreadable store could not be moved aside", Err: err}
		}
		s.pendingBackup = false
		return nil
	}
	return &PathError{Op: "store", Kind: KindIO, Message: "no free name was found to move the unreadable store aside to"}
}

// save replaces the store with paths, atomically: a new file only its owner
// can read, written in full and flushed, then renamed over the old one. A
// store that could not be read is moved aside first, and if it cannot be,
// nothing is written.
func (s *rootStore) save(paths []string) error {
	fail := func(message string, err error) error {
		return &PathError{Op: "store", Kind: KindIO, Message: message, Err: err}
	}
	if err := s.moveAside(); err != nil {
		return err
	}
	directory := filepath.Dir(s.file)
	if err := os.MkdirAll(directory, storeDirectoryMode); err != nil {
		return fail("the store's directory could not be made", err)
	}
	body, err := json.MarshalIndent(storeDocument{Version: storeVersion, Roots: append([]string{}, paths...)}, "", "  ")
	if err != nil {
		return fail("the store could not be encoded", err)
	}
	body = append(body, '\n')

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fail("a temporary name for the store could not be generated", err)
	}
	temporary := filepath.Join(directory, "."+filepath.Base(s.file)+".tmp-"+hex.EncodeToString(suffix))
	file, err := os.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL, storeFileMode)
	if err != nil {
		return fail("the store could not be written", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = file.Close()
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return fail("the store could not be written", err)
	}
	// The mode is set again explicitly: a umask can only take bits away, but
	// a file this sensitive should not depend on what the umask was.
	if err := file.Chmod(storeFileMode); err != nil {
		return fail("the store could not be given its mode", err)
	}
	if err := file.Sync(); err != nil {
		return fail("the store could not be flushed", err)
	}
	if err := file.Close(); err != nil {
		return fail("the store could not be closed", err)
	}
	if err := os.Rename(temporary, s.file); err != nil {
		return fail("the store could not be replaced", err)
	}
	committed = true
	if parent, err := os.Open(directory); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}
