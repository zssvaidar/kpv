// Package codegen emits the files a pipeline is generated into, and keeps the
// line between those files and the user's own code.
//
// A pipeline directory holds two kinds of code. Everything under generated/
// is the tool's: it is written from the pipeline model and rewritten whenever
// the model changes. Everything else, src/ above all, is the user's, and a run
// never creates, changes, renames or removes anything there. That line is
// drawn here and not by each generator: a Generator is a pure function from
// the model to paths and bytes, and this package decides whether each path may
// be written at all, so the boundary does not depend on a generator behaving.
//
// What under generated/ the tool wrote is recorded in a manifest,
// generated/kpv-manifest.json (ManifestName), with the SHA-256 of each file as
// it was written. The manifest is what lets a run tell its own output apart
// from a user's edit: a file whose bytes no longer hash to what the manifest
// says is modified, and is reported rather than overwritten; a file the
// manifest does not list is unowned, and is reported rather than overwritten.
// The one exception to both is a file that already holds exactly the bytes
// that would be written, which is reported unchanged and recorded, because
// that is what a run interrupted after its files and before its manifest
// leaves behind.
//
// Hashes are over the exact bytes on disk. Nothing is normalized: not line
// endings, not encoding, not a trailing newline. A generated file checked out
// through git with autocrlf, or saved by an editor that changed its line
// endings, is therefore modified, and a run refuses to overwrite it. That is
// the fail-closed direction: a run cannot tell such a change from an edit, and
// an edit is what this package exists not to destroy.
//
// Every write goes through the projectfs guard, through the descriptor of the
// authorized root the pipeline directory belongs to, and this package makes no
// filesystem call of its own (projectfs/boundary_test.go holds that). Each
// file is replaced atomically, with Path.WriteAtomically, so a run that fails
// part-way leaves every file it had not reached holding its previous bytes.
//
// Every directory a run looks into or writes into -- the pipeline directory,
// generated/, and each directory under it -- is held open with projectfs
// Path.Pin from the moment it is checked, and every file is reached by its
// single name relative to the pinned directory that holds it. So a directory
// replaced by a symbolic link at any point after the run checked it, inside
// the root or not, is never written through: the link is not followed, and
// what it points at is untouched. A pipeline directory whose path has a link
// on it when the run starts is refused with the guard's changed error.
//
// The same holds when a directory is renamed rather than replaced: the run
// keeps writing into the directory it checked, in its new place, and its
// report and manifest describe that directory. The next run finds the old
// name empty or gone and treats it that way.
//
// A first run killed before it has written any manifest leaves files the next
// run treats like any file no manifest lists: adopted when they hold exactly
// the bytes it would write, otherwise unowned and not overwritten, so a next
// run with a different model refuses until they are removed. That fails
// closed and is accepted; discarding such a file is #70's.
//
// What this does not do, written down so the edges are known: two agent
// processes running against the same directory are not kept apart (two runs
// in one agent are, see Run).
package codegen

import (
	"bytes"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

// GeneratedDir is the directory, inside a pipeline directory, that generated
// output lands in. Nothing a run writes is outside it.
const GeneratedDir = "generated"

// SourceDir is the directory, inside a pipeline directory, that holds the
// user's own code. A run never touches it; the name is here so that callers
// and documentation say the same thing.
const SourceDir = "src"

// ManifestName is the manifest's file name, directly inside GeneratedDir.
// No generator may emit it, under any letter case.
const ManifestName = "kpv-manifest.json"

// ManifestVersion is the manifest format this package reads and writes.
// A manifest with any other version is reported and left alone.
const ManifestVersion = 1

// File is one file a generator emits.
type File struct {
	// Path is relative to GeneratedDir, with forward slashes, for example
	// "main.go" or "internal/steps/filter.go".
	Path string
	// Content is the file's exact bytes.
	Content []byte
}

// Generator turns a pipeline model into files.
//
// It is a pure function of the model: it returns paths and bytes and never
// touches the filesystem. Where those bytes may go is decided by Run, not by
// the generator.
type Generator interface {
	// Name identifies the generator in a report, for example "go".
	Name() string
	// Generate returns the files for model. An error fails the whole run and
	// nothing is written.
	Generate(model *pipeline.Pipeline) ([]File, error)
}

// GeneratorFunc makes a Generator out of a name and a function.
func GeneratorFunc(name string, generate func(*pipeline.Pipeline) ([]File, error)) Generator {
	return funcGenerator{name: name, generate: generate}
}

type funcGenerator struct {
	name     string
	generate func(*pipeline.Pipeline) ([]File, error)
}

func (g funcGenerator) Name() string { return g.name }

func (g funcGenerator) Generate(model *pipeline.Pipeline) ([]File, error) {
	return g.generate(model)
}

// copyFiles takes the generator's output by value, so a generator that holds
// on to its slices and changes them later cannot change what is written.
func copyFiles(files []File) []File {
	out := make([]File, len(files))
	for i, file := range files {
		out[i] = File{Path: file.Path, Content: bytes.Clone(file.Content)}
		if out[i].Content == nil {
			out[i].Content = []byte{}
		}
	}
	return out
}
