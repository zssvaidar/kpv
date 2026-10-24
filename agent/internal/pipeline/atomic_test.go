// The atomicity of a write, tested by making a real write fail: an internal
// test, because the invariant belongs to writeFileAtomically and the point is
// to fail inside it rather than to read it and believe it.

package pipeline

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const previousGoodFile = "id: previous\nlanguage: python\n"

func TestAFailedWritePartwayThroughKeepsThePreviousFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "pipeline.yaml")
	if err := os.WriteFile(path, []byte(previousGoodFile), 0o644); err != nil {
		t.Fatalf("write the previous file: %v", err)
	}

	// The failure is a real one: bytes reach a real temporary file, the file is
	// flushed, and only then does the write give up, which is the shape of a
	// disk filling up halfway through.
	wantErr := errors.New("no space left on device")
	var temporary string
	var partial int
	err := writeFileAtomically(path, 0o644, func(w io.Writer) error {
		written, writeErr := w.Write([]byte("id: replacement\nlanguage: go\n"))
		if writeErr != nil {
			return writeErr
		}
		partial = written
		file, ok := w.(*os.File)
		if !ok {
			t.Fatalf("the write goes to a %T, not a file", w)
		}
		if err := file.Sync(); err != nil {
			t.Fatalf("flush the temporary file: %v", err)
		}
		temporary = file.Name()
		onDisk, readErr := os.ReadFile(temporary)
		if readErr != nil {
			t.Fatalf("read the temporary file: %v", readErr)
		}
		if len(onDisk) == 0 {
			t.Fatal("nothing reached the temporary file, so this proves nothing")
		}
		return fmt.Errorf("after %d bytes: %w", written, wantErr)
	})

	if partial == 0 {
		t.Fatal("the write never got as far as writing anything")
	}
	var fault *FileError
	if !errors.As(err, &fault) {
		t.Fatalf("the failure is %T, not a *FileError: %v", err, err)
	}
	if fault.Kind != KindIO {
		t.Errorf("failed as %q, want %q: %v", fault.Kind, KindIO, fault)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("the failure does not carry its cause: %v", err)
	}

	if content, readErr := os.ReadFile(path); readErr != nil {
		t.Errorf("the previous file is gone: %v", readErr)
	} else if string(content) != previousGoodFile {
		t.Errorf("the previous file was replaced by a partial write:\n%s", content)
	}
	if temporary != "" {
		if _, statErr := os.Stat(temporary); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("the temporary file %s outlived the failed write", temporary)
		}
	}
	if left := entries(t, directory); len(left) != 1 || left[0] != "pipeline.yaml" {
		t.Errorf("the directory holds %v, want just the pipeline file", left)
	}
}

func TestAFailedRenameKeepsThePreviousFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "pipeline.yaml")
	if err := os.WriteFile(path, []byte(previousGoodFile), 0o644); err != nil {
		t.Fatalf("write the previous file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })

	// The whole write succeeds, and the commit is what fails: renaming over
	// the destination needs write permission on the directory, which is taken
	// away once the temporary file is already complete.
	err := writeFileAtomically(path, 0o644, func(w io.Writer) error {
		if _, writeErr := w.Write([]byte("id: replacement\nlanguage: go\n")); writeErr != nil {
			return writeErr
		}
		return os.Chmod(directory, 0o500)
	})

	var fault *FileError
	if !errors.As(err, &fault) {
		t.Fatalf("the failure is %T, not a *FileError: %v", err, err)
	}
	if fault.Kind != KindIO {
		t.Errorf("failed as %q, want %q: %v", fault.Kind, KindIO, fault)
	}
	if fault.Path != path {
		t.Errorf("the failure names %q, want the file %q", fault.Path, path)
	}

	if content, readErr := os.ReadFile(path); readErr != nil {
		t.Errorf("the previous file is gone: %v", readErr)
	} else if string(content) != previousGoodFile {
		t.Errorf("a write that could not be committed replaced the previous file:\n%s", content)
	}
}

func TestAWriteThatSucceedsReplacesTheFileAndKeepsItsMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "pipeline.yaml")
	if err := os.WriteFile(path, []byte(previousGoodFile), 0o600); err != nil {
		t.Fatalf("write the previous file: %v", err)
	}

	const replacement = "id: replacement\nlanguage: go\n"
	if err := writeFileAtomically(path, 0o600, func(w io.Writer) error {
		_, writeErr := w.Write([]byte(replacement))
		return writeErr
	}); err != nil {
		t.Fatalf("writeFileAtomically: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the file: %v", err)
	}
	if string(content) != replacement {
		t.Errorf("the file holds %q, want the replacement", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the file has mode %v, want 0600", info.Mode().Perm())
	}
	if left := entries(t, directory); len(left) != 1 {
		t.Errorf("the directory holds %v, want just the pipeline file", left)
	}
}

func entries(t *testing.T, directory string) []string {
	t.Helper()
	found, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the directory: %v", err)
	}
	names := make([]string, 0, len(found))
	for _, entry := range found {
		names = append(names, entry.Name())
	}
	return names
}
