package process

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

// typedProgramBuilds type checks this package's non-test files that build
// here, plus an optional planted file, and answers every composite literal
// whose type is Program outside programs.go. Type checking sees the literal
// whose type is left out, `[]Program{{...}}`, which the syntax alone cannot.
func typedProgramBuilds(t *testing.T, plant string) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	names := map[*ast.File]string{}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if matched, err := build.Default.MatchFile(".", name); err != nil || !matched {
			continue
		}
		parsed, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		names[parsed] = name
		files = append(files, parsed)
	}
	if plant != "" {
		parsed, err := parser.ParseFile(set, "plant.go", "package process\n"+plant+"\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		names[parsed] = "plant.go"
		files = append(files, parsed)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("internal/process", set, files, info)
	if err != nil {
		t.Fatalf("type check: %v", err)
	}
	program := pkg.Scope().Lookup("Program").Type()
	var out []string
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// An element literal of a []*Program is typed *Program.
			literalType := info.Types[literal].Type
			if pointer, isPointer := literalType.(*types.Pointer); isPointer {
				literalType = pointer.Elem()
			}
			if types.Identical(literalType, program) && names[file] != "programs.go" {
				out = append(out, names[file]+" has a Program literal")
			}
			return true
		})
	}
	return out
}

func TestNoProgramLiteralOutsideProgramsGoEvenWithItsTypeLeftOut(t *testing.T) {
	t.Parallel()
	for _, build := range typedProgramBuilds(t, "") {
		t.Error(build)
	}
	for _, plant := range []string{
		`var _ = []Program{{executable: "x"}}`,
		`var _ = map[string]Program{"x": {executable: "x"}}`,
		`var _ = [...]*Program{{executable: "x"}}`,
	} {
		if len(typedProgramBuilds(t, plant)) == 0 {
			t.Errorf("the typed check misses %q", plant)
		}
	}
}
