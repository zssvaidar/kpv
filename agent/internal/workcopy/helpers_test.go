package workcopy

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// world is a scratch home with a roots store under home/.config/kpv, a
// catalog over it and a working-copy store in the tree beside the roots
// store. Nothing here is the real home or ~/.config/kpv.
type world struct {
	t       *testing.T
	home    string
	config  string
	guard   *projectfs.Guard
	browser *projectfs.Browser
	roots   *projectfs.Roots
	catalog *catalog.Catalog
	store   *Store
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
	w := &world{t: t, home: home, config: filepath.Join(home, ".config", "kpv"), logs: &lockedBuffer{}}
	w.open(true)
	return w
}

// open builds the agent's objects over the scratch home, as a start of the
// agent does; withStore false is an agent with no config directory.
func (w *world) open(withStore bool) {
	t := w.t
	t.Helper()
	guard, err := projectfs.New()
	if err != nil {
		t.Fatal(err)
	}
	browser, err := projectfs.NewBrowser(guard, w.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	logger := slog.New(slog.NewTextHandler(w.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	storeFile := ""
	if withStore {
		storeFile = filepath.Join(w.config, "roots.json")
	}
	roots, err := projectfs.OpenRoots(browser, storeFile, logger)
	if err != nil {
		t.Fatal(err)
	}
	pipelines, err := catalog.New(roots, guard, logger)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := projectfs.OpenPrivateTree(roots, TreeName)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(pipelines, tree, logger)
	if err != nil {
		t.Fatal(err)
	}
	w.guard, w.browser, w.roots, w.catalog, w.store = guard, browser, roots, pipelines, store
}

// pipeline makes an authorized root holding a pipeline directory "app" with
// pipeline.yaml, src/main.py and .env, and answers the root and location.
func (w *world) pipeline() (string, catalog.Location) {
	w.t.Helper()
	root := filepath.Join(w.home, "project")
	mkdir(w.t, root)
	if _, err := w.roots.Authorize(root); err != nil {
		w.t.Fatal(err)
	}
	write(w.t, filepath.Join(root, "app", "pipeline.yaml"), "id: app\n")
	write(w.t, filepath.Join(root, "app", "src", "main.py"), "print('hi')\n")
	write(w.t, filepath.Join(root, "app", ".env"), "SECRET=do-not-copy\n")
	write(w.t, filepath.Join(root, "other.txt"), "beside\n")
	return root, catalog.Location{Root: root, Directory: "app"}
}

func (w *world) stage(location catalog.Location, changes ...Change) string {
	w.t.Helper()
	revision, err := w.store.Stage(location, changes...)
	if err != nil {
		w.t.Fatalf("Stage: %v", err)
	}
	return revision
}

func (w *world) review(location catalog.Location) *Review {
	w.t.Helper()
	review, err := w.store.Review(location)
	if err != nil {
		w.t.Fatalf("Review: %v", err)
	}
	return review
}

// treeFiles is the name of every file in the store's tree directory.
func (w *world) treeFiles() []string {
	w.t.Helper()
	entries, err := os.ReadDir(filepath.Join(w.config, TreeName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		w.t.Fatal(err)
	}
	out := []string{}
	for _, entry := range entries {
		out = append(out, entry.Name())
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

// snapshot describes every entry under dir, its mode, a link's target and a
// file's bytes, as internal/codegen's helpers_test.go does, so a test can
// compare a whole root byte for byte.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		description := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			description += " -> " + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			description += " " + string(data)
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = description
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sameTree fails when after differs from before anywhere but the paths in
// changed, listing each difference.
func sameTree(t *testing.T, before, after map[string]string, changed ...string) {
	t.Helper()
	skip := map[string]bool{}
	for _, path := range changed {
		skip[path] = true
	}
	var differences []string
	for path, description := range before {
		if !skip[path] && after[path] != description {
			differences = append(differences, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok && !skip[path] {
			differences = append(differences, path)
		}
	}
	sort.Strings(differences)
	if len(differences) > 0 {
		t.Fatalf("the root changed at %s", strings.Join(differences, ", "))
	}
}

func kindIs(t *testing.T, err error, want ErrorKind) *Error {
	t.Helper()
	var fault *Error
	if !errors.As(err, &fault) || fault.Kind != want {
		t.Fatalf("error %v, want kind %s", err, want)
	}
	return fault
}
