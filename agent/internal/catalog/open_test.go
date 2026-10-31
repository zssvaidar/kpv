// Opening an entry by its root and directory (#48), and what it answers when
// the entry changed since it was listed.

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

func TestOpeningAnEntryAnswersItsDocumentAndTheHashOfItsBytes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "orders", "orders")
	bytes := read(t, filepath.Join(root, "orders", FileName))

	document, err := w.catalog.Open(Location{Root: root, Directory: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(bytes))
	if document.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("the hash is %s, want that of the bytes read", document.SHA256)
	}
	if document.Dir != filepath.Join(root, "orders") || document.Root != root || document.Directory != "orders" {
		t.Errorf("the document is at %+v", document.Location)
	}
	var decoded map[string]any
	if err := json.Unmarshal(document.JSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["id"] != "orders" || document.Pipeline == nil || string(document.Pipeline.ID) != "orders" {
		t.Errorf("the document is %s", document.JSON)
	}
	// What the file holds, with no default it left out filled in.
	if strings.Contains(string(document.JSON), "logLevel") || strings.Contains(string(document.JSON), "autoOffsetReset") {
		t.Errorf("the document gained defaults: %s", document.JSON)
	}
}

func TestARootThatIsAPipelineIsOpenedWithNoDirectory(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("single")
	write(t, filepath.Join(root, FileName), valid("single"))
	pipelineAt(t, root, "inside", "inside")
	if document, err := w.catalog.Open(Location{Root: root}); err != nil || document.Dir != root {
		t.Errorf("open the root pipeline: %v %+v", err, document)
	}
	// The list never shows a pipeline inside a root that is one.
	if _, err := w.catalog.Open(Location{Root: root, Directory: "inside"}); kindOf(t, err) != KindNotFound {
		t.Errorf("a pipeline nested in a root pipeline opened: %v", err)
	}
}

func TestADirectoryNameThatIsNotOneNameIsRefusedInvalid(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "a", "a")
	for _, name := range []string{".", "..", "a/b", "../code/a", `a\b`, "a\x00", "/a"} {
		if _, err := w.catalog.Open(Location{Root: root, Directory: name}); kindOf(t, err) != KindInvalid {
			t.Errorf("%q answered %v, want invalid", name, err)
		}
	}
	if _, err := w.catalog.Open(Location{Directory: "a"}); kindOf(t, err) != KindInvalid {
		t.Errorf("no root answered %v, want invalid", err)
	}
}

func TestAnEntryThatChangedSinceTheListAnswersWhatItIsNow(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	gone := w.root("gone")
	revoked := w.root("revoked")
	for _, r := range []string{root, gone, revoked} {
		pipelineAt(t, r, "a", "a")
	}
	pipelineAt(t, root, "broken", "broken")
	pipelineAt(t, root, "removed", "removed")
	pipelineAt(t, root, ".hidden", "hidden")
	pipelineAt(t, w.home, "elsewhere", "theirs")
	w.catalog.List()

	write(t, filepath.Join(root, "broken", FileName), "id: [")
	if err := os.RemoveAll(filepath.Join(root, "removed")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(w.home, "elsewhere"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := w.roots.Revoke(revoked); err != nil {
		t.Fatal(err)
	}

	_, err := w.catalog.Open(Location{Root: root, Directory: "broken"})
	if fault, ok := err.(*Error); !ok || fault.Kind != KindUnreadable || fault.Unreadable.Kind != UnreadableParse || len(fault.Unreadable.Problems) == 0 {
		t.Errorf("an entry that became unreadable answered %v", err)
	}
	for name, want := range map[string]ErrorKind{"removed": KindNotFound, "link": KindNotFound, ".hidden": KindNotFound, "never": KindNotFound} {
		if _, err := w.catalog.Open(Location{Root: root, Directory: name}); kindOf(t, err) != want {
			t.Errorf("%s answered %v, want %s", name, err, want)
		}
	}
	_, err = w.catalog.Open(Location{Root: gone, Directory: "a"})
	if fault, ok := err.(*Error); !ok || fault.Kind != KindRootUnavailable || fault.Reason != projectfs.KindNotFound {
		t.Errorf("an entry whose root went away answered %v", err)
	}
	if _, err := w.catalog.Open(Location{Root: revoked, Directory: "a"}); kindOf(t, err) != KindNotAuthorized {
		t.Errorf("an entry whose root was revoked answered %v", err)
	}
	if _, err := w.catalog.Open(Location{Root: filepath.Join(w.home, "elsewhere")}); kindOf(t, err) != KindNotAuthorized {
		t.Errorf("a directory that is not a root answered %v", err)
	}
}

func TestOpeningASchemaFailureAnswersItsProblemsAndNoPipeline(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	write(t, filepath.Join(root, "a", FileName), strings.Replace(valid("a"), "  networkMode: docker\n", "  networkMode: bridge\n", 1))
	document, err := w.catalog.Open(Location{Root: root, Directory: "a"})
	fault, ok := err.(*Error)
	if document != nil || !ok || fault.Unreadable == nil || fault.Unreadable.Kind != UnreadableSchema {
		t.Fatalf("a schema failure opened as %+v, %v", document, err)
	}
	if problem := fault.Unreadable.Problems[0]; problem.Pointer != "/runtimeConfiguration/networkMode" || problem.Line != 13 {
		t.Errorf("the problem is %+v", problem)
	}
}
