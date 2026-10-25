// Structural tests: two claims about the whole agent module that no behaviour
// can show.
//
// The first is #6's constraint in as many words -- no path from an API
// parameter is interpolated into a shell command anywhere. The strongest form
// of that is not an assertion about one handler, it is that the agent has no
// way to run a command at all: nothing imports os/exec, nothing reaches the
// process-spawning calls, and nothing names a shell. A path cannot be
// interpolated into a command that does not exist.
//
// The second is the constraint that makes this package worth having: all
// filesystem code routes through it rather than calling os directly. That is
// checked where it is visible -- which files in the module contain a
// filesystem call -- so a new route that reads a path fails here rather than
// shipping unguarded. internal/pipeline is the one other place, because its
// store predates this package and #48 moves it over; httpapi and channel carry
// their own bans on reaching either.
//
// Both tests read the syntax tree of every non-test file in the module. Test
// files are excluded on purpose: a test may run a command (one of #3's does,
// to prove its output is byte-identical across processes) and may touch the
// filesystem freely, and neither is ever handed an API parameter.

package projectfs

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleFile is one parsed non-test source file of the agent module.
type moduleFile struct {
	// path is relative to the module root, with forward slashes.
	path string
	// pkg is the directory the file is in, relative to the module root.
	pkg  string
	file *ast.File
	set  *token.FileSet
}

// moduleRoot walks up from this package's directory to the go.mod the module
// is defined by, so the tests below cover the whole module and not just the
// package they live in.
func moduleRoot(t *testing.T) string {
	t.Helper()
	directory, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("find this package's directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("no go.mod above this package, so these tests cannot find the module")
		}
		directory = parent
	}
}

// parseModule parses every non-test Go file in the agent module.
func parseModule(t *testing.T) []moduleFile {
	t.Helper()
	root := moduleRoot(t)
	var out []moduleFile
	set := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(set, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		out = append(out, moduleFile{path: relative, pkg: filepath.ToSlash(filepath.Dir(relative)), file: parsed, set: set})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}

	// Non-vacuity: these tests are absences, so a walk that found nothing
	// would pass them both.
	packages := map[string]bool{}
	for _, file := range out {
		packages[file.pkg] = true
	}
	if len(out) < 20 || len(packages) < 6 {
		t.Fatalf("parsed %d files in %d packages of the module, which is too few for these tests to be looking at the right thing", len(out), len(packages))
	}
	for _, expected := range []string{"cmd/agent", "internal/channel", "internal/httpapi", "internal/pipeline", "internal/projectfs", "internal/version"} {
		if !packages[expected] {
			t.Fatalf("%s was not parsed, so these tests are not covering the whole module", expected)
		}
	}
	return out
}

func importsOfFile(t *testing.T, file moduleFile) []string {
	t.Helper()
	out := make([]string, 0, len(file.file.Imports))
	for _, spec := range file.file.Imports {
		out = append(out, strings.Trim(spec.Path.Value, `"`))
	}
	return out
}

// rendered prints an expression back as source, which is how a call is
// identified by what it is written as rather than by resolving it.
func rendered(t *testing.T, file moduleFile, node ast.Node) string {
	t.Helper()
	var out strings.Builder
	if err := printer.Fprint(&out, file.set, node); err != nil {
		t.Fatalf("print a node in %s: %v", file.path, err)
	}
	return out.String()
}

// No path from an API parameter is interpolated into a shell command, because
// the agent cannot run a command at all.
func TestNothingInTheAgentCanRunACommand(t *testing.T) {
	t.Parallel()

	bannedImports := map[string]string{
		"os/exec":                  "runs commands",
		"syscall":                  "reaches exec and fork directly",
		"golang.org/x/sys/unix":    "reaches exec and fork directly",
		"golang.org/x/sys/windows": "reaches exec and fork directly",
		"golang.org/x/sys/execabs": "runs commands",
	}

	bannedCalls := map[string]string{
		"exec.Command":          "runs a command",
		"exec.CommandContext":   "runs a command",
		"exec.LookPath":         "looks a command up to run it",
		"os.StartProcess":       "starts a process",
		"syscall.Exec":          "replaces this process with a command",
		"syscall.ForkExec":      "forks and runs a command",
		"syscall.StartProcess":  "starts a process",
		"syscall.CreateProcess": "starts a process",
	}
	// A literal that would be the shell, or the flag that hands it a string
	// to interpret. Either one appearing is the thing this test is about.
	bannedLiterals := map[string]bool{
		"sh": true, "bash": true, "zsh": true, "dash": true, "cmd": true,
		"cmd.exe": true, "powershell": true, "powershell.exe": true, "pwsh": true,
		"/bin/sh": true, "/bin/bash": true, "/usr/bin/sh": true, "/usr/bin/bash": true,
		"-c": true, "/c": true, "-Command": true,
	}

	files := parseModule(t)
	for _, file := range files {
		for _, path := range importsOfFile(t, file) {
			if why, banned := bannedImports[path]; banned {
				t.Errorf("%s imports %s, which %s; no path from an API parameter may reach a command, and the way to keep that true is to have no way to run one", file.path, path, why)
			}
		}
		ast.Inspect(file.file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				written := rendered(t, file, typed.Fun)
				if why, banned := bannedCalls[written]; banned {
					t.Errorf("%s calls %s, which %s", file.path, written, why)
				}
			case *ast.BasicLit:
				if typed.Kind != token.STRING {
					return true
				}
				text := strings.Trim(typed.Value, "\"`")
				if bannedLiterals[text] {
					t.Errorf("%s has the literal %s in it, which names a shell or the flag that hands one a string to interpret", file.path, typed.Value)
				}
			}
			return true
		})
	}
}

