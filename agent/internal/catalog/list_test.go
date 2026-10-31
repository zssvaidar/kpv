// The list (#48): where a pipeline lives, what an entry carries, the roots it
// is scoped to, and every way a directory or a pipeline.yaml can be other
// than expected.

package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

func TestAPipelineIsADirectoryDirectlyInsideARootHoldingPipelineYAML(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "orders", "orders")
	pipelineAt(t, root, "Billing", "billing")
	pipelineAt(t, root, "deep/nested", "nested")         // two levels down: not looked at
	pipelineAt(t, root, ".hidden", "hidden")             // starts with ".": not scanned
	mkdir(t, filepath.Join(root, "empty"))               // no pipeline.yaml: not listed
	write(t, filepath.Join(root, "notes.txt"), "a file") // not a directory
	write(t, filepath.Join(root, "temp", ".pipeline.yaml.tmp-0123"), valid("temp"))
	modified := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "orders", FileName), modified, modified); err != nil {
		t.Fatal(err)
	}

	got := listed(t, w.catalog.List(), root)
	if !got.Available || got.Reason != "" || got.Truncated || got.SkippedLinks != 0 {
		t.Fatalf("the root is %+v", got)
	}
	// Byte order: an uppercase letter sorts before every lowercase one.
	if want := []string{"Billing", "orders"}; !reflect.DeepEqual(directories(got), want) {
		t.Fatalf("listed %v, want %v", directories(got), want)
	}
	orders := entry(t, got, "orders")
	if orders.Root != root || orders.Dir != filepath.Join(root, "orders") || orders.ID != "orders" ||
		orders.Name != "The orders pipeline" || orders.Language != "python" || !orders.Modified.Equal(modified) {
		t.Errorf("the entry is %+v", orders)
	}
	if orders.Pipeline == nil || string(orders.Pipeline.ID) != "orders" || orders.Steps == nil || orders.Unreadable != nil || !orders.Readable() {
		t.Errorf("the entry does not carry its parsed model: %+v", orders)
	}
	if len(orders.Duplicates) != 0 {
		t.Errorf("a unique id is marked duplicate: %v", orders.Duplicates)
	}
}

func TestARootThatHoldsPipelineYAMLIsThatOnePipeline(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("single")
	write(t, filepath.Join(root, FileName), valid("single"))
	pipelineAt(t, root, "inside", "inside")

	got := listed(t, w.catalog.List(), root)
	if len(got.Pipelines) != 1 {
		t.Fatalf("listed %v, want only the root", directories(got))
	}
	only := got.Pipelines[0]
	if only.Directory != "" || only.Dir != root || only.ID != "single" {
		t.Errorf("the root pipeline is %+v", only)
	}
}

func TestEveryRootIsListedInTheStoresOrderAndTwoListsAreIdentical(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	second := w.root("zeta")
	first := w.root("alpha")
	gone := w.root("gone")
	pipelineAt(t, second, "b", "b")
	pipelineAt(t, first, "a", "a")
	pipelineAt(t, gone, "c", "c")

	before := w.catalog.List()
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	list := w.catalog.List()
	var paths []string
	for _, root := range list.Roots {
		paths = append(paths, root.Path)
	}
	if want := []string{second, first, gone}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("roots are listed %v, want the store's order %v", paths, want)
	}
	missing := listed(t, list, gone)
	if missing.Available || missing.Reason != projectfs.KindNotFound || len(missing.Pipelines) != 0 || missing.Pipelines == nil {
		t.Errorf("a removed root is %+v, want unavailable notFound with no pipelines", missing)
	}
	if len(listed(t, before, gone).Pipelines) != 1 {
		t.Fatal("the root had a pipeline before it went")
	}
	if again := w.catalog.List(); !reflect.DeepEqual(again, list) {
		t.Errorf("two lists of an unchanged disk differ:\n%+v\n%+v", list, again)
	}
}

