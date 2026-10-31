package codegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// project is one authorized root in a scratch directory, holding a pipeline
// directory with the user's own code in it. Nothing here is the real home or
// the real config directory.
type project struct {
	root  string // the authorized root
	pipe  string // the pipeline directory, root/pipe
	guard *projectfs.Guard
	dir   projectfs.Path
}

func newProject(t *testing.T) *project {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "project")
	pipe := filepath.Join(root, "pipe")
	write(t, filepath.Join(pipe, "pipeline.yaml"), "id: orders\n")
	write(t, filepath.Join(pipe, SourceDir, "handler.go"), "package src // the user's code\n")
	write(t, filepath.Join(root, "README.md"), "not a pipeline\n")
	guard, err := projectfs.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	dir, err := guard.Resolve(pipe)
	if err != nil {
		t.Fatal(err)
	}
	return &project{root: root, pipe: pipe, guard: guard, dir: dir}
}

func (p *project) generated(parts ...string) string {
	return filepath.Join(append([]string{p.pipe, GeneratedDir}, parts...)...)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
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

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func model(id string) *pipeline.Pipeline {
	return &pipeline.Pipeline{ID: pipeline.Identifier(id), Language: pipeline.LanguageGo}
}

// fakeContent is what the fake generator writes for a model.
func fakeContent(id string) string { return "// generated for " + id + "\n" }

// fake is the fake single-file generator: one file, its bytes a function of
// the model and nothing else.
var fake = GeneratorFunc("fake", func(m *pipeline.Pipeline) ([]File, error) {
	return []File{{Path: "pipeline.txt", Content: []byte(fakeContent(string(m.ID)))}}, nil
})

// emitting is a generator that returns exactly these files, whatever the
// model: path to content.
func emitting(files map[string]string) Generator {
	return GeneratorFunc("fixed", func(*pipeline.Pipeline) ([]File, error) {
		out := make([]File, 0, len(files))
		for path, content := range files {
			out = append(out, File{Path: path, Content: []byte(content)})
		}
		return out, nil
	})
}

func runOK(t *testing.T, p *project, m *pipeline.Pipeline, generators ...Generator) Report {
	t.Helper()
	report, err := Run(context.Background(), p.dir, m, generators...)
	if err != nil {
		t.Fatalf("run: %v; report %+v", err, report)
	}
	return report
}

// problemOf is the problem a run's error carries, checking the report holds
// the same one.
func problemOf(t *testing.T, report Report, err error) Problem {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("want a *codegen.Error, got %v", err)
	}
	if report.Problem == nil || *report.Problem != failure.Problem {
		t.Fatalf("the report's problem %+v is not the error's %+v", report.Problem, failure.Problem)
	}
	return failure.Problem
}

func statuses(report Report) map[string]Status {
	out := map[string]Status{}
	for _, file := range report.Files {
		out[file.Path] = file.Status
	}
	return out
}

func entryOf(t *testing.T, report Report, path string) FileReport {
	t.Helper()
	for _, file := range report.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("%q is not in the report %+v", path, report.Files)
	return FileReport{}
}

func wantStatuses(t *testing.T, report Report, want map[string]Status) {
	t.Helper()
	got := statuses(report)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("statuses: got %v, want %v", got, want)
	}
	if !sort.SliceIsSorted(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path }) {
		t.Fatalf("the report is not sorted by path: %+v", report.Files)
	}
}

func readManifestFile(t *testing.T, p *project) manifestDocument {
	t.Helper()
	var document manifestDocument
	if err := json.Unmarshal([]byte(read(t, p.generated(ManifestName))), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func manifestPaths(t *testing.T, p *project) []string {
	t.Helper()
	var out []string
	for _, entry := range readManifestFile(t, p).Files {
		out = append(out, entry.Path)
	}
	return out
}

// snapshot is everything under dir, byte for byte: each entry's type, mode,
// and content or link target, skipping the subtrees named in skip (absolute
// paths).
func snapshot(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, skipped := range skip {
			if path == skipped {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
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
			switch {
			case errors.Is(err, fs.ErrPermission):
				description += " (unreadable)"
			case err != nil:
				return err
			default:
				description += " " + string(data)
			}
		}
		out[path] = description
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// outside is a snapshot of the whole authorized root except generated/.
func (p *project) outside(t *testing.T) map[string]string {
	t.Helper()
	return snapshot(t, p.root, p.generated())
}

// whole is a snapshot of the whole authorized root, generated/ included.
func (p *project) whole(t *testing.T) map[string]string {
	t.Helper()
	return snapshot(t, p.root)
}

func sameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if fmt.Sprint(before) == fmt.Sprint(after) {
		return
	}
	var differences []string
	for path, description := range before {
		if after[path] != description {
			differences = append(differences, fmt.Sprintf("%s: %q -> %q", path, description, after[path]))
		}
	}
	for path, description := range after {
		if _, ok := before[path]; !ok {
			differences = append(differences, fmt.Sprintf("%s: created %q", path, description))
		}
	}
	sort.Strings(differences)
	t.Fatalf("%s changed:\n%s", what, strings.Join(differences, "\n"))
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not stop root")
	}
}
