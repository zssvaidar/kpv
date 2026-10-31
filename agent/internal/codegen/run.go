package codegen

import (
	"context"
	"io"
	"sort"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// fileMode is the mode a new generated file gets; one that is already there
// keeps its own.
const fileMode = 0o644

// hooks lets this package's tests act at a point inside a run, which is the
// only way to revoke a root or cancel a run between two writes. Run passes
// none.
type hooks struct {
	// beforeWrite is called before each file is written, with its path.
	beforeWrite func(path string)
}

// Run generates model into dir/generated/ with every generator in turn.
//
// dir is the pipeline directory, the one that holds generated/ and src/,
// already resolved through the guard; where a pipeline lives is not decided
// here. Only generated/ and what is under it is ever created, changed or
// removed.
//
// The order of a run is what makes it safe:
//
//  1. Every generator runs, and every path it returns is checked. Any refused
//     path, the manifest's own included, refuses the whole run.
//  2. generated/ and its manifest are read. A generated/ that is a link or
//     not a directory, or a manifest that cannot be read, parsed or
//     understood, stops the run.
//  3. Every output is checked against what is on disk and the manifest, and
//     every manifest entry no generator emits is looked for. A run that finds
//     any file it may not overwrite writes nothing and reports every one.
//  4. The files are written, each atomically, in path order.
//  5. The manifest is written, atomically and last, listing each file whose
//     bytes on disk were read back and matched; it is not written when it
//     would be the same bytes.
//
// A run that stops in step 4 reports which files were written and which were
// not, and still writes a manifest for what it did write if it can.
//
// The report is always returned. The error is a *Error, holding the same
// Problem, whenever the run did not complete. Two runs for the same directory
// in one agent never interleave: the second waits for the first, or returns
// ProblemInterrupted when ctx ends first.
func Run(ctx context.Context, dir projectfs.Path, model *pipeline.Pipeline, generators ...Generator) (Report, error) {
	return run(ctx, dir, model, generators, hooks{})
}

func run(ctx context.Context, dir projectfs.Path, model *pipeline.Pipeline, generators []Generator, hook hooks) (Report, error) {
	var report Report
	stop := func(problem *Error) (Report, error) {
		sortReports(report.Files)
		report.Problem = &problem.Problem
		return report, problem
	}
	if model == nil {
		return stop(&Error{Problem: Problem{Kind: ProblemInvalidModel}})
	}

	unlock, err := lockDirectory(ctx, dir.Real())
	if err != nil {
		return stop(&Error{Problem: Problem{Kind: ProblemInterrupted}, Err: err})
	}
	defer unlock()

	// 1. Generate and check every path before anything is looked at on disk.
	var outputs []File
	for _, generator := range generators {
		files, err := generator.Generate(model)
		if err != nil {
			return stop(&Error{Problem: Problem{Kind: ProblemGeneratorFailed, Generator: generator.Name()}, Err: err})
		}
		outputs = append(outputs, copyFiles(files)...)
	}
	paths := make([]string, len(outputs))
	for i, file := range outputs {
		paths[i] = file.Path
	}
	if refused := checkSet(paths); len(refused) > 0 {
		reported := map[string]bool{}
		for _, path := range paths {
			if reported[path] {
				continue
			}
			reported[path] = true
			if reason, ok := refused[path]; ok {
				report.Files = append(report.Files, FileReport{Path: path, Status: StatusRefused, Reason: reason})
			} else {
				report.Files = append(report.Files, FileReport{Path: path, Status: StatusNotWritten})
			}
		}
		return stop(&Error{Problem: Problem{Kind: ProblemInvalidOutput}})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })

	// 2. generated/ and the manifest.
	t, problem := openTree(dir)
	if problem != nil {
		return stop(problem)
	}
	previous, problem := t.readManifest()
	if problem != nil {
		return stop(problem)
	}

	// 3. Check everything before the first write.
	emitted := map[string]bool{}
	planned := make([]FileReport, len(outputs))
	conflicts := false
	for i, file := range outputs {
		emitted[file.Path] = true
		planned[i] = t.examine(file, entryFor(previous, file.Path))
		conflicts = conflicts || planned[i].Status.conflict()
	}
	var orphans []FileReport
	for _, path := range sortedPaths(previous) {
		if emitted[path] {
			continue
		}
		if orphan, present := t.lookForOrphan(path); present {
			orphans = append(orphans, orphan)
		}
	}
	if conflicts {
		for _, planned := range planned {
			if writes(planned.Status) {
				planned.Status = StatusNotWritten
			}
			report.Files = append(report.Files, planned)
		}
		report.Files = append(report.Files, orphans...)
		return stop(&Error{Problem: Problem{Kind: ProblemConflicts}})
	}

	// 4. Write.
	var failure *Error
	if !t.exists {
		if err := t.generated.Mkdir(0o755); err != nil {
			for _, planned := range planned {
				planned.Status = StatusNotWritten
				report.Files = append(report.Files, planned)
			}
			return stop(guardProblem(ProblemGuard, err))
		}
		t.exists = true
	}
	t.removeTemporaries(append(paths, sortedPaths(previous)...))
	for i, file := range outputs {
		if failure != nil {
			if writes(planned[i].Status) {
				planned[i].Status = StatusNotWritten
			}
			continue
		}
		if !writes(planned[i].Status) {
			continue
		}
		if err := ctx.Err(); err != nil {
			failure = &Error{Problem: Problem{Kind: ProblemInterrupted}, Err: err}
			planned[i].Status = StatusNotWritten
			continue
		}
		if hook.beforeWrite != nil {
			hook.beforeWrite(file.Path)
		}
		if blocked := t.makeDirectories(file.Path); blocked != nil {
			planned[i] = *blocked
			failure = &Error{Problem: Problem{Kind: ProblemWriteFailed, GuardKind: blocked.Kind}}
			if blocked.Status == StatusBlocked {
				failure.Problem.Kind = ProblemConflicts
			}
			continue
		}
		// Look again, a moment before writing: what the check found must
		// still be what is there.
		if again := t.examine(file, entryFor(previous, file.Path)); !writes(again.Status) {
			if again.Status == StatusUnchanged {
				planned[i] = again
				continue
			}
			if again.Reason == "" {
				again.Reason = ReasonChangedDuringRun
			}
			planned[i] = again
			failure = &Error{Problem: Problem{Kind: ProblemConflicts}}
			continue
		}
		if err := t.writeFile(file.Path, file.Content); err != nil {
			planned[i] = FileReport{Path: file.Path, Status: StatusFailed, Kind: kindOf(err)}
			failure = &Error{Problem: Problem{Kind: ProblemWriteFailed, GuardKind: kindOf(err)}, Err: err}
		}
	}

	// 5. The manifest, last, describing what is on disk now.
	next := manifest{}
	for i, file := range outputs {
		if landed(planned[i].Status) && t.holds(file.Path, file.Content) {
			next[file.Path] = manifestEntry{Path: file.Path, SHA256: hashOf(file.Content)}
		} else if entry := entryFor(previous, file.Path); entry != nil {
			next[file.Path] = manifestEntry{Path: entry.Path, SHA256: entry.SHA256}
		}
	}
	for _, orphan := range orphans {
		entry := previous[orphan.Path]
		entry.Orphaned = true
		next[orphan.Path] = entry
	}
	if encoded := next.encode(); string(encoded) != string(t.manifestBytes) {
		if err := t.writeFile(ManifestName, encoded); err != nil {
			if failure == nil {
				failure = &Error{Problem: Problem{Kind: ProblemWriteFailed, GuardKind: kindOf(err)}, Err: err}
			}
		} else {
			report.ManifestWritten = true
		}
	}

	report.Files = append(append(report.Files, planned...), orphans...)
	if failure != nil {
		return stop(failure)
	}
	sortReports(report.Files)
	return report, nil
}