func TestEachListChecksTheRootsAgainSoAChangedRootIsUnavailable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "a", "a")
	// Another directory made at the root's path is not the one authorized.
	if err := os.Rename(root, root+".old"); err != nil {
		t.Fatal(err)
	}
	pipelineAt(t, root, "a", "a")
	got := listed(t, w.catalog.List(), root)
	if got.Available || got.Reason != projectfs.KindChanged || len(got.Pipelines) != 0 {
		t.Errorf("a replaced root is %+v, want unavailable changed", got)
	}
}

func TestARootThatGoesAwayWhileItIsScannedIsUnavailableNotPartial(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	for _, name := range []string{"a", "b", "c"} {
		pipelineAt(t, root, name, name)
	}
	w.catalog.hook.beforePin = func(name string) {
		if name == "b" {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		}
	}
	got := listed(t, w.catalog.List(), root)
	if got.Available || got.Reason != projectfs.KindNotFound || len(got.Pipelines) != 0 {
		t.Errorf("a root removed mid-scan is %+v, want unavailable with no pipelines", got)
	}
}

func TestARootRevokedWhileItIsScannedIsNotListedWithWhatItHad(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "a", "a")
	w.catalog.hook.scanning = func(string) {
		if _, err := w.roots.Revoke(root); err != nil {
			t.Error(err)
		}
	}
	if list := w.catalog.List(); len(list.Roots) != 0 {
		t.Errorf("a revoked root is still listed: %+v", list)
	}
}

func TestAPipelineYAMLThatDoesNotLoadIsListedUnreadableWithItsProblems(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	write(t, filepath.Join(root, "syntax", FileName), "id: a\nlanguage: [python\n")
	write(t, filepath.Join(root, "duplicate", FileName), valid("dup")+"id: again\n")
	// A hand-edited validate step without the onInvalid the schema requires.
	write(t, filepath.Join(root, "schema", FileName), strings.Replace(valid("schema"), "destination:",
		"processingSteps:\n  - id: check\n    type: validate\n    configuration:\n      rules:\n        - field: amount\ndestination:", 1))

	got := listed(t, w.catalog.List(), root)
	syntax := entry(t, got, "syntax")
	if syntax.Readable() || syntax.Unreadable.Kind != UnreadableParse || len(syntax.Unreadable.Problems) != 1 {
		t.Fatalf("a syntax error is %+v", syntax)
	}
	if problem := syntax.Unreadable.Problems[0]; problem.Message == "" || problem.Line == 0 {
		t.Errorf("the parse problem is %+v, want a message and a line", problem)
	}
	if syntax.ID != "" || syntax.Name != "" || syntax.Language != "" || syntax.Pipeline != nil || syntax.Modified.IsZero() {
		t.Errorf("an unreadable entry carries what it should not, or no time: %+v", syntax)
	}
	if duplicate := entry(t, got, "duplicate"); duplicate.Unreadable == nil || duplicate.Unreadable.Kind != UnreadableParse ||
		duplicate.Unreadable.Problems[0].Pointer != "/id" || duplicate.Unreadable.Problems[0].Line != 14 {
		t.Errorf("a duplicate key is %+v", duplicate.Unreadable)
	}
	schema := entry(t, got, "schema")
	if schema.Readable() || schema.Unreadable.Kind != UnreadableSchema || schema.ID != "" {
		t.Fatalf("a schema failure is %+v", schema)
	}
	found := false
	for _, problem := range schema.Unreadable.Problems {
		if problem.Pointer == "/processingSteps/0/configuration" && problem.Line == 12 && problem.Column == 7 && strings.Contains(problem.Message, "onInvalid") {
			found = true
		}
	}
	if !found {
		t.Errorf("no problem names the missing onInvalid with its line: %+v", schema.Unreadable.Problems)
	}
}

