package codegen

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestInspectSaysWhatEachGeneratedFileIsWithoutWriting(t *testing.T) {
	t.Parallel()
	p := newProject(t)

	inspection, err := Inspect(context.Background(), p.dir)
	if err != nil || inspection.ManifestFound || len(inspection.Files) != 0 || exists(p.generated()) {
		t.Fatalf("before any run: %+v %v", inspection, err)
	}

	runOK(t, p, model("orders"), emitting(map[string]string{
		"same.txt": "s", "edited.txt": "e", "deleted.txt": "d", "orphan.txt": "o", "gone.txt": "g", "dir.txt": "x",
	}))
	runOK(t, p, model("orders"), emitting(map[string]string{"same.txt": "s", "edited.txt": "e", "deleted.txt": "d", "dir.txt": "x"}))
	write(t, p.generated("edited.txt"), "E")
	for _, name := range []string{"deleted.txt", "gone.txt", "dir.txt"} {
		if err := os.Remove(p.generated(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(p.generated("dir.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, p.generated("unlisted.txt"), "mine")
	before := p.whole(t)

	inspection, err = Inspect(context.Background(), p.dir)

	if err != nil || !inspection.ManifestFound {
		t.Fatalf("%+v %v", inspection, err)
	}
	got := fmt.Sprint(inspection.Files)
	want := fmt.Sprint([]InspectedFile{
		{Path: "deleted.txt", Status: InspectMissing},
		{Path: "dir.txt", Status: InspectNotRegularFile},
		{Path: "edited.txt", Status: InspectModified},
		{Path: "orphan.txt", Status: InspectOrphaned},
		{Path: "same.txt", Status: InspectUnchanged},
	})
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	sameSnapshot(t, "the whole tree", before, p.whole(t))
}
