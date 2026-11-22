// Structural tests: claims about the whole agent module that no behaviour can
// show.
//
// The first is #6's constraint in as many words -- no path from an API
// parameter is interpolated into a shell command anywhere. Until #37 that was
// kept by the agent having no way to run a command at all. It now has exactly
// one: internal/process may start a process, and only a Program from its fixed
// table (internal/process/programs.go), whose executable, arguments and
// environment names are written there, with no shell and nothing taken from a
// request but stdin bytes. Every other package keeps every ban: no os/exec, no
// syscall, no golang.org/x/sys, no process-spawning call, and no string
// literal naming a shell or its -c flag. Inside internal/process the bans that
// would reach around the table stay too, and so does the literal ban. Which
// packages may import internal/process is a list here; httpapi is never on it.
//
// A later issue that needs a program adds its entry to programs.go, which
// TestEveryProgramInTheTableIsWellFormed checks, and the package that runs it
// to processImporters below, with the reason. Nothing else changes here.
//
// The second is the constraint that makes this package worth having: all
// filesystem code routes through it rather than calling os directly. That is
// checked where it is visible -- which files in the module contain a
// filesystem call -- so a new route that reads a path fails here rather than
// shipping unguarded. There is no other place: internal/pipeline's store
// moved onto the guard in internal/catalog (#48), and httpapi and channel
// carry their own bans on reaching the filesystem at all.
//
// Both tests read the syntax tree of every non-test file in the module. Test
// files are excluded on purpose: a test may run a command (one of #3's does,
// to prove its output is byte-identical across processes, and
// internal/process's fakes are the test binary run again) and may touch the
// filesystem freely, and neither is ever handed an API parameter.

package projectfs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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
	for _, expected := range []string{"cmd/agent", "internal/catalog", "internal/channel", "internal/httpapi", "internal/pipeline", "internal/projectfs", "internal/version"} {
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

// The one package that may start a process, and why. It is a single entry on
// purpose: a second package that wants to start one goes through this one.
var processAllowance = map[string]string{
	"internal/process": "runs only a Program from its fixed table, with no shell, no argument from a request, and a kill of the whole process group after every run",
}

// What the allowance lets internal/process do that no other package may. A
// name is the package path and the selector, resolved through the file's
// imports, so it is the same however the code is spelled.
var (
	processAllowedImports = map[string]bool{"os/exec": true, "syscall": true}
	// Every os/exec name internal/process may refer to. exec.Cmd only as
	// *exec.Cmd, the type exec.CommandContext answers: a Cmd literal, a
	// new(exec.Cmd) or a Cmd variable starts a process without the one call
	// site.
	processExecNames = map[string]bool{"CommandContext": true, "LookPath": true, "Cmd": true, "ErrWaitDelay": true}
	// The only syscall names internal/process may use: the process group, its
	// kill, and reading how the child ended.
	processSyscallNames = map[string]bool{"SysProcAttr": true, "Kill": true, "SIGKILL": true, "Signal": true, "WaitStatus": true}
)

// The packages whose non-test files may import internal/process, and why.
// #51 and #36 add theirs here rather than making another exception.
var processImporters = map[string]string{
	"internal/parser": "runs a parser sidecar (#37, #38, #39)",
}

const processPackage = "github.com/zssvaidar/kpv/agent/internal/process"

// guardedImports are the standard packages both tests below match names
// from. Each must be imported under its own name, never renamed, dot- or
// blank-imported, so that a name written os.StartProcess or filepath.Abs is
// the only way to write it. The value is the name.
var guardedImports = map[string]string{
	"os": "os", "os/exec": "exec", "syscall": "syscall",
	"path/filepath": "filepath", "io/ioutil": "ioutil", "io/fs": "fs",
}

// bannedReferences are names no package may refer to at all, called or not:
// a function value starts a process as well as a call does.
var bannedReferences = map[string]string{
	"os.StartProcess":       "starts a process",
	"os.ProcAttr":           "describes a process to start",
	"exec.Command":          "runs a command without a context",
	"syscall.Exec":          "replaces this process with a command",
	"syscall.ForkExec":      "forks and runs a command",
	"syscall.StartProcess":  "starts a process",
	"syscall.CreateProcess": "starts a process",
}

// bannedLiterals are the shell names and the flags that hand a shell a
// string to interpret. Either one appearing, as a literal or a constant
// expression of literals, is the thing this test is about, in every package,
// internal/process included. internal/process/programs_test.go reads this
// list from here.
var bannedLiterals = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "cmd": true,
	"cmd.exe": true, "powershell": true, "powershell.exe": true, "pwsh": true,
	"/bin/sh": true, "/bin/bash": true, "/usr/bin/sh": true, "/usr/bin/bash": true,
	"-c": true, "/c": true, "-Command": true,
}

