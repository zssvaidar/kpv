// Structural tests: which packages this one may use, and which may use it.
// They read the non-test source, because an import is not observable from
// behaviour.

package catalog

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/zssvaidar/kpv/agent"

// imports is every import of the non-test Go files in directory.
func imports(t *testing.T, directory string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		parsed++
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			out[path] = true
		}
	}
	if parsed == 0 {
		t.Fatalf("no source files in %s", directory)
	}
	return out
}

func TestThisPackageImportsOnlyProjectfsAndPipelineFromTheAgent(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{module + "/internal/projectfs": true, module + "/internal/pipeline": true}
	found := imports(t, ".")
	for path := range found {
		if strings.HasPrefix(path, module) && !allowed[path] {
			t.Errorf("internal/catalog imports %s", path)
		}
		// The standard library only, apart from the two agent packages.
		if !strings.HasPrefix(path, module) && strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			t.Errorf("internal/catalog imports %s, which is not the standard library", path)
		}
		// Every filesystem operation goes through the guard.
		if path == "os" || path == "path/filepath" || path == "os/exec" || path == "syscall" {
			t.Errorf("internal/catalog imports %s; its filesystem access is the guard's", path)
		}
	}
	for path := range allowed {
		if !found[path] {
			t.Errorf("internal/catalog no longer imports %s, so this test is looking at the wrong thing", path)
		}
	}
}

// The packages below catalog do not reach up into it, and the HTTP API may
// hold a catalog but the codegen and secrets packages, which are handed a
// pipeline directory by their caller, do not look one up themselves.
func TestTheLowerPackagesDoNotImportThisPackage(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"../projectfs", "../pipeline", "../codegen", "../secrets", "../channel"} {
		if imports(t, directory)[module+"/internal/catalog"] {
			t.Errorf("%s imports internal/catalog", directory)
		}
	}
}
