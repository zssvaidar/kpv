package projectfs

import (
	"io"
	"io/fs"
	"os"
)

// The methods here are Resolve followed by one operation on the result. They
// exist so the common case reads as one call, and they add nothing: the check
// and the enforcement are both the same ones Path performs.

// Open resolves path and opens it for reading.
func (g *Guard) Open(path string) (*os.File, error) {
	resolved, err := g.resolve("open", path)
	if err != nil {
		return nil, err
	}
	return resolved.Open()
}

// ReadFile resolves path and reads the whole file.
func (g *Guard) ReadFile(path string) ([]byte, error) {
	resolved, err := g.resolve("read", path)
	if err != nil {
		return nil, err
	}
	return resolved.ReadFile()
}

// WriteFile resolves path and writes data to it.
func (g *Guard) WriteFile(path string, data []byte, perm fs.FileMode) error {
	resolved, err := g.resolve("write", path)
	if err != nil {
		return err
	}
	return resolved.WriteFile(data, perm)
}

// WriteAtomically resolves path and replaces it with whatever emit writes,
// atomically.
func (g *Guard) WriteAtomically(path string, perm fs.FileMode, emit func(io.Writer) error) error {
	resolved, err := g.resolve("write", path)
	if err != nil {
		return err
	}
	return resolved.WriteAtomically(perm, emit)
}

// Stat resolves path and describes it.
func (g *Guard) Stat(path string) (fs.FileInfo, error) {
	resolved, err := g.resolve("stat", path)
	if err != nil {
		return nil, err
	}
	return resolved.Stat()
}

// ReadDir resolves path and reads the directory, entries sorted by name.
func (g *Guard) ReadDir(path string) ([]fs.DirEntry, error) {
	resolved, err := g.resolve("readdir", path)
	if err != nil {
		return nil, err
	}
	return resolved.ReadDir()
}

// Remove resolves path and removes it.
func (g *Guard) Remove(path string) error {
	resolved, err := g.resolve("remove", path)
	if err != nil {
		return err
	}
	return resolved.Remove()
}

// Rename resolves both paths and renames the first to the second. Both must be
// inside the same authorized root.
func (g *Guard) Rename(from, to string) error {
	source, err := g.resolve("rename", from)
	if err != nil {
		return err
	}
	destination, err := g.resolve("rename", to)
	if err != nil {
		return err
	}
	return source.RenameTo(destination)
}