func TestAFileThatIsNotAPlainSmallFileIsNotRead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	outside := filepath.Join(w.home, "elsewhere")
	write(t, filepath.Join(outside, FileName), valid("outside"))
	pipelineAt(t, root, "target", "target")

	write(t, filepath.Join(root, "big", FileName), strings.Repeat("#", MaxFileSize+1))
	write(t, filepath.Join(root, "exact", FileName), valid("exact")+strings.Repeat("#", MaxFileSize-len(valid("exact"))))
	mkdir(t, filepath.Join(root, "inside-link"))
	if err := os.Symlink(filepath.Join(root, "target", FileName), filepath.Join(root, "inside-link", FileName)); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(root, "outside-link"))
	if err := os.Symlink(filepath.Join(outside, FileName), filepath.Join(root, "outside-link", FileName)); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(root, "directory", FileName))
	mkdir(t, filepath.Join(root, "fifo"))
	if err := syscall.Mkfifo(filepath.Join(root, "fifo", FileName), 0o644); err != nil {
		t.Fatal(err)
	}

	got := listed(t, w.catalog.List(), root)
	for name, want := range map[string]UnreadableKind{
		"big": UnreadableTooLarge, "inside-link": UnreadableLink, "outside-link": UnreadableLink,
		"directory": UnreadableNotRegularFile, "fifo": UnreadableNotRegularFile,
	} {
		found := entry(t, got, name)
		if found.Unreadable == nil || found.Unreadable.Kind != want || len(found.Unreadable.Problems) != 0 || found.Unreadable.Problems == nil {
			t.Errorf("%s is %+v, want %s with no problems", name, found.Unreadable, want)
		}
	}
	if exact := entry(t, got, "exact"); !exact.Readable() {
		t.Errorf("a file of exactly 1 MiB did not load: %+v", exact.Unreadable)
	}
}

func TestLinksAndUnreadableDirectoriesInARoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	w := newWorld(t)
	root := w.root("code")
	outside := filepath.Join(w.home, "elsewhere")
	pipelineAt(t, w.home, "elsewhere", "outside")
	pipelineAt(t, root, "real", "real")
	if err := os.Symlink(outside, filepath.Join(root, "to-outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "to-inside")); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"no-access": 0o000, "no-search": 0o600, "no-list": 0o300} {
		pipelineAt(t, root, name, name)
		if err := os.Chmod(filepath.Join(root, name), mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, name), 0o755) })
	}

	got := listed(t, w.catalog.List(), root)
	if got.SkippedLinks != 2 {
		t.Errorf("skipped %d links, want 2", got.SkippedLinks)
	}
	for _, name := range directories(got) {
		if strings.HasPrefix(name, "to-") {
			t.Errorf("the link %s is listed as a pipeline", name)
		}
	}
	for _, name := range []string{"no-access", "no-search", "no-list"} {
		if found := entry(t, got, name); found.Unreadable == nil || found.Unreadable.Kind != UnreadableUnreadable {
			t.Errorf("%s is %+v, want unreadable", name, found.Unreadable)
		}
	}
}

func TestADirectorySwappedForALinkDuringTheListIsNeverReadThrough(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "swapped", "mine")
	pipelineAt(t, w.home, "elsewhere", "theirs")
	w.catalog.hook.beforePin = func(name string) {
		if name != "swapped" {
			return
		}
		if err := os.Rename(filepath.Join(root, "swapped"), filepath.Join(root, ".moved")); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(filepath.Join(w.home, "elsewhere"), filepath.Join(root, "swapped")); err != nil {
			t.Error(err)
		}
	}
	got := listed(t, w.catalog.List(), root)
	if len(got.Pipelines) != 0 || got.SkippedLinks != 1 {
		t.Errorf("the swapped directory is listed %+v with %d links skipped", got.Pipelines, got.SkippedLinks)
	}
}

func TestTheListChangesNothingOnDisk(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "a", "a")
	write(t, filepath.Join(root, "a", ".pipeline.yaml.tmp-deadbeef"), "partial")
	write(t, filepath.Join(root, "b", ".pipeline.yaml.tmp-cafe"), valid("b"))
	write(t, filepath.Join(root, "c", FileName), "id: [")
	before := snapshot(t, root)
	list := w.catalog.List()
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("the list changed the disk\nbefore %v\nafter  %v", before, after)
	}
	if want := []string{"a", "c"}; !reflect.DeepEqual(directories(listed(t, list, root)), want) {
		t.Errorf("listed %v, want %v: a temporary file is not a pipeline", directories(listed(t, list, root)), want)
	}
}

