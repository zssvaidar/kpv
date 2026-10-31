package codegen

import (
	"context"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// InspectStatus is what a generated file is against the manifest, without
// generating anything.
type InspectStatus string

const (
	// InspectUnchanged is a file whose bytes hash to what the manifest
	// recorded.
	InspectUnchanged InspectStatus = "unchanged"
	// InspectModified is a file whose bytes do not.
	InspectModified InspectStatus = "modified"
	// InspectMissing is a file the manifest lists that is not there.
	InspectMissing InspectStatus = "missing"
	// InspectOrphaned is a file the last run found no generator emitting,
	// still on disk. One that has been deleted is not listed, because the
	// next run drops it without a report.
	InspectOrphaned InspectStatus = "orphaned"
	// InspectNotRegularFile is a path the manifest lists that is now a
	// directory, a symbolic link or anything else that is not a regular file.
	InspectNotRegularFile InspectStatus = "notRegularFile"
)

// InspectedFile is one path the manifest lists.
type InspectedFile struct {
	// Path is relative to generated/.
	Path   string
	Status InspectStatus
	// Kind is the guard's error kind when the file could not be read, in
	// which case Status is InspectModified: it cannot be shown to be
	// unchanged.
	Kind projectfs.ErrorKind `json:",omitempty"`
}

// Inspection is the manifest read against the disk.
type Inspection struct {
	// ManifestFound says there is a manifest. Without one there is nothing
	// generated, and Files is empty.
	ManifestFound bool
	// Files is every path the manifest lists, sorted, except an orphan that
	// is gone.
	Files []InspectedFile
}

// Inspect reads dir/generated/'s manifest and says, for each file it lists,
// whether it is unchanged, modified, missing or orphaned. It writes nothing,
// and it waits for a run on the same directory to finish first, so it never
// sees one half-done. The failures are the same *Error a run reports for
// generated/ and the manifest.
func Inspect(ctx context.Context, dir projectfs.Path) (Inspection, error) {
	unlock, err := lockDirectory(ctx, dir.Real())
	if err != nil {
		return Inspection{}, &Error{Problem: Problem{Kind: ProblemInterrupted}, Err: err}
	}
	defer unlock()

	t, problem := openTree(dir)
	if problem != nil {
		return Inspection{}, problem
	}
	listed, problem := t.readManifest()
	if problem != nil {
		return Inspection{}, problem
	}
	inspection := Inspection{ManifestFound: t.manifestBytes != nil, Files: []InspectedFile{}}
	for _, path := range sortedPaths(listed) {
		entry := listed[path]
		if entry.Orphaned {
			if _, present := t.lookForOrphan(path); present {
				inspection.Files = append(inspection.Files, InspectedFile{Path: path, Status: InspectOrphaned})
			}
			continue
		}
		state := t.lookAtFile(path)
		file := InspectedFile{Path: path}
		switch {
		case state.absent:
			file.Status = InspectMissing
		case state.regular && hashOf(state.content) == entry.SHA256:
			file.Status = InspectUnchanged
		case state.regular:
			file.Status = InspectModified
		case state.report.Status == StatusFailed:
			file.Status = InspectModified
			file.Kind = state.report.Kind
		default:
			file.Status = InspectNotRegularFile
		}
		inspection.Files = append(inspection.Files, file)
	}
	return inspection, nil
}