// resolver names what a selector refers to through one file's imports.
type resolver map[string]string

func resolverOf(file *ast.File) resolver {
	out := resolver{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		out[name] = path
	}
	return out
}

// name answers a selector on a guarded package as "<own name>.<selector>",
// and "" for anything else.
func (r resolver) name(selector *ast.SelectorExpr) string {
	ident, ok := selector.X.(*ast.Ident)
	if !ok || ident.Obj != nil {
		// A selector on a local variable that happens to be called exec is
		// not the package.
		return ""
	}
	own, guarded := guardedImports[r[ident.Name]]
	if !guarded {
		return ""
	}
	return own + "." + selector.Sel.Name
}

// importNameViolations refuses a guarded package imported under another name.
func importNameViolations(file moduleFile) []string {
	var out []string
	for _, spec := range file.file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		own, guarded := guardedImports[path]
		if guarded && spec.Name != nil && spec.Name.Name != own {
			out = append(out, fmt.Sprintf("%s imports %s as %s; it is imported under its own name only, so the bans on its names cannot be spelled around", file.path, path, spec.Name.Name))
		}
	}
	return out
}

// constantStrings folds every package-level and local string constant whose
// value is literals and other such constants, so `const a = "s"; a + "h"` is
// seen as "sh".
func constantStrings(files []moduleFile) map[string]string {
	known := map[string]string{}
	for range 4 {
		for _, file := range files {
			ast.Inspect(file.file, func(node ast.Node) bool {
				declaration, ok := node.(*ast.GenDecl)
				if !ok || declaration.Tok != token.CONST {
					return true
				}
				for _, spec := range declaration.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range value.Names {
						if index < len(value.Values) {
							if text, ok := foldString(value.Values[index], known); ok {
								known[name.Name] = text
							}
						}
					}
				}
				return true
			})
		}
	}
	return known
}

// foldString evaluates a string constant expression of literals, parentheses,
// + and known constants.
func foldString(expression ast.Expr, known map[string]string) (string, bool) {
	switch typed := expression.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(typed.Value)
		return text, err == nil
	case *ast.ParenExpr:
		return foldString(typed.X, known)
	case *ast.Ident:
		text, ok := known[typed.Name]
		return text, ok
	case *ast.BinaryExpr:
		if typed.Op != token.ADD {
			return "", false
		}
		left, ok := foldString(typed.X, known)
		if !ok {
			return "", false
		}
		right, ok := foldString(typed.Y, known)
		return left + right, ok
	}
	return "", false
}

// commandScan is what scanForCommands found: every violation, and what the
// module-wide checks after it need.
type commandScan struct {
	violations          []string
	processImportsExec  bool
	commandContextRefs  int
	forTestDeclaredIn   string
	forTestReferencedIn []string
}

