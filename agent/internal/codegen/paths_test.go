package codegen

import (
	"context"
	"testing"
)

func TestEveryPathAGeneratorMayNotWriteIsRefusedAndTheRunWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		paths  []string
		reason map[string]Reason
	}{
		{"empty", []string{""}, map[string]Reason{"": ReasonEmpty}},
		{"absolute", []string{"/etc/passwd"}, map[string]Reason{"/etc/passwd": ReasonAbsolute}},
		{"dot", []string{"./x.txt"}, map[string]Reason{"./x.txt": ReasonDotSegment}},
		{"dot in the middle", []string{"a/./x.txt"}, map[string]Reason{"a/./x.txt": ReasonDotSegment}},
		{"dot dot", []string{"../src/handler.go"}, map[string]Reason{"../src/handler.go": ReasonDotSegment}},
		{"dot dot that comes back", []string{"a/../x.txt"}, map[string]Reason{"a/../x.txt": ReasonDotSegment}},
		{"a lone dot dot", []string{".."}, map[string]Reason{"..": ReasonDotSegment}},
		{"empty segment", []string{"a//x.txt"}, map[string]Reason{"a//x.txt": ReasonEmptySegment}},
		{"NUL", []string{"x\x00.txt"}, map[string]Reason{"x\x00.txt": ReasonNUL}},
		{"backslash", []string{`a\x.txt`}, map[string]Reason{`a\x.txt`: ReasonBackslash}},
		{"backslash climbing", []string{`..\src\handler.go`}, map[string]Reason{`..\src\handler.go`: ReasonBackslash}},
		{"trailing separator", []string{"dir/"}, map[string]Reason{"dir/": ReasonTrailingSeparator}},
		{"duplicate", []string{"x.txt", "x.txt"}, map[string]Reason{"x.txt": ReasonDuplicate}},
		{"case", []string{"Main.go", "main.go"}, map[string]Reason{"Main.go": ReasonCaseCollision, "main.go": ReasonCaseCollision}},
		{"case in a directory", []string{"Pkg/a.go", "pkg/b.go"}, map[string]Reason{"Pkg/a.go": ReasonCaseCollision, "pkg/b.go": ReasonCaseCollision}},
		{"file and directory", []string{"a", "a/b.txt"}, map[string]Reason{"a": ReasonFileDirectoryCollision, "a/b.txt": ReasonFileDirectoryCollision}},
		{"file and directory by case", []string{"A", "a/b.txt"}, map[string]Reason{"A": ReasonFileDirectoryCollision, "a/b.txt": ReasonFileDirectoryCollision}},
		{"the manifest", []string{ManifestName}, map[string]Reason{ManifestName: ReasonManifest}},
		{"the manifest in another case", []string{"KPV-Manifest.JSON"}, map[string]Reason{"KPV-Manifest.JSON": ReasonManifest}},
		{"under the manifest", []string{ManifestName + "/x"}, map[string]Reason{ManifestName + "/x": ReasonManifest}},
		{"a temporary file name", []string{"a/.x.go.tmp-0123456789abcdef"}, map[string]Reason{"a/.x.go.tmp-0123456789abcdef": ReasonTemporaryName}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			before := p.whole(t)
			files := map[string]string{"fine.txt": "fine"}
			var generators []Generator
			generators = append(generators, emitting(files))
			for _, path := range c.paths {
				generators = append(generators, emitting(map[string]string{path: "content"}))
			}

			report, err := Run(context.Background(), p.dir, model("orders"), generators...)

			if problemOf(t, report, err).Kind != ProblemInvalidOutput {
				t.Fatalf("problem %+v", report.Problem)
			}
			want := map[string]Status{"fine.txt": StatusNotWritten}
			for path := range c.reason {
				want[path] = StatusRefused
			}
			wantStatuses(t, report, want)
			for path, reason := range c.reason {
				if got := entryOf(t, report, path).Reason; got != reason {
					t.Fatalf("%q refused as %s, want %s", path, got, reason)
				}
			}
			sameSnapshot(t, "the whole tree", before, p.whole(t))
		})
	}
}

func TestPathsThatLookUnusualButAreFineAreAccepted(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	files := map[string]string{
		".hidden": "h", "a/b/c/d.go": "d", "...": "dots", "x..y": "x", "tmp-file": "t",
		".x.tmp-short": "not the guard's shape", "src/inside-generated.txt": "s", "with space.txt": "w",
	}
	report := runOK(t, p, model("orders"), emitting(files))
	for path := range files {
		if statuses(report)[path] != StatusWritten {
			t.Fatalf("%q: %+v", path, report.Files)
		}
		if read(t, p.generated(splitSlash(path)...)) != files[path] {
			t.Fatalf("%q content", path)
		}
	}
	if read(t, p.pipe+"/src/handler.go") != "package src // the user's code\n" {
		t.Fatal("a generated src/ reached the user's src/")
	}
}

func splitSlash(path string) []string {
	var out []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return append(out, path[start:])
}

func TestTheGuardsTemporaryNameIsRecognisedExactly(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		".main.go.tmp-0123456789abcdef":           "main.go",
		".kpv-manifest.json.tmp-ffffffffffffffff": ManifestName,
		".a.tmp-b.tmp-0000000000000000":           "a.tmp-b",
	} {
		if got, ok := temporaryBase(name); !ok || got != want {
			t.Errorf("%q: got %q %v, want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{
		"main.go.tmp-0123456789abcdef", ".main.go.tmp-0123456789ABCDEF", ".main.go.tmp-0123456789abcde",
		".main.go.tmp-0123456789abcdef0", "..tmp-0123456789abcdef", ".main.go", ".main.go.tmp-",
	} {
		if _, ok := temporaryBase(name); ok {
			t.Errorf("%q was taken for a temporary file", name)
		}
	}
}
