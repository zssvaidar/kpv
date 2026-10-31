// Structural tests: which packages this one may use, and which may use it.
// They read the non-test source, because an import is not observable from
// behaviour.

package secrets

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

func TestThisPackageImportsOnlyProjectfsFromTheAgentAndNoFilesystemPackage(t *testing.T) {
	t.Parallel()
	found := imports(t, ".")
	for path := range found {
		if strings.HasPrefix(path, module) && path != module+"/internal/projectfs" {
			t.Errorf("internal/secrets imports %s", path)
		}
		if !strings.HasPrefix(path, module) && strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			t.Errorf("internal/secrets imports %s, which is not the standard library", path)
		}
		if path == "os" || path == "path/filepath" || path == "os/exec" || path == "syscall" {
			t.Errorf("internal/secrets imports %s; its filesystem access is the guard's", path)
		}
	}
	if !found[module+"/internal/projectfs"] {
		t.Error("internal/secrets no longer imports internal/projectfs, so this test is looking at the wrong thing")
	}
}

func TestThePackagesBelowItDoNotImportIt(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"../projectfs", "../pipeline", "../codegen", "../channel"} {
		if imports(t, directory)[module+"/internal/secrets"] {
			t.Errorf("%s imports internal/secrets", directory)
		}
	}
}