// scanForCommands applies every rule of TestNothingInTheAgentCanRunACommand
// to files. It is a function of the files alone so planted violations can be
// fed to it (TestTheCommandScanCatchesEveryPlantedViolation).
func scanForCommands(files []moduleFile) commandScan {
	var scan commandScan
	byPackage := map[string][]moduleFile{}
	for _, file := range files {
		byPackage[file.pkg] = append(byPackage[file.pkg], file)
	}
	constants := map[string]map[string]string{}
	for pkg, members := range byPackage {
		constants[pkg] = constantStrings(members)
	}
	report := func(format string, args ...any) {
		scan.violations = append(scan.violations, fmt.Sprintf(format, args...))
	}

	for _, file := range files {
		_, allowed := processAllowance[file.pkg]
		scan.violations = append(scan.violations, importNameViolations(file)...)
		for _, spec := range file.file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			switch {
			case strings.HasPrefix(path, "golang.org/x/sys"):
				report("%s imports %s, which reaches exec and fork directly", file.path, path)
			case (path == "os/exec" || path == "syscall") && (!allowed || !processAllowedImports[path]):
				report("%s imports %s; only internal/process may start a process, so no path from an API parameter can reach a command", file.path, path)
			case path == processPackage:
				if _, may := processImporters[file.pkg]; !may {
					report("%s imports internal/process, and %s is not on the list of packages that may", file.path, file.pkg)
				}
			}
			if allowed && path == "os/exec" {
				scan.processImportsExec = true
			}
		}

		names := resolverOf(file.file)
		var parents []ast.Node
		ast.Inspect(file.file, func(node ast.Node) bool {
			if node == nil {
				parents = parents[:len(parents)-1]
				return true
			}
			var parent ast.Node
			if len(parents) > 0 {
				parent = parents[len(parents)-1]
			}
			parents = append(parents, node)

			switch typed := node.(type) {
			case *ast.FuncDecl:
				if typed.Recv == nil && typed.Name.Name == "ForTest" && allowed {
					scan.forTestDeclaredIn = file.path
				}
			case *ast.Ident:
				if typed.Name == "ForTest" {
					scan.forTestReferencedIn = append(scan.forTestReferencedIn, file.path)
				}
			case *ast.SelectorExpr:
				name := names.name(typed)
				if name == "" {
					return true
				}
				if why, banned := bannedReferences[name]; banned {
					report("%s refers to %s, which %s", file.path, name, why)
				}
				pkg, selector, _ := strings.Cut(name, ".")
				switch {
				case pkg == "exec" && allowed:
					if !processExecNames[selector] {
						report("%s uses exec.%s, which is not on the list internal/process may use", file.path, selector)
					}
					if selector == "CommandContext" {
						scan.commandContextRefs++
					}
					if _, pointer := parent.(*ast.StarExpr); selector == "Cmd" && !pointer {
						report("%s uses exec.Cmd other than as *exec.Cmd; a Cmd literal, new(exec.Cmd) or a Cmd variable starts a process without exec.CommandContext", file.path)
					}
				case pkg == "syscall" && allowed:
					if !processSyscallNames[selector] {
						report("%s uses syscall.%s, which is not on the list internal/process may use", file.path, selector)
					}
				}
			case *ast.BasicLit, *ast.BinaryExpr:
				expression := typed.(ast.Expr)
				if _, inner := parent.(*ast.BinaryExpr); inner {
					// Folded as part of the expression it is in.
					if _, isLiteral := typed.(*ast.BasicLit); !isLiteral {
						return true
					}
				}
				if text, ok := foldString(expression, constants[file.pkg]); ok && bannedLiterals[text] {
					report("%s has %s in it, which is %q: a shell or the flag that hands one a string to interpret", file.path, renderedExpression(file, expression), text)
				}
			}
			return true
		})
	}
	return scan
}

func renderedExpression(file moduleFile, node ast.Node) string {
	var out strings.Builder
	if err := printer.Fprint(&out, file.set, node); err != nil {
		return "an expression"
	}
	return out.String()
}