func TestPipelinesSharingAnIDAreEachListedAndNameTheOthers(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	first := w.root("first")
	second := w.root("second")
	pipelineAt(t, first, "orders", "orders")
	pipelineAt(t, first, "copy", "Orders")
	pipelineAt(t, second, "orders", "ORDERS")
	pipelineAt(t, second, "other", "other")

	list := w.catalog.List()
	want := map[Location][]Location{
		{first, "orders"}:  {{first, "copy"}, {second, "orders"}},
		{first, "copy"}:    {{first, "orders"}, {second, "orders"}},
		{second, "orders"}: {{first, "copy"}, {first, "orders"}},
	}
	for location, others := range want {
		got := entry(t, listed(t, list, location.Root), location.Directory)
		if !reflect.DeepEqual(got.Duplicates, others) {
			t.Errorf("%v names %v, want %v", location, got.Duplicates, others)
		}
	}
	if other := entry(t, listed(t, list, second), "other"); len(other.Duplicates) != 0 {
		t.Errorf("a unique id names %v", other.Duplicates)
	}
	for location := range want {
		if _, err := w.catalog.Open(location); err != nil {
			t.Errorf("a duplicate cannot be opened: %v", err)
		}
	}
}

func TestARootWithMoreEntriesThanTheLimitIsTruncatedAndBounded(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	full := w.root("full")
	over := w.root("over")
	for index := range projectfs.MaxListing {
		pipelineAt(t, full, "p"+strconv.Itoa(index), "p"+strconv.Itoa(index))
		pipelineAt(t, over, "p"+strconv.Itoa(index), "p"+strconv.Itoa(index))
	}
	for index := range 5 {
		pipelineAt(t, over, "extra"+strconv.Itoa(index), "extra"+strconv.Itoa(index))
	}
	list := w.catalog.List()
	if got := listed(t, list, full); got.Truncated || len(got.Pipelines) != projectfs.MaxListing {
		t.Errorf("a root of exactly the limit is truncated=%v with %d pipelines", got.Truncated, len(got.Pipelines))
	}
	if got := listed(t, list, over); !got.Truncated || len(got.Pipelines) != projectfs.MaxListing {
		t.Errorf("a root over the limit is truncated=%v with %d pipelines, want truncated and %d", got.Truncated, len(got.Pipelines), projectfs.MaxListing)
	}
}

func TestTheListIsScopedToAuthorizedRoots(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	pipelineAt(t, w.home, "loose", "loose") // in home, but not a root
	if list := w.catalog.List(); len(list.Roots) != 0 {
		t.Fatalf("with no root, the list is %+v", list)
	}
	root := w.root("code")
	pipelineAt(t, root, "a", "a")
	list := w.catalog.List()
	if len(list.Roots) != 1 || len(list.Roots[0].Pipelines) != 1 {
		t.Errorf("the list is %+v, want the one root with its one pipeline", list)
	}
}

func TestTheListIsReadAgainOnEveryCall(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	if got := listed(t, w.catalog.List(), root); len(got.Pipelines) != 0 {
		t.Fatalf("an empty root lists %v", directories(got))
	}
	pipelineAt(t, root, "a", "a")
	if got := listed(t, w.catalog.List(), root); len(got.Pipelines) != 1 {
		t.Errorf("a pipeline made since the last list is not listed: %v", directories(got))
	}
}

func TestListLogLinesCarryCountsAndNoPath(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineAt(t, root, "secret-project-name", "a")
	w.catalog.List()
	logs := w.logs.String()
	if !strings.Contains(logs, "pipelines=1") {
		t.Errorf("the list was not logged with its counts: %s", logs)
	}
	if strings.Contains(logs, w.home) || strings.Contains(logs, "secret-project-name") {
		t.Errorf("a log line carries a path: %s", logs)
	}
}
