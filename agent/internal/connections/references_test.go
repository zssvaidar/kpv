package connections

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// referencedWorld is a world with pipelines in two roots: one naming
// eu-cluster on its source, one on its destination, one naming another
// connection, one naming none, one that does not parse and one that fails the
// schema, plus a root that is itself a pipeline naming eu-cluster on both.
func referencedWorld(t *testing.T) (*world, map[string][]byte) {
	t.Helper()
	w := newWorld(t)
	code := w.root("code")
	solo := w.root("solo")
	files := map[string]string{
		filepath.Join(code, "orders", catalog.FileName):  pipelineYAML("orders", "eu-cluster", ""),
		filepath.Join(code, "billing", catalog.FileName): pipelineYAML("billing", "", "eu-cluster"),
		filepath.Join(code, "other", catalog.FileName):   pipelineYAML("other", "us-cluster", "EU-cluster"),
		filepath.Join(code, "plain", catalog.FileName):   pipelineYAML("plain", "", ""),
		filepath.Join(code, "broken", catalog.FileName):  "id: [unclosed\n",
		filepath.Join(code, "invalid", catalog.FileName): "id: x\n",
		filepath.Join(solo, catalog.FileName):            pipelineYAML("solo", "eu-cluster", "eu-cluster"),
	}
	before := map[string][]byte{}
	for path, content := range files {
		writeFile(t, path, content)
		before[path] = []byte(content)
	}
	if _, err := w.store.Create(draft(saslRecord(t, "eu-cluster"))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Create(draft(saslRecord(t, "unused"))); err != nil {
		t.Fatal(err)
	}
	return w, before
}

func unchanged(t *testing.T, before map[string][]byte) {
	t.Helper()
	for path, content := range before {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(content) {
			t.Errorf("%s changed: %v", path, err)
		}
	}
}

func TestDeletingAReferencedConnectionReportsEveryReferenceAndChangesNothing(t *testing.T) {
	t.Parallel()
	w, before := referencedWorld(t)
	code, solo := filepath.Join(w.home, "code"), filepath.Join(w.home, "solo")
	_, err := w.store.Delete(draft(map[string]any{"name": "eu-cluster"}))
	var refused *Error
	if !asError(err, &refused) || refused.Kind != KindReferenced || refused.Report == nil {
		t.Fatalf("Delete = %v", err)
	}
	wantReferences := []Reference{
		{Location: catalog.Location{Root: code, Directory: "billing"}, PipelineID: "billing", Block: BlockDestination},
		{Location: catalog.Location{Root: code, Directory: "orders"}, PipelineID: "orders", Block: BlockSource},
		{Location: catalog.Location{Root: solo}, PipelineID: "solo", Block: BlockSource},
		{Location: catalog.Location{Root: solo}, PipelineID: "solo", Block: BlockDestination},
	}
	if !reflect.DeepEqual(refused.Report.References, wantReferences) {
		t.Errorf("references\n got %+v\nwant %+v", refused.Report.References, wantReferences)
	}
	wantUnchecked := []Unchecked{
		{Location: catalog.Location{Root: code, Directory: "broken"}, Reason: string(catalog.UnreadableParse)},
		{Location: catalog.Location{Root: code, Directory: "invalid"}, Reason: string(catalog.UnreadableSchema)},
	}
	if !reflect.DeepEqual(refused.Report.Unchecked, wantUnchecked) {
		t.Errorf("unchecked\n got %+v\nwant %+v", refused.Report.Unchecked, wantUnchecked)
	}
	if _, ok := w.store.Get("eu-cluster"); !ok {
		t.Fatal("a refused delete deleted")
	}
	unchanged(t, before)

	// The same request confirmed goes ahead, and the pipelines keep the name,
	// which now names no connection.
	remaining, err := w.store.Delete(draft(map[string]any{"name": "eu-cluster", "confirm": true}))
	if err != nil {
		t.Fatalf("confirmed Delete: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Name != "unused" {
		t.Fatalf("remaining %+v", remaining)
	}
	unchanged(t, before)
	for _, entry := range w.catalog.List().Roots[1].Pipelines {
		if entry.Pipeline == nil || entry.Pipeline.Source.Connection == nil || string(*entry.Pipeline.Source.Connection) != "eu-cluster" {
			t.Fatalf("the dangling name is not read back: %+v", entry)
		}
	}
}

func TestDeletingAConnectionNoPipelineNamesNeedsNoConfirmation(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	writeFile(t, filepath.Join(w.root("code"), "plain", catalog.FileName), pipelineYAML("plain", "", "other"))
	if _, err := w.store.Create(draft(saslRecord(t, "unused"))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Delete(draft(map[string]any{"name": "unused"})); err != nil {
		t.Fatalf("Delete = %v", err)
	}
}

// An unreadable pipeline needs the same confirmation as a reference, even
// when no readable pipeline names the connection.
func TestAnUncheckedPipelineNeedsTheSameConfirmationAsAReference(t *testing.T) {
	t.Parallel()
	w, _ := referencedWorld(t)
	_, err := w.store.Delete(draft(map[string]any{"name": "unused"}))
	var refused *Error
	if !asError(err, &refused) || refused.Kind != KindReferenced || len(refused.Report.References) != 0 || len(refused.Report.Unchecked) != 2 {
		t.Fatalf("Delete = %v %+v", err, refused)
	}
}

func TestRenamingAReferencedConnectionIsReportedAndRewritesNoPipeline(t *testing.T) {
	t.Parallel()
	w, before := referencedWorld(t)
	renamed := update("eu-cluster", saslRecord(t, "eu-west"), map[string]any{"saslPassword": nil, "tlsClientKey": nil})
	_, err := w.store.Update(draft(renamed))
	var refused *Error
	if !asError(err, &refused) || refused.Kind != KindReferenced || len(refused.Report.References) != 4 {
		t.Fatalf("Update = %v", err)
	}
	if _, ok := w.store.Get("eu-cluster"); !ok {
		t.Fatal("a refused rename renamed")
	}
	// An edit that keeps the name is not a rename and needs no report.
	kept := update("eu-cluster", saslRecord(t, "eu-cluster"), map[string]any{"saslPassword": nil, "tlsClientKey": nil, "saslUsername": "x"})
	if _, err := w.store.Update(draft(kept)); err != nil {
		t.Fatalf("an edit without a rename: %v", err)
	}
	renamed["confirm"] = true
	if _, err := w.store.Update(draft(renamed)); err != nil {
		t.Fatalf("confirmed rename: %v", err)
	}
	if connection, ok := w.store.Get("eu-west"); !ok || connection.SASLPassword.Reveal() != knownPassword {
		t.Fatal("the renamed connection lost its password")
	}
	unchanged(t, before)
}

func TestAnUnavailableOrTruncatedRootIsUnchecked(t *testing.T) {
	t.Parallel()
	gone := catalog.Root{Path: "/home/u/gone", Reason: projectfs.KindNotFound, Pipelines: []catalog.Entry{}}
	big := catalog.Root{Path: "/home/u/big", Available: true, Truncated: true, Pipelines: []catalog.Entry{}}
	store := Open(nil, fakeLister{catalog.List{Roots: []catalog.Root{gone, big}}}, nil)
	if _, err := store.Create(draft(saslRecord(t, "a"))); err != nil {
		t.Fatal(err)
	}
	report := store.References("a")
	want := []Unchecked{
		{Location: catalog.Location{Root: "/home/u/gone"}, Reason: UncheckedRootUnavailable},
		{Location: catalog.Location{Root: "/home/u/big"}, Reason: UncheckedTruncated},
	}
	if len(report.References) != 0 || !reflect.DeepEqual(report.Unchecked, want) {
		t.Fatalf("report %+v", report)
	}
	if _, err := store.Delete(draft(map[string]any{"name": "a"})); KindOf(err) != KindReferenced {
		t.Fatalf("Delete = %v", err)
	}
}