// No path from an API parameter is interpolated into a shell command: only
// internal/process can start a process, only a fixed-table program, and never
// a shell.
func TestNothingInTheAgentCanRunACommand(t *testing.T) {
	t.Parallel()
	if len(processAllowance) != 1 {
		t.Fatalf("processAllowance has %d entries; exactly one package may start a process", len(processAllowance))
	}
	scan := scanForCommands(parseModule(t))
	for _, violation := range scan.violations {
		t.Error(violation)
	}
	// The allowance cannot go stale: if internal/process stops importing
	// os/exec, the entry above is allowing nothing and has to go.
	if !scan.processImportsExec {
		t.Error("internal/process no longer imports os/exec, so its allowance here is stale")
	}
	if scan.commandContextRefs != 1 {
		t.Errorf("internal/process refers to exec.CommandContext in %d places, want exactly one", scan.commandContextRefs)
	}
	// ForTest builds an arbitrary Program. Only the file declaring it may
	// name it; any other non-test file that does is shipping a way around
	// the table.
	if scan.forTestDeclaredIn == "" {
		t.Fatal("no file in internal/process declares ForTest, so this test is not checking who names it")
	}
	for _, path := range scan.forTestReferencedIn {
		if path != scan.forTestDeclaredIn {
			t.Errorf("%s refers to ForTest, which only tests may use", path)
		}
	}
}

