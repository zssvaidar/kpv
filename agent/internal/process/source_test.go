// Structural tests: facts about this package's own source that a behavioural
// test cannot reach. That a Program can only come from the table is a claim
// about where Program values are built, so it is checked where that is
// visible.

package process

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func parseThisPackage(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read this package's directory: %v", err)
	}
	set := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = parsed
	}
	for _, expected := range []string{"process.go", "programs.go", "fortest.go", "group_unix.go", "group_other.go"} {
		if files[expected] == nil {
			t.Fatalf("%s was not parsed, so this test is looking at the wrong thing (%d files)", expected, len(files))
		}
	}
	return files
}

// This package is the bottom of the agent: it imports no other agent package,
// so nothing a request carries can reach it except through its callers'
// stdin bytes.
func TestThisPackageImportsNoOtherAgentPackage(t *testing.T) {
	t.Parallel()
	for name, file := range parseThisPackage(t) {
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.HasPrefix(path, "github.com/zssvaidar/kpv/") {
				t.Errorf("%s imports %s; internal/process imports no other agent package", name, path)
			}
		}
	}
}

// programFields are Program's fields. Assigning one outside programs.go
// builds a Program as surely as a literal does.
var programFields = map[string]bool{"executable": true, "args": true, "env": true, "limits": true}

// programBuilds is every place file builds or fills in a Program: a Program
// literal, new(Program), a variable declared as a Program, or an assignment
// to one of its fields. Only programs.go may hold any.
func programBuilds(file *ast.File) []string {
	var out []string
	isProgram := func(expression ast.Expr) bool {
		ident, ok := expression.(*ast.Ident)
		return ok && ident.Name == "Program"
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CompositeLit:
			if isProgram(typed.Type) {
				out = append(out, "a Program literal")
			}
		case *ast.CallExpr:
			if ident, ok := typed.Fun.(*ast.Ident); ok && ident.Name == "new" && len(typed.Args) == 1 && isProgram(typed.Args[0]) {
				out = append(out, "new(Program)")
			}
		case *ast.ValueSpec:
			if typed.Type != nil && isProgram(typed.Type) {
				out = append(out, "a variable declared as a Program")
			}
		case *ast.AssignStmt:
			for _, target := range typed.Lhs {
				if selector, ok := target.(*ast.SelectorExpr); ok && programFields[selector.Sel.Name] {
					out = append(out, "an assignment to ."+selector.Sel.Name)
				}
			}
		}
		return true
	})
	return out
}

// A Program is built in programs.go only: the table's entries go through
// newProgram there, and ForTest calls the same function. Any other file that
// builds or fills in a Program is making one outside the table.
func TestOnlyProgramsGoBuildsAProgram(t *testing.T) {
	t.Parallel()
	built := 0
	for name, file := range parseThisPackage(t) {
		builds := programBuilds(file)
		if name == "programs.go" {
			built += len(builds)
			continue
		}
		for _, build := range builds {
			t.Errorf("%s has %s; every Program is built in programs.go", name, build)
		}
	}
	if built == 0 {
		t.Fatal("no Program is built in programs.go, so this test is matching nothing")
	}
}

// Each way of building a Program the check above must see, planted.
func TestTheProgramCheckCatchesEveryPlantedBuild(t *testing.T) {
	t.Parallel()
	for _, plant := range []string{
		`var _ = Program{executable: "x"}`,
		`var _ = &Program{}`,
		`func f() { p := new(Program); p.executable = "x" }`,
		`func f() Program { var p Program; return p }`,
		`func f(p *Program) { p.limits = Limits{Timeout: 1, MaxStdout: 1} }`,
		`func f(p *Program) { p.args, p.env = nil, nil }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "plant.go", "package process\n"+plant+"\n", 0)
		if err != nil {
			t.Fatalf("parse %q: %v", plant, err)
		}
		if len(programBuilds(file)) == 0 {
			t.Errorf("the check misses %q", plant)
		}
	}
	// Taking a Program as a parameter, or copying one, builds nothing.
	file, err := parser.ParseFile(token.NewFileSet(), "plant.go", "package process\nfunc f(p Program) Program { q := p; return q }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	if builds := programBuilds(file); len(builds) != 0 {
		t.Errorf("a parameter is taken for a build: %v", builds)
	}
}
