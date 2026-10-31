package secrets

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// project is one authorized root in a scratch directory, holding a pipeline
// directory with a pipeline file, the user's code and generated output in it.
// Nothing here is the real home or the real config directory.
type project struct {
	t     *testing.T
	root  string
	pipe  string
	guard *projectfs.Guard
	files *Files
	logs  *logBuffer
}

// logBuffer collects every log line, as text and as JSON.
type logBuffer struct {
	mutex sync.Mutex
	text  bytes.Buffer
	json  bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.text.Write(p)
}

type jsonSink struct{ b *logBuffer }

func (s jsonSink) Write(p []byte) (int, error) {
	s.b.mutex.Lock()
	defer s.b.mutex.Unlock()
	return s.b.json.Write(p)
}

func (b *logBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.text.String() + b.json.String()
}

// fanout sends every record to two handlers.
type fanout struct{ handlers []slog.Handler }

func (f fanout) Enabled(context.Context, slog.Level) bool { return true }
func (f fanout) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range f.handlers {
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			return err
		}
	}
	return nil
}
func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := fanout{}
	for _, handler := range f.handlers {
		out.handlers = append(out.handlers, handler.WithAttrs(attrs))
	}
	return out
}
func (f fanout) WithGroup(name string) slog.Handler {
	out := fanout{}
	for _, handler := range f.handlers {
		out.handlers = append(out.handlers, handler.WithGroup(name))
	}
	return out
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
	write(t, filepath.Join(pipe, "src", "handler.go"), "package src // the user's code\n")
	write(t, filepath.Join(pipe, "generated", "main.go"), "package main\n")
	guard, err := projectfs.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	logs := &logBuffer{}
	options := &slog.HandlerOptions{Level: slog.LevelDebug}
	logger := slog.New(fanout{handlers: []slog.Handler{
		slog.NewTextHandler(logs, options), slog.NewJSONHandler(jsonSink{logs}, options),
	}})
	return &project{t: t, root: root, pipe: pipe, guard: guard, files: New(guard, logger), logs: logs}
}

func (p *project) path(name string) string { return filepath.Join(p.pipe, name) }

func (p *project) env(t *testing.T) string       { return read(t, p.path(EnvFile)) }
func (p *project) gitignore(t *testing.T) string { return read(t, p.path(GitignoreFile)) }

// ceiling is above every repository a test makes, so git never finds one
// outside the scratch directory.
func (p *project) ceiling() string { return filepath.Dir(p.root) }

func (p *project) write(t *testing.T, values map[string]string) (Report, error) {
	t.Helper()
	return p.files.WriteEnv(context.Background(), p.pipe, secretsOf(values))
}

func secretsOf(values map[string]string) map[string]Secret {
	out := make(map[string]Secret, len(values))
	for key, value := range values {
		out[key] = NewSecret(value)
	}
	return out
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

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// snapshot is every entry under directory except the names given, with its
// mode and, for a file or link, its content or target.
func snapshot(t *testing.T, directory string, except ...string) map[string]string {
	t.Helper()
	skip := map[string]bool{}
	for _, name := range except {
		skip[name] = true
	}
	out := map[string]string{}
	err := filepath.WalkDir(directory, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(directory, path)
		if skip[relative] {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			value += " -> " + target
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(path)
			value += " " + string(data)
		}
		out[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, before, after map[string]string) {
	t.Helper()
	var names []string
	for name := range before {
		names = append(names, name)
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if before[name] != after[name] {
			t.Errorf("%s changed: %q -> %q", name, before[name], after[name])
		}
	}
}

// refusedWith asserts err is an *Error with exactly these kinds, in order,
// and returns its problems.
func refusedWith(t *testing.T, err error, kinds ...Kind) []Problem {
	t.Helper()
	refusal, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error with %v, got %T %v", kinds, err, err)
	}
	got := refusal.Kinds()
	if strings.Join(kindStrings(got), ",") != strings.Join(kindStrings(kinds), ",") {
		t.Fatalf("want kinds %v, got %v (%v)", kinds, got, err)
	}
	return refusal.Problems
}

func kindStrings(kinds []Kind) []string {
	out := make([]string, len(kinds))
	for i, kind := range kinds {
		out[i] = string(kind)
	}
	return out
}

func hasKind(problems []Problem, kind Kind) *Problem {
	for i := range problems {
		if problems[i].Kind == kind {
			return &problems[i]
		}
	}
	return nil
}

// git runs git in directory with no user or system configuration, so the
// developer's own excludes file cannot change the answer. It reports whether
// the command succeeded.
func git(t *testing.T, ceiling, directory string, args ...string) (string, bool) {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed, and this test checks agreement with it")
	}
	home := t.TempDir()
	command := exec.Command(path, args...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES="+ceiling,
	)
	out, err := command.CombinedOutput()
	if err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			t.Fatalf("git %v: %v", args, err)
		}
		return string(out), false
	}
	return string(out), true
}

func gitInit(t *testing.T, ceiling, directory string) {
	t.Helper()
	if out, ok := git(t, ceiling, directory, "init", "-q", "."); !ok {
		t.Fatalf("git init: %s", out)
	}
}

// gitIgnores is git's answer for name, asked in directory.
func gitIgnores(t *testing.T, ceiling, directory, name string) bool {
	t.Helper()
	out, ok := git(t, ceiling, directory, "check-ignore", "-q", name)
	if !ok && strings.TrimSpace(out) != "" {
		t.Fatalf("git check-ignore %s: %s", name, out)
	}
	return ok
}
