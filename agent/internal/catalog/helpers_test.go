package catalog

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// world is a scratch home directory with a roots store in it, and a catalog
// over those roots. Nothing here is the real home or ~/.config/kpv.
type world struct {
	t       *testing.T
	home    string
	guard   *projectfs.Guard
	browser *projectfs.Browser
	roots   *projectfs.Roots
	catalog *Catalog
	logs    *lockedBuffer
}

type lockedBuffer struct {
	mutex sync.Mutex
	buf   bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buf.String()
}

func newWorld(t *testing.T) *world {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	mkdir(t, home)
	guard, err := projectfs.New()
	if err != nil {
		t.Fatal(err)
	}
	browser, err := projectfs.NewBrowser(guard, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	roots, err := projectfs.OpenRoots(browser, filepath.Join(home, ".config", "kpv", "roots.json"), logger)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := New(roots, guard, logger)
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, home: home, guard: guard, browser: browser, roots: roots, catalog: catalog, logs: logs}
}

// root makes a directory under home and authorizes it, returning its path.
func (w *world) root(name string) string {
	w.t.Helper()
	path := filepath.Join(w.home, name)
	mkdir(w.t, path)
	if _, err := w.roots.Authorize(path); err != nil {
		w.t.Fatalf("authorize %s: %v", name, err)
	}
	return path
}

// listed is the one root at path in a list, failing when it is not there.
func listed(t *testing.T, list List, path string) Root {
	t.Helper()
	for _, root := range list.Roots {
		if root.Path == path {
			return root
		}
	}
	t.Fatalf("root %s is not in the list %+v", path, list)
	return Root{}
}

// entry is the entry for directory in root, failing when there is none.
func entry(t *testing.T, root Root, directory string) Entry {
	t.Helper()
	for _, found := range root.Pipelines {
		if found.Directory == directory {
			return found
		}
	}
	t.Fatalf("no entry %q in %s: %+v", directory, root.Path, root.Pipelines)
	return Entry{}
}

func directories(root Root) []string {
	out := []string{}
	for _, found := range root.Pipelines {
		out = append(out, found.Directory)
	}
	return out
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// valid is a pipeline.yaml that loads, with the given id.
func valid(id string) string {
	return "id: " + id + "\nname: The " + id + " pipeline\nlanguage: python\nsource:\n  id: in\n  type: kafkaSource\n  topic: in.v1\n" +
		"destination:\n  id: out\n  type: kafkaDestination\n  topic: out.v1\nruntimeConfiguration:\n  networkMode: docker\n"
}

// pipelineAt writes a valid pipeline with id into root/directory.
func pipelineAt(t *testing.T, root, directory, id string) {
	t.Helper()
	write(t, filepath.Join(root, directory, FileName), valid(id))
}

func kindOf(t *testing.T, err error) ErrorKind {
	t.Helper()
	fault, ok := err.(*Error)
	if !ok {
		t.Fatalf("the failure is %T, not a *catalog.Error: %v", err, err)
	}
	return fault.Kind
}

// snapshot is every path under directory with its kind and content, so a
// test can show that nothing on disk changed.
func snapshot(t *testing.T, directory string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(directory, path)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			out[rel] = "link:" + target
		case info.IsDir():
			out[rel] = "dir:" + info.Mode().String()
		default:
			data, _ := os.ReadFile(path)
			out[rel] = "file:" + info.Mode().String() + ":" + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
