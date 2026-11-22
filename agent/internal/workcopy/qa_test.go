package workcopy

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// TestQAStage prepares a store for checking the review in the real app,
// until #77 or #97 stages anything. It is skipped unless KPV_QA_CONFIG names
// a scratch config directory (the XDG_CONFIG_HOME the agent will be started
// with; the store is its kpv/ directory). HOME must be the agent's scratch
// home, so the stored root is checked as the agent checks it.
//
//	HOME=/tmp/qa/home KPV_QA_CONFIG=/tmp/qa/config \
//	KPV_QA_ROOT=/tmp/qa/home/project KPV_QA_DIRECTORY=app \
//	KPV_QA_FILES=src/main.py,src/new.py KPV_QA_DELETE=src/old.py \
//	go test ./internal/workcopy/ -run TestQAStage -count=1
//
// The root must already be authorized in that store (authorize it through
// the running agent first). Each path in KPV_QA_FILES is staged with a line
// appended to the project file, or as a new file holding one line; each in
// KPV_QA_DELETE is staged as a deletion. KPV_QA_DIRECTORY is left out for a
// root that is itself the pipeline.
func TestQAStage(t *testing.T) {
	config := os.Getenv("KPV_QA_CONFIG")
	if config == "" {
		t.Skip("KPV_QA_CONFIG is not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	guard, err := projectfs.New()
	if err != nil {
		t.Fatal(err)
	}
	browser, err := projectfs.NewBrowser(guard, home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browser.Close() }()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	roots, err := projectfs.OpenRoots(browser, filepath.Join(config, "kpv", "roots.json"), logger)
	if err != nil {
		t.Fatal(err)
	}
	pipelines, err := catalog.New(roots, guard, logger)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := projectfs.OpenPrivateTree(roots, TreeName)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(pipelines, tree, logger)
	if err != nil {
		t.Fatal(err)
	}
	location := catalog.Location{Root: os.Getenv("KPV_QA_ROOT"), Directory: os.Getenv("KPV_QA_DIRECTORY")}
	var changes []Change
	for _, path := range split(os.Getenv("KPV_QA_FILES")) {
		current := []byte{}
		if source, err := pipelines.ReadSource(location, path); err == nil {
			current = source.Text
		} else if catalog.IsPipelineFile(path) {
			current = []byte(readPipeline(t, pipelines, location))
		}
		changes = append(changes, Change{Path: path, Text: append(append([]byte{}, current...), []byte("# staged by the QA helper\n")...)})
	}
	for _, path := range split(os.Getenv("KPV_QA_DELETE")) {
		changes = append(changes, Change{Path: path, Delete: true})
	}
	revision, err := store.Stage(location, changes...)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("staged %d changes, revision %s", len(changes), revision)
}

func split(list string) []string {
	var out []string
	for _, item := range strings.Split(list, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func readPipeline(t *testing.T, pipelines *catalog.Catalog, location catalog.Location) string {
	t.Helper()
	var text string
	err := pipelines.WithFiles(location, func(files *catalog.Files) error {
		current, err := files.Read(catalog.FileName)
		text = string(current.Data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return text
}