// writes reports whether a planned status is one the run writes.
func writes(status Status) bool {
	return status == StatusWritten || status == StatusRegenerated
}

// landed reports whether a status ends with the run's bytes on disk.
func landed(status Status) bool {
	return writes(status) || status == StatusUnchanged
}

// writeFile replaces one file under generated/ atomically.
func (t *tree) writeFile(path string, content []byte) error {
	handle, err := t.at(path)
	if err != nil {
		return err
	}
	return handle.WriteAtomically(fileMode, func(w io.Writer) error {
		_, err := w.Write(content)
		return err
	})
}

// holds reports whether the file at path is a regular file with exactly
// content in it, read back from disk.
func (t *tree) holds(path string, content []byte) bool {
	state := t.lookAtFile(path)
	return state.regular && string(state.content) == string(content)
}

// lookForOrphan describes a manifest entry no generator emits. One that is
// gone is not reported, and is dropped from the manifest.
func (t *tree) lookForOrphan(path string) (FileReport, bool) {
	directory, _ := splitPath(path)
	switch state := t.directory(directory); state.condition {
	case directoryAbsent, directoryNotDirectory:
		return FileReport{}, false
	case directoryFailed:
		return FileReport{Path: path, Status: StatusOrphaned, Kind: state.kind}, true
	}
	handle, err := t.at(path)
	if err != nil {
		return FileReport{Path: path, Status: StatusOrphaned, Kind: kindOf(err)}, true
	}
	if _, err := handle.Lstat(); projectfs.IsNotFound(err) {
		return FileReport{}, false
	} else if err != nil {
		return FileReport{Path: path, Status: StatusOrphaned, Kind: kindOf(err)}, true
	}
	return FileReport{Path: path, Status: StatusOrphaned}, true
}

func entryFor(m manifest, path string) *manifestEntry {
	if entry, ok := m[path]; ok {
		return &entry
	}
	return nil
}

func sortedPaths(m manifest) []string {
	out := make([]string, 0, len(m))
	for path := range m {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func sortReports(files []FileReport) {
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}