// Each way around the rules above that review found, planted in a file of
// its own and fed to the same scan, so a regression in the matcher fails
// here rather than letting the next one through.
func TestTheCommandScanCatchesEveryPlantedViolation(t *testing.T) {
	t.Parallel()
	plants := []struct {
		name, pkg, source, want string
	}{
		{"exec imported elsewhere", "internal/version", `import "os/exec"; var _ = exec.LookPath`, "imports os/exec"},
		{"syscall imported elsewhere", "internal/version", `import "syscall"; var _ = syscall.Getpid`, "imports syscall"},
		{"x/sys anywhere", "internal/process", `import _ "golang.org/x/sys/unix"`, "golang.org/x/sys/unix"},
		{"x/sys aliased", "internal/process", `import u "golang.org/x/sys/unix"; var _ = u.Exec`, "golang.org/x/sys/unix"},
		{"renamed syscall", "internal/process", `import sc "syscall"; var _ = sc.ForkExec`, "imports syscall as sc"},
		{"renamed syscall reaches ForkExec", "internal/process", `import sc "syscall"; func f() { sc.ForkExec("x", nil, nil) }`, "syscall.ForkExec"},
		{"renamed syscall name off the list", "internal/process", `import sc "syscall"; var _ = sc.Getpid`, "syscall.Getpid"},
		{"renamed exec", "internal/process", `import ex "os/exec"; var _ = ex.Command("x")`, "exec.Command,"},
		{"dot exec", "internal/process", `import . "os/exec"; var _ = Command`, "imports os/exec as ."},
		{"blank os", "internal/version", `import _ "os"`, "imports os as _"},
		{"renamed os in process", "internal/process", `import o "os"; func f() { o.StartProcess("x", nil, nil) }`, "os.StartProcess"},
		{"renamed os elsewhere", "internal/version", `import o "os"; func f() { o.StartProcess("x", nil, nil) }`, "os.StartProcess"},
		{"StartProcess as a value", "internal/version", `import "os"; var f = os.StartProcess`, "os.StartProcess"},
		{"Command as a value", "internal/process", `import "os/exec"; var f = exec.Command`, "exec.Command,"},
		{"Cmd literal", "internal/process", `import "os/exec"; func f() { _ = (&exec.Cmd{Path: "/x"}).Start() }`, "exec.Cmd other than"},
		{"new Cmd", "internal/process", `import "os/exec"; func f() { _ = new(exec.Cmd).Start() }`, "exec.Cmd other than"},
		{"Cmd variable", "internal/process", `import "os/exec"; func f() { var c exec.Cmd; c.Path = "/x"; _ = c.Start() }`, "exec.Cmd other than"},
		{"exec name off the list", "internal/process", `import "os/exec"; var _ = exec.ErrNotFound`, "exec.ErrNotFound"},
		{"escaped -c", "internal/process", `var _ = "\x2dc"`, `"-c"`},
		{"raw sh", "internal/version", "var _ = `sh`", `"sh"`},
		{"concatenated sh", "internal/process", `var _ = "s" + "h"`, `"sh"`},
		{"concatenated through a constant", "internal/process", `const half = "/bin/"; var _ = half + ("ba" + "sh")`, `"/bin/bash"`},
		{"new importer", "internal/catalog", `import "github.com/zssvaidar/kpv/agent/internal/process"; var _ process.Kind`, "not on the list"},
		{"renamed filepath", "internal/version", `import fp "path/filepath"; var _ = fp.Abs`, "imports path/filepath as fp"},
	}
	for _, plant := range plants {
		t.Run(plant.name, func(t *testing.T) {
			t.Parallel()
			set := token.NewFileSet()
			parsed, err := parser.ParseFile(set, "plant.go", "package plant\n"+plant.source+"\n", parser.ParseComments)
			if err != nil {
				t.Fatalf("parse the plant: %v", err)
			}
			scan := scanForCommands([]moduleFile{{path: plant.pkg + "/plant.go", pkg: plant.pkg, file: parsed, set: set}})
			for _, violation := range scan.violations {
				if strings.Contains(violation, plant.want) {
					return
				}
			}
			t.Fatalf("the scan missed it; it found %q, want one mentioning %q", scan.violations, plant.want)
		})
	}

	// The counts the module-wide test checks, through an alias as well.
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, "plant.go", `package plant
import ex "os/exec"
import "context"
var _ = ex.CommandContext(context.Background(), "x")
var _ = ex.CommandContext
func ForTest() {}
var _ = ForTest
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	scan := scanForCommands([]moduleFile{{path: "internal/process/plant.go", pkg: "internal/process", file: parsed, set: set}})
	if scan.commandContextRefs != 2 || !scan.processImportsExec {
		t.Fatalf("an aliased exec.CommandContext counted %d times, import seen %v", scan.commandContextRefs, scan.processImportsExec)
	}
	other := token.NewFileSet()
	elsewhere, err := parser.ParseFile(other, "plant.go", "package plant\nimport p \"github.com/zssvaidar/kpv/agent/internal/process\"\nvar _ = p.ForTest\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	scan = scanForCommands([]moduleFile{{path: "internal/parser/plant.go", pkg: "internal/parser", file: elsewhere, set: other}})
	if len(scan.forTestReferencedIn) != 1 {
		t.Fatalf("an aliased ForTest reference was not seen: %v", scan.forTestReferencedIn)
	}
}

// Every filesystem call in the module is in the one package allowed to make
// one. This is what "all later filesystem code routes through this
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
	}

	files := parseModule(t)
	found := map[string]int{}
	names := map[string]bool{}
	for _, file := range files {
		// os, path/filepath and io/ioutil are imported under their own names
		// only (TestNothingInTheAgentCanRunACommand refuses anything else),
		// and a name is resolved through the file's imports, so `o "os"`
		// cannot spell around this. A reference counts, called or not: a
		// function value reaches the filesystem as well as a call does.
		for _, violation := range importNameViolations(file) {
			t.Error(violation)
		}
		imports := resolverOf(file.file)
		ast.Inspect(file.file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			written := imports.name(selector)
			if !filesystemCalls[written] {
				return true
			}
			found[file.pkg]++
			names[written] = true
			if _, mayReachIt := allowed[file.pkg]; !mayReachIt {
				t.Errorf("%s refers to %s; every filesystem call goes through internal/projectfs, because that is where a path is checked against the authorized roots", file.path, written)
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
	// Most of this package's filesystem work is not an os call at all: it
	// goes through the *os.Root of the path's own root. os.OpenRoot is where
	// that descriptor comes from, so it is the one name whose absence would
	// mean either the design changed or the matcher stopped matching.
	if !names["os.OpenRoot"] {
		t.Fatalf("os.OpenRoot was not recognised anywhere, so either the roots are no longer held open or this test is matching nothing; recognised %v", names)
	}
	if !names["os.Stat"] {
		t.Fatalf("os.Stat was not recognised anywhere; this test is matching nothing")
	}
	t.Logf("filesystem calls by package: %v", found)
}
