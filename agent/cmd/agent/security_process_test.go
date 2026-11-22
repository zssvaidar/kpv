//go:build unix

// No request reaches a process (#37). The agent's PATH is a scratch
// directory of stand-ins named after the shells, env, git and docker and
// every executable in internal/process/programs.go; each records its argv,
// environment and stdin to a marker file. Every string input of every route
// gets the injection values, and afterwards no marker carries request input,
// and, with the program table empty, none exists at all. The static proof
// that only internal/process can start a process is #37's
// (internal/projectfs/boundary_test.go, internal/process/*_test.go); this
// is the run-time half.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// programsFile is the fixed program table.
const programsFile = "../../internal/process/programs.go"

// standInNames are the executables a request could hope to reach.
var standInNames = []string{"sh", "bash", "dash", "zsh", "env", "git", "docker"}

// programExecutables reads the executable of every entry of the program
// table from source, the way internal/process/programs_test.go reads its
// lists: the first argument of each newProgram call in programs.go's
// programs map, a string literal by the table's own rule.
func programExecutables(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), programsFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", programsFile, err)
	}
	var names []string
	foundTable := false
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "programs" {
			return true
		}
		foundTable = true
		ast.Inspect(spec, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); !ok || ident.Name != "newProgram" || len(call.Args) == 0 {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			name, err := strconv.Unquote(literalValue(literal, ok))
			if !ok || err != nil || name == "" {
				t.Errorf("a program table entry's executable is not a string literal the suite can read")
				return true
			}
			names = append(names, name)
			return true
		})
		return false
	})
	if !foundTable {
		t.Fatalf("%s has no programs table, so the suite cannot put its executables on PATH", programsFile)
	}
	return names
}

func literalValue(literal *ast.BasicLit, ok bool) string {
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	return literal.Value
}

// writeStandIns puts a recording stand-in for each name on the scratch
// PATH. A stand-in is a script run by /bin/sh by its absolute path, so the
// scratch sh is never what runs it; it writes its argv, environment and
// stdin to markers/<name>.<pid>.
func writeStandIns(t *testing.T, s *scratch) {
	t.Helper()
	names := append(append([]string(nil), standInNames...), programExecutables(t)...)
	for _, name := range names {
		marker := filepath.Join(s.markers, name)
		script := "#!/bin/sh\n" +
			"m=" + strconv.Quote(marker) + ".$$\n" +
			"for a in \"$0\" \"$@\"; do printf '%s\\n' \"$a\"; done > \"$m.argv\"\n" +
			"/usr/bin/env > \"$m.env\"\n" +
			"/bin/cat > \"$m.stdin\"\n"
		mustWrite(t, filepath.Join(s.bin, name), script, 0o755)
	}
}

// injections are the values every string input gets.
var injections = []string{"$(touch M)", "`touch M`", "; touch M", "| touch M", "&& touch M", "x\ntouch M", "--help", "-c"}

func (f *fixture) noProcess(t *testing.T) {
	sent := 0
	for _, route := range f.routes {
		for _, input := range route.row.strs {
			for _, value := range injections {
				target, body := route.build(t, route.with(f, input, value))
				header := f.a.authorized()
				if body != nil {
					header.Set("Content-Type", "application/json")
				}
				got := f.a.send(request{method: route.method, target: target, body: body, header: header})
				if got.status >= 500 {
					t.Errorf("%s with %s=%q answered %d %s", route.pattern, input, value, got.status, got.body)
				}
				sent++
			}
		}
		f.restoreRoots(t)
	}
	if sent == 0 {
		t.Fatal("no string input was attacked")
	}

	markers := filesUnder(t, f.s.markers)
	if len(programExecutables(t)) == 0 && len(markers) != 0 {
		t.Errorf("the program table is empty, so nothing may run, and these markers exist: %v", markers)
	}
	for _, marker := range markers {
		body, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range injections {
			if strings.Contains(string(body), strings.TrimSpace(value)) {
				t.Errorf("the marker %s carries the request input %q", marker, value)
			}
		}
	}
	f.noFileNamedM(t)
}

// noFileNamedM walks every scratch directory for a file an injection made.
func (f *fixture) noFileNamedM(t *testing.T) {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(f.s.base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == "M" {
			found = append(found, path)
		}
		return nil
	})
	sort.Strings(found)
	if len(found) != 0 {
		t.Errorf("an injected command ran: %v", found)
	}
}
