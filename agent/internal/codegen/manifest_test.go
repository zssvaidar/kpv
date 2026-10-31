package codegen

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAManifestThatCannotBeReadOrUnderstoodStopsTheRunAndIsNotWrittenOver(t *testing.T) {
	t.Parallel()
	hash := hashOf([]byte(fakeContent("orders")))
	entry := `{"path": "pipeline.txt", "sha256": "` + hash + `"}`
	cases := []struct {
		name     string
		manifest string
		want     ProblemKind
	}{
		{"not JSON", "{not json", ProblemManifestInvalid},
		{"empty", "", ProblemManifestInvalid},
		{"an array", "[]", ProblemManifestInvalid},
		{"no version", `{"files": []}`, ProblemManifestInvalid},
		{"a null version", `{"version": null, "files": []}`, ProblemManifestInvalid},
		{"a string version", `{"version": "1", "files": []}`, ProblemManifestInvalid},
		{"version 2", `{"version": 2, "files": []}`, ProblemManifestUnknownVersion},
		{"version 0", `{"version": 0, "files": []}`, ProblemManifestUnknownVersion},
		{"version 1.5", `{"version": 1.5, "files": []}`, ProblemManifestUnknownVersion},
		{"no files", `{"version": 1}`, ProblemManifestInvalid},
		{"null files", `{"version": 1, "files": null}`, ProblemManifestInvalid},
		{"an unknown field", `{"version": 1, "files": [], "extra": true}`, ProblemManifestInvalid},
		{"an unknown entry field", `{"version": 1, "files": [{"path": "a", "sha256": "` + hash + `", "size": 1}]}`, ProblemManifestInvalid},
		{"trailing data", `{"version": 1, "files": []} {}`, ProblemManifestInvalid},
		{"a duplicate path", `{"version": 1, "files": [` + entry + `,` + entry + `]}`, ProblemManifestInvalid},
		{"a path twice by case", `{"version": 1, "files": [` + entry + `,` + strings.Replace(entry, "pipeline", "Pipeline", 1) + `]}`, ProblemManifestInvalid},
		{"a short hash", `{"version": 1, "files": [{"path": "a", "sha256": "abc"}]}`, ProblemManifestInvalid},
		{"an uppercase hash", `{"version": 1, "files": [{"path": "a", "sha256": "` + strings.ToUpper(hash) + `"}]}`, ProblemManifestInvalid},
		{"a climbing path", `{"version": 1, "files": [{"path": "../src/handler.go", "sha256": "` + hash + `"}]}`, ProblemManifestInvalid},
		{"an absolute path", `{"version": 1, "files": [{"path": "/etc/passwd", "sha256": "` + hash + `"}]}`, ProblemManifestInvalid},
		{"the manifest itself", `{"version": 1, "files": [{"path": "` + ManifestName + `", "sha256": "` + hash + `"}]}`, ProblemManifestInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			write(t, p.generated(ManifestName), c.manifest)
			before := p.whole(t)

			report, err := Run(context.Background(), p.dir, model("orders"), fake)

			if got := problemOf(t, report, err).Kind; got != c.want {
				t.Fatalf("problem %s, want %s (%v)", got, c.want, err)
			}
			sameSnapshot(t, "the whole tree", before, p.whole(t))
			if _, err := Inspect(context.Background(), p.dir); err == nil {
				t.Fatal("inspect accepted it")
			}
		})
	}
}

func TestAManifestThatIsNotARegularFileOrCannotBeReadStopsTheRun(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, p *project){
		"a directory": func(t *testing.T, p *project) {
			if err := os.MkdirAll(p.generated(ManifestName), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"a link to a valid manifest": func(t *testing.T, p *project) {
			write(t, p.generated("real.json"), `{"version": 1, "files": []}`)
			symlink(t, "real.json", p.generated(ManifestName))
		},
		"unreadable": func(t *testing.T, p *project) {
			skipIfRoot(t)
			write(t, p.generated(ManifestName), `{"version": 1, "files": []}`)
			if err := os.Chmod(p.generated(ManifestName), 0); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			prepare(t, p)
			before := p.whole(t)
			report, err := Run(context.Background(), p.dir, model("orders"), fake)
			if got := problemOf(t, report, err).Kind; got != ProblemManifestUnreadable {
				t.Fatalf("problem %s (%v)", got, err)
			}
			sameSnapshot(t, "the whole tree", before, p.whole(t))
		})
	}
}

func TestTheManifestOnlyListsFilesWhoseBytesMatchedWhenItWasWritten(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	// The user replaces the file a moment before it is written; the run
	// notices, writes nothing over it, and the manifest does not claim it.
	edit := hooks{beforeWrite: func(path string) {
		if path == "b.txt" {
			write(t, p.generated("b.txt"), "the user's")
		}
	}}
	report, err := run(context.Background(), p.dir, model("orders"), []Generator{emitting(map[string]string{"a.txt": "a", "b.txt": "b", "c.txt": "c"})}, edit)
	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"a.txt": StatusWritten, "b.txt": StatusUnowned, "c.txt": StatusNotWritten})
	if got := entryOf(t, report, "b.txt").Reason; got != ReasonChangedDuringRun {
		t.Fatalf("reason %s", got)
	}
	if read(t, p.generated("b.txt")) != "the user's" || exists(p.generated("c.txt")) {
		t.Fatal("a file was written over or past the change")
	}
	if got := strings.Join(manifestPaths(t, p), " "); got != "a.txt" {
		t.Fatalf("manifest %s", got)
	}
}