// Every filesystem call in the module is in one of the two packages allowed to
// make one. This is what "all later filesystem code routes through this
// package rather than calling os directly" means in a form that fails when it
// stops being true.
func TestTheFilesystemIsOnlyReachedFromThePackagesAllowedToReachIt(t *testing.T) {
	t.Parallel()

	// os functions that touch the filesystem. os.Stdout, os.Exit, os.Args and
	// the rest are not filesystem calls and are not listed.
	filesystemCalls := map[string]bool{}
	for _, name := range []string{
		"os.Open", "os.OpenFile", "os.Create", "os.CreateTemp", "os.MkdirTemp",
		"os.ReadFile", "os.WriteFile", "os.ReadDir", "os.Stat", "os.Lstat",
		"os.Readlink", "os.Symlink", "os.Link", "os.Remove", "os.RemoveAll",
		"os.Rename", "os.Mkdir", "os.MkdirAll", "os.Truncate", "os.Chmod",
		"os.Chown", "os.Lchown", "os.Chtimes", "os.Chdir", "os.Getwd",
		"os.OpenRoot", "os.DirFS", "os.CopyFS", "os.NewFile",
		"filepath.Walk", "filepath.WalkDir", "filepath.Glob",
		"filepath.EvalSymlinks", "filepath.Abs",
		"ioutil.ReadFile", "ioutil.WriteFile", "ioutil.ReadDir", "ioutil.TempFile",
	} {
		filesystemCalls[name] = true
	}
	allowed := map[string]string{
		"internal/projectfs": "is the package every filesystem call is supposed to go through",
		"internal/pipeline":  "predates this package; #48 moves its store onto the guard",
	}

	files := parseModule(t)
	found := map[string]int{}
	names := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			written := rendered(t, file, call.Fun)
			if !filesystemCalls[written] {
				return true
			}
			found[file.pkg]++
			names[written] = true
			if _, mayReachIt := allowed[file.pkg]; !mayReachIt {
				t.Errorf("%s calls %s; every filesystem call goes through internal/projectfs, because that is where a path is checked against the authorized roots", file.path, written)
			}
			return true
		})
		// io/ioutil is a filesystem package whole, and nothing should be
		// importing it anywhere.
		for _, path := range importsOfFile(t, file) {
			if path == "io/ioutil" {
				t.Errorf("%s imports io/ioutil", file.path)
			}
		}
	}

	// Non-vacuity, both ways: the scan has to be finding calls at all, and it
	// has to be finding them in this package, or the list of names above has
	// gone stale and the test is passing because it recognises nothing.
	total := 0
	for _, count := range found {
		total += count
	}
	if total < 10 {
		t.Fatalf("only %d filesystem calls were recognised in the whole module (%v); the names this test looks for have gone stale", total, found)
	}
	if found["internal/projectfs"] < 4 {
		t.Fatalf("only %d filesystem calls were recognised in internal/projectfs; the names have gone stale", found["internal/projectfs"])
	}
	if found["internal/pipeline"] < 3 {
		t.Fatalf("only %d filesystem calls were recognised in internal/pipeline, which has several; the names have gone stale", found["internal/pipeline"])
	}
	// Most of this package's filesystem work is not an os call at all: it
	// goes through the *os.Root of the path's own root. os.OpenRoot is where
	// that descriptor comes from, so it is the one name whose absence would
	// mean either the design changed or the matcher stopped matching.
	if !names["os.OpenRoot"] {
		t.Fatalf("os.OpenRoot was not recognised anywhere, so either the roots are no longer held open or this test is matching nothing; recognised %v", names)
	}
	if !names["os.ReadFile"] {
		t.Fatalf("os.ReadFile was not recognised anywhere; this test is matching nothing")
	}
	t.Logf("filesystem calls by package: %v", found)
}
