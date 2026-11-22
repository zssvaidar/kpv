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

// A Program literal is written in programs.go only: the table's entries go
// through newProgram there, and ForTest calls the same function. Any other
// file that writes Program{...} is building a program outside the table.
func TestOnlyProgramsGoBuildsAProgram(t *testing.T) {
	t.Parallel()
	built := 0
	for name, file := range parseThisPackage(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if ident, ok := literal.Type.(*ast.Ident); ok && ident.Name == "Program" {
				if name != "programs.go" {
					t.Errorf("%s builds a Program; every Program is built in programs.go", name)
				}
				built++
			}
			return true
		})
	}
	if built == 0 {
		t.Fatal("no Program literal anywhere, so this test is matching nothing")
	}
}
